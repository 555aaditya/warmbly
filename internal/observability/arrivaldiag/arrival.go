package arrivaldiag

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type Stage uint8

const (
	Claim Stage = iota
	KeyLookup
	Decrypt
	Decode
	NewEmail
	Bounce
	Complaint
	Checkpoint
	Retire
	Retry
	Backlog
	ProbeObservation
	WarmupMarked
	WarmupUnmarked
	WarmupKnown
	WarmupThread
	WarmupReport
	WarmupFile
	WarmupDefer
	UniboxCreate
	Realtime
	Webhook
	Tagging
	ReplyAutomation
	Slack
	stageCount
)

var stageNames = [stageCount]string{
	"claim", "key_lookup", "decrypt", "decode", "new_email", "bounce", "complaint",
	"checkpoint", "retire", "retry", "backlog", "probe_observation", "warmup_marked",
	"warmup_unmarked", "warmup_known", "warmup_thread", "warmup_report", "warmup_file",
	"warmup_defer", "unibox_create", "realtime", "webhook", "tagging", "reply_automation", "slack",
}

type counter struct {
	attempts int64
	duration time.Duration
}

type Snapshot struct {
	stages [stageCount]counter
}

type Pass struct {
	mu    sync.Mutex
	now   func() time.Time
	stats Snapshot
}

type passKey struct{}

func WithPass(ctx context.Context) (context.Context, *Pass) {
	p := &Pass{now: time.Now}
	return context.WithValue(ctx, passKey{}, p), p
}

func (p *Pass) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}

var noop = func() {}

// Begin observes the existing call only; it neither inspects data nor changes context lifetime.
func Begin(ctx context.Context, stage Stage) func() {
	p, ok := ctx.Value(passKey{}).(*Pass)
	if !ok || stage >= stageCount {
		return noop
	}
	start := p.now()
	p.mu.Lock()
	p.stats.stages[stage].attempts++
	p.mu.Unlock()
	var ended atomic.Bool
	return func() {
		if !ended.CompareAndSwap(false, true) {
			return
		}
		elapsed := max(0, p.now().Sub(start))
		p.mu.Lock()
		p.stats.stages[stage].duration += elapsed
		p.mu.Unlock()
	}
}

type Summary struct {
	stats                               Snapshot
	passes, failed, cancelled, deadline int64
	duration, lastDuration, maxDuration time.Duration
}

type Window struct {
	summary Summary
	lastLog time.Time
}

// Record emits once per thirty seconds, plus one final shutdown summary.
func (w *Window) Record(stats Snapshot, elapsed time.Duration, failed, cancelled, deadline, final bool, now time.Time) *Summary {
	w.summary.passes++
	if failed {
		w.summary.failed++
	}
	if cancelled {
		w.summary.cancelled++
	}
	if deadline {
		w.summary.deadline++
	}
	w.summary.duration += max(0, elapsed)
	w.summary.lastDuration = max(0, elapsed)
	w.summary.maxDuration = max(w.summary.maxDuration, elapsed)
	for i, stage := range stats.stages {
		w.summary.stats.stages[i].attempts += stage.attempts
		w.summary.stats.stages[i].duration += stage.duration
	}
	if !final && !w.lastLog.IsZero() && now.Sub(w.lastLog) < 30*time.Second {
		return nil
	}
	w.lastLog = now
	return w.Flush()
}

// Flush reports only recorded, returned passes and is a no-op for an empty window.
func (w *Window) Flush() *Summary {
	if w.summary.passes == 0 {
		return nil
	}
	result := w.summary
	w.summary = Summary{}
	return &result
}

func (s *Summary) Log() {
	stages := zerolog.Dict()
	for i, stage := range s.stats.stages {
		if stage.attempts == 0 {
			continue
		}
		stages.Dict(stageNames[i], zerolog.Dict().Int64("attempts", stage.attempts).Int64("duration_ms", stage.duration.Milliseconds()))
	}
	log.Info().Int64("pass_attempts", s.passes).
		Int64("delivery_error_pass_attempts", s.failed).
		Int64("cancelled_pass_attempts", s.cancelled).
		Int64("deadline_pass_attempts", s.deadline).
		Int64("pass_duration_ms", s.duration.Milliseconds()).
		Int64("last_pass_duration_ms", s.lastDuration.Milliseconds()).
		Int64("max_pass_duration_ms", s.maxDuration.Milliseconds()).
		Dict("stages", stages).Msg("sync arrival delivery diagnostics")
}
