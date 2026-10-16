package worker

import (
	"context"
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/client/smtpimap/smtp"
	"github.com/warmbly/warmbly/internal/infrastructure/codec"
	"github.com/warmbly/warmbly/internal/infrastructure/eventbus"
	"github.com/warmbly/warmbly/internal/models"
)

// A send's deadline must outlast the SMTP client's own budget, or a slow
// server's verdict is cut off after DATA and the result becomes unknown.
func TestReceiveGivesASendTheSMTPBudget(t *testing.T) {
	w := &WorkerService{Codec: codec.NewJSON()}
	left := map[models.WorkerEventType]time.Duration{}
	record := func(t models.WorkerEventType) func(context.Context, any) error {
		return func(ctx context.Context, _ any) error {
			d, ok := ctx.Deadline()
			if !ok {
				left[t] = -1
				return nil
			}
			left[t] = time.Until(d)
			return nil
		}
	}
	w.eventHandlers = map[models.WorkerEventType]func(context.Context, any) error{
		models.WorkerEventTypeSendEmail: record(models.WorkerEventTypeSendEmail),
		models.WorkerEventTypeAddEmail:  record(models.WorkerEventTypeAddEmail),
	}
	// The bus hands the handler the subscription's budget; Receive narrows it per event.
	ctx, cancel := context.WithTimeout(t.Context(), CommandBudget)
	defer cancel()
	for typ := range w.eventHandlers {
		payload, err := w.Codec.Serialize(ctx, "commands", &models.WorkerEvent{Type: typ, Body: map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Receive(ctx, eventbus.Message{Payload: payload}); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
	}

	if send := left[models.WorkerEventTypeSendEmail]; send <= smtp.SendTimeout {
		t.Fatalf("send deadline %s does not cover the SMTP budget %s", send, smtp.SendTimeout)
	}
	if other := left[models.WorkerEventTypeAddEmail]; other <= 0 || other > eventTimeout {
		t.Fatalf("non-send deadline %s, want at most %s", other, eventTimeout)
	}
	if CommandBudget < sendEventTimeout+sendResultReportTimeout {
		t.Fatalf("command budget %s ends before a send and its result report can", CommandBudget)
	}
}
