package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/observability/arrivaldiag"
	"github.com/warmbly/warmbly/internal/repository"
)

type diagnosticArrivalStub struct {
	repository.ArrivalOutbox
	delivery     func(context.Context, func(context.Context, models.JobEventType, any) error) error
	backlogCalls int
}

func (s *diagnosticArrivalStub) DeliverArrivals(ctx context.Context, deliver func(context.Context, models.JobEventType, any) error) error {
	return s.delivery(ctx, deliver)
}
func (s *diagnosticArrivalStub) ArrivalBacklog(context.Context) (repository.ArrivalBacklog, error) {
	s.backlogCalls++
	return repository.ArrivalBacklog{}, nil
}

func TestArrivalDiagnosticsPassPreservesContextCallbackAndError(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		var output bytes.Buffer
		previous := log.Logger
		log.Logger = zerolog.New(&output)
		parent, cancel := context.WithDeadline(t.Context(), time.Now().Add(10*time.Second))
		defer cancel()
		type marker struct{}
		parent = context.WithValue(parent, marker{}, "PRIVATE_CONTEXT_AUTH")
		deadline, _ := parent.Deadline()
		sentinel := errors.New("PRIVATE_ERROR_PROVIDER_PAYLOAD")
		body := &models.JobEventNewEmail{UserID: uuid.New(), Message: &models.EmailMessageStoreData{ID: uuid.New(), EmailID: uuid.New(), Subject: "PRIVATE_SUBJECT_ADDRESS_BODY"}}
		stub := &diagnosticArrivalStub{}
		s := &JobsService{ArrivalOutbox: stub}
		s.InitEvents()
		calls := 0
		var receivedContext context.Context
		Register(s, models.JobEventTypeNewEmail, func(ctx context.Context, event *models.JobEventNewEmail) error {
			calls++
			receivedContext = ctx
			if event != body || ctx.Value(marker{}) != parent.Value(marker{}) {
				t.Fatal("instrumentation changed payload/context values")
			}
			if got, _ := ctx.Deadline(); !got.Equal(deadline) {
				t.Fatal("parent deadline changed")
			}
			return sentinel
		})
		stub.delivery = func(ctx context.Context, deliver func(context.Context, models.JobEventType, any) error) error {
			finish := arrivaldiag.Begin(ctx, arrivaldiag.NewEmail)
			err := deliver(ctx, models.JobEventTypeNewEmail, body)
			finish()
			if err != sentinel {
				t.Fatal("callback error identity changed")
			}
			if cancelled {
				cancel()
			}
			return err
		}
		var window arrivaldiag.Window
		var failed, lastAlert time.Time
		s.runArrivalOutboxPass(parent, &window, &failed, &lastAlert)
		log.Logger = previous
		if calls != 1 || stub.backlogCalls != 1 || failed.IsZero() || !lastAlert.IsZero() {
			t.Fatalf("pass changed existing observe/delivery calls: %d %d", calls, stub.backlogCalls)
		}
		if receivedContext.Err() != context.Canceled {
			t.Fatal("pass context no longer cancelled after work")
		}
		if strings.Contains(output.String(), "PRIVATE") || strings.Contains(output.String(), body.UserID.String()) {
			t.Fatal("diagnostic logged payload, identity or error")
		}
		var fields map[string]any
		for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
			var row map[string]any
			if err := json.Unmarshal([]byte(line), &row); err != nil {
				t.Fatal(err)
			}
			if row["message"] == "sync arrival delivery diagnostics" {
				fields = row
			}
		}
		if fields["delivery_error_pass_attempts"] != float64(1) || fields["pass_attempts"] != float64(1) {
			t.Fatalf("attempt was mislabeled completion: %v", fields)
		}
		wantCancelled := float64(0)
		if cancelled {
			wantCancelled = 1
		}
		if fields["cancelled_pass_attempts"] != wantCancelled {
			t.Fatal("actual cancellation was lost")
		}
	}
}

type diagnosticInboxRepo struct {
	repository.UniboxRepository
	calls int
	err   error
}

func (s *diagnosticInboxRepo) CreateEntry(context.Context, uuid.UUID, *models.EmailMessageStoreData) error {
	s.calls++
	return s.err
}

type diagnosticEmailRepo struct {
	repository.EmailRepository
	calls int
}

func (s *diagnosticEmailRepo) GetByID(context.Context, uuid.UUID) (*models.Email, *errx.Error) {
	s.calls++
	return nil, nil
}

func TestArrivalDiagnosticsDependenciesDoNotAddCallsOrAcknowledgeErrors(t *testing.T) {
	for _, observed := range []bool{false, true} {
		inbox := &diagnosticInboxRepo{}
		accounts := &diagnosticEmailRepo{}
		s := &JobsService{UniboxRepository: inbox, EmailRepository: accounts}
		event := &models.JobEventNewEmail{UserID: uuid.New(), Message: &models.EmailMessageStoreData{ID: uuid.New(), EmailID: uuid.New(), Folder: models.FolderInbox}}
		ctx := t.Context()
		if observed {
			ctx, _ = arrivaldiag.WithPass(ctx)
		}
		if err := s.HandleNewEmail(ctx, event); err != nil {
			t.Fatal(err)
		}
		if inbox.calls != 1 || accounts.calls != 1 {
			t.Fatal("timing added dependency queries")
		}
		first := errors.New("private inbox write error")
		inbox.err = first
		if err := s.HandleNewEmail(ctx, event); err != first {
			t.Fatal("instrumentation acknowledged or wrapped write error")
		}
		if inbox.calls != 2 {
			t.Fatal("inbox write retried by diagnostics")
		}
	}
}
