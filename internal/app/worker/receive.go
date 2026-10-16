package worker

import (
	"context"
	"time"

	"github.com/warmbly/warmbly/internal/client/smtpimap/smtp"
	"github.com/warmbly/warmbly/internal/infrastructure/eventbus"
	"github.com/warmbly/warmbly/internal/models"
)

const (
	// eventTimeout bounds every worker command except a send.
	eventTimeout = 30 * time.Second
	// sendEventTimeout covers the SMTP client's whole conversation plus the
	// dispatch checks and body fetch that run before it.
	sendEventTimeout = smtp.SendTimeout + 30*time.Second
	// sendResultReportTimeout is the detached budget a send's outcome is recorded under.
	sendResultReportTimeout = 45 * time.Second
	// publishTimeout bounds the mailbox error event a failed send raises (the bus's default publish bound).
	publishTimeout = 30 * time.Second
)

// CommandBudget is the longest one worker command can hold its delivery: a
// send's deadline, its result report and the mailbox error it may raise. The
// command subscription runs under it so the bus does not hand a command out
// again while the first delivery is still working.
const CommandBudget = sendEventTimeout + sendResultReportTimeout + publishTimeout

type deliveryKey struct{}

// delivery is what a handler may know about the bus message it is processing.
type delivery struct {
	attempt    int  // 1-based delivery count; 0 when unknown
	redelivers bool // a handler error gets the message delivered again
}

// deliveryOf returns the bus delivery details for the message a handler is
// processing (zero value when the handler was not invoked from Receive).
func deliveryOf(ctx context.Context) delivery {
	d, _ := ctx.Value(deliveryKey{}).(delivery)
	return d
}

// eventDeadline is how long one command of the given type may run.
func eventDeadline(t models.WorkerEventType) time.Duration {
	if t == models.WorkerEventTypeSendEmail {
		return sendEventTimeout
	}
	return eventTimeout
}

// Receive is the eventbus.Handler that drives the worker's event loop. It
// decodes the wire payload via the injected codec.Codec and dispatches to
// HandleEvent.
func (w *WorkerService) Receive(ctx context.Context, msg eventbus.Message) error {
	var event models.WorkerEvent
	if err := w.Codec.Deserialize(ctx, msg.Topic, msg.Payload, &event); err != nil {
		return err
	}

	hctx, cancel := context.WithTimeout(context.WithValue(ctx, deliveryKey{}, delivery{attempt: msg.Attempt, redelivers: msg.Redelivers}), eventDeadline(event.Type))
	defer cancel()
	return w.HandleEvent(hctx, &event)
}
