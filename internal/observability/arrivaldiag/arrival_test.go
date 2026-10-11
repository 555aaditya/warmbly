package arrivaldiag

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func TestArrivalDiagnosticsDeterministicCountsDurationAndContext(t *testing.T) {
	type marker struct{}
	parent, cancel := context.WithCancel(context.WithValue(t.Context(), marker{}, "sensitive payload context"))
	ctx, p := WithPass(parent)
	now := time.Unix(100, 0)
	p.now = func() time.Time { return now }
	finish := Begin(ctx, Claim)
	now = now.Add(12 * time.Millisecond)
	finish()
	finish()
	if got := p.Snapshot().stages[Claim]; got.attempts != 1 || got.duration != 12*time.Millisecond {
		t.Fatalf("non-deterministic/idempotent measurement: %+v", got)
	}
	if ctx.Value(marker{}) != parent.Value(marker{}) {
		t.Fatal("context values changed")
	}
	cancel()
	if ctx.Err() != context.Canceled {
		t.Fatal("cancellation did not propagate unchanged")
	}
	Begin(t.Context(), Claim)()
	Begin(ctx, Stage(255))()
	if p.Snapshot().stages[Claim].attempts != 1 {
		t.Fatal("invalid/no-context measurement changed counters")
	}
}

func TestArrivalDiagnosticsConcurrentCountsStayFinite(t *testing.T) {
	ctx, p := WithPass(t.Context())
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 32 {
				Begin(ctx, NewEmail)()
			}
		}()
	}
	wg.Wait()
	if got := p.Snapshot().stages[NewEmail]; got.attempts != 256 || got.duration < 0 {
		t.Fatalf("raced counters: %+v", got)
	}
}

func TestArrivalDiagnosticsCadenceAndShutdownAreBounded(t *testing.T) {
	var w Window
	now := time.Unix(100, 0)
	stats := Snapshot{}
	stats.stages[Claim] = counter{attempts: 2, duration: 5 * time.Millisecond}
	first := w.Record(stats, time.Second, false, false, false, false, now)
	if first == nil || first.passes != 1 {
		t.Fatal("first observation not emitted")
	}
	for i := 1; i < 6; i++ {
		if out := w.Record(stats, 2*time.Second, true, true, true, false, now.Add(time.Duration(i)*5*time.Second)); out != nil {
			t.Fatal("per-pass logging exceeded cadence")
		}
	}
	out := w.Record(stats, 3*time.Second, false, false, false, false, now.Add(30*time.Second))
	if out == nil || out.passes != 6 || out.failed != 5 || out.cancelled != 5 || out.deadline != 5 || out.stats.stages[Claim].attempts != 12 || out.duration != 13*time.Second || out.maxDuration != 3*time.Second || out.lastDuration != 3*time.Second {
		t.Fatalf("window lost attempted work: %+v", out)
	}
	final := w.Record(stats, time.Second, false, true, false, true, now.Add(31*time.Second))
	if final == nil || final.passes != 1 || final.cancelled != 1 {
		t.Fatal("terminal cancellation was lost")
	}
}

func TestArrivalDiagnosticsLogIsOnlyFiniteCountsAndDurations(t *testing.T) {
	var output bytes.Buffer
	previous := log.Logger
	log.Logger = zerolog.New(&output)
	t.Cleanup(func() { log.Logger = previous })
	type sensitiveKey struct{}
	ctx, p := WithPass(context.WithValue(t.Context(), sensitiveKey{}, "PRIVATE_PROVIDER_ERROR_ADDRESS_PAYLOAD_AUTH_UUID"))
	for stage := Stage(0); stage < stageCount; stage++ {
		Begin(ctx, stage)()
	}
	var w Window
	w.Record(p.Snapshot(), time.Second, true, true, true, false, time.Unix(100, 0)).Log()
	if strings.Contains(output.String(), "PRIVATE") || strings.Contains(output.String(), "error\"") {
		t.Fatal("diagnostics included a payload/raw error")
	}
	var fields map[string]any
	if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"level": true, "message": true, "pass_attempts": true, "delivery_error_pass_attempts": true, "cancelled_pass_attempts": true, "deadline_pass_attempts": true, "pass_duration_ms": true, "last_pass_duration_ms": true, "max_pass_duration_ms": true, "stages": true}
	for name, value := range fields {
		if !allowed[name] {
			t.Fatalf("unbounded field %s", name)
		}
		if name != "level" && name != "message" && name != "stages" {
			if _, ok := value.(float64); !ok {
				t.Fatalf("non-numeric diagnostic %s", name)
			}
		}
	}
	stages, ok := fields["stages"].(map[string]any)
	if !ok || len(stages) != int(stageCount) {
		t.Fatal("stage cardinality not fixed")
	}
	for _, name := range stageNames {
		entry, ok := stages[name].(map[string]any)
		if !ok || len(entry) != 2 || entry["attempts"] != float64(1) {
			t.Fatalf("unsafe/missing stage %s: %v", name, entry)
		}
		if _, ok := entry["duration_ms"].(float64); !ok {
			t.Fatal("duration is not numeric")
		}
	}
}
