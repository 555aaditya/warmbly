package eventbus

import (
	"hash/fnv"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/rs/zerolog/log"
)

const natsLaneBuffer = 32

// natsLanes runs a keyed subscription on ordered lanes. JetStream times a
// delivery from when it was handed out, so every message the lanes hold,
// queued or running, is kept alive with an in-progress heartbeat.
type natsLanes struct {
	queues   []chan jetstream.Msg
	deliver  func(jetstream.Msg)
	halted   chan struct{}
	haltOnce sync.Once
	done     chan struct{}
	wg       sync.WaitGroup

	mu   sync.Mutex
	held map[jetstream.Msg]struct{}
}

func newNATSLanes(lanes int, ackWait time.Duration, deliver func(jetstream.Msg)) *natsLanes {
	p := &natsLanes{
		queues:  make([]chan jetstream.Msg, lanes),
		deliver: deliver,
		halted:  make(chan struct{}),
		done:    make(chan struct{}),
		held:    map[jetstream.Msg]struct{}{},
	}
	for i := range p.queues {
		p.queues[i] = make(chan jetstream.Msg, natsLaneBuffer)
		p.wg.Add(1)
		go p.run(p.queues[i])
	}
	go p.heartbeat(max(ackWait/3, 100*time.Millisecond))
	return p
}

func natsLane(key string, lanes int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % uint32(lanes))
}

// dispatch queues m behind every earlier message with the same key.
func (p *natsLanes) dispatch(m jetstream.Msg, key string) {
	p.hold(m)
	if p.isHalted() {
		p.abandon(m)
		return
	}
	select {
	case p.queues[natsLane(key, len(p.queues))] <- m:
	case <-p.halted:
		p.abandon(m)
	}
}

func (p *natsLanes) run(queue <-chan jetstream.Msg) {
	defer p.wg.Done()
	for m := range queue {
		if p.isHalted() {
			p.abandon(m)
			continue
		}
		p.deliver(m)
		p.release(m)
	}
}

func (p *natsLanes) hold(m jetstream.Msg) {
	p.mu.Lock()
	p.held[m] = struct{}{}
	p.mu.Unlock()
}

func (p *natsLanes) release(m jetstream.Msg) {
	p.mu.Lock()
	delete(p.held, m)
	p.mu.Unlock()
}

// abandon hands back a message that will not be handled here, so it is
// redelivered without waiting out its ack wait.
func (p *natsLanes) abandon(m jetstream.Msg) {
	p.release(m)
	if err := m.Nak(); err != nil {
		log.Warn().Err(err).Msg("eventbus nats nak failed")
	}
}

func (p *natsLanes) heartbeat(every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
		}
		p.mu.Lock()
		msgs := make([]jetstream.Msg, 0, len(p.held))
		for m := range p.held {
			msgs = append(msgs, m)
		}
		p.mu.Unlock()
		for _, m := range msgs {
			// A message settled since the snapshot refuses this, which is fine.
			if err := m.InProgress(); err != nil {
				log.Debug().Err(err).Msg("eventbus nats in-progress heartbeat failed")
			}
		}
	}
}

func (p *natsLanes) isHalted() bool {
	select {
	case <-p.halted:
		return true
	default:
		return false
	}
}

// halt stops handling anything not already running; queued messages are handed back.
func (p *natsLanes) halt() {
	p.haltOnce.Do(func() { close(p.halted) })
}

// stop waits for the running handlers once no more messages can be dispatched.
func (p *natsLanes) stop() {
	for _, q := range p.queues {
		close(q)
	}
	p.wg.Wait()
	close(p.done)
}
