package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// stubCreateRepo counts which of the two write paths the handler took.
type stubCreateRepo struct {
	repository.EmailAccountErrorRepository
	createdOnce int
	suppress    bool
	observedAt  *time.Time
	taskID      *uuid.UUID
	operation   models.MailTransportOperation
	fail        bool
}

func (s *stubCreateRepo) CreateOnce(_ context.Context, in *repository.CreateEmailAccountError) (*repository.EmailAccountError, *errx.Error) {
	s.createdOnce++
	s.observedAt = in.ObservedAt
	s.taskID = in.TaskID
	s.operation = in.Operation
	if s.fail {
		return nil, errx.InternalError()
	}
	if s.suppress {
		return nil, nil
	}
	return &repository.EmailAccountError{ErrorCode: in.ErrorCode}, nil
}

func TestServerErrorOperationDoesNotInferSyncFromMissingTask(t *testing.T) {
	for _, tc := range []struct {
		operation models.MailTransportOperation
		task      string
		want      models.MailTransportOperation
	}{
		{"", "", models.MailTransportUnknown},
		{"", uuid.NewString(), models.MailTransportUnknown},
		{models.MailTransportSync, "", models.MailTransportSync},
		{models.MailTransportSend, uuid.NewString(), models.MailTransportSend},
		{models.MailTransportSync, uuid.NewString(), models.MailTransportUnknown},
		{models.MailTransportSync, "invalid", models.MailTransportUnknown},
		{models.MailTransportSend, "invalid", models.MailTransportUnknown},
		{models.MailTransportSend, uuid.Nil.String(), models.MailTransportUnknown},
		{"future_version", "", models.MailTransportUnknown},
	} {
		repo := &stubCreateRepo{}
		s := &JobsService{EmailAccountErrorRepository: repo}
		if err := s.HandleEmailServerError(t.Context(), models.EmailErrorEvent{
			EmailAccountID: uuid.NewString(), UserID: uuid.NewString(), ErrorCode: "SERVER_UNREACHABLE",
			Operation: tc.operation, TaskID: tc.task,
		}); err != nil {
			t.Fatal(err)
		}
		if repo.operation != tc.want {
			t.Fatalf("operation=%q task=%q: got %q want %q", tc.operation, tc.task, repo.operation, tc.want)
		}
	}
}

func TestServerErrorStorageFailureRemainsRetryable(t *testing.T) {
	s := &JobsService{EmailAccountErrorRepository: &stubCreateRepo{fail: true}}
	if err := s.HandleEmailServerError(t.Context(), models.EmailErrorEvent{
		EmailAccountID: uuid.NewString(), UserID: uuid.NewString(), ErrorCode: "SERVER_UNREACHABLE",
	}); err == nil {
		t.Fatal("storage failure was acknowledged without preserving report")
	}
}

func TestServerErrorKeepsWorkerOccurrenceWhenResultDeliveryIsLate(t *testing.T) {
	repo := &stubCreateRepo{}
	s := &JobsService{EmailAccountErrorRepository: repo}
	stale := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	taskID := uuid.New()
	if err := s.HandleEmailServerError(context.Background(), models.EmailErrorEvent{
		EmailAccountID: uuid.NewString(), UserID: uuid.NewString(), TaskID: taskID.String(),
		ErrorCode: "SERVER_UNREACHABLE", Timestamp: stale.Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	if repo.observedAt == nil || !repo.observedAt.Equal(stale) || repo.taskID == nil || *repo.taskID != taskID {
		t.Fatalf("delayed send report lost its worker time or task scope: %v %v", repo.observedAt, repo.taskID)
	}
	if err := s.HandleEmailServerError(context.Background(), models.EmailErrorEvent{
		EmailAccountID: uuid.NewString(), UserID: uuid.NewString(), ErrorCode: "SERVER_UNREACHABLE",
	}); err != nil {
		t.Fatal(err)
	}
	if repo.observedAt != nil || repo.taskID != nil {
		t.Fatal("legacy event fabricated worker occurrence or send scope")
	}
	for _, timestamp := range []int64{-1, 0, time.Now().Add(time.Hour).Unix(), 1<<63 - 1} {
		if serverErrorObservedAt(timestamp) != nil {
			t.Fatalf("invalid worker clock accepted as provider occurrence: %d", timestamp)
		}
	}
}

// A mail server that refuses the same command on every pass is relayed on
// every pass. Recording each one unconditionally filled a mailbox's error
// list with an identical row a minute for as long as the refusal lasted
// (issue #405), so the server-error path has to write conditionally.
func TestServerErrorRecordsOnePerUnresolvedCode(t *testing.T) {
	repo := &stubCreateRepo{}
	s := &JobsService{EmailAccountErrorRepository: repo}

	event := models.EmailErrorEvent{
		EmailAccountID: uuid.NewString(),
		UserID:         uuid.NewString(),
		ErrorCode:      string(errx.MailErrorCodeImapUnknown),
		Message:        "Something went wrong: NO System Error",
		UserVisible:    true,
	}

	for i := 0; i < 3; i++ {
		if err := s.HandleEmailServerError(context.Background(), event); err != nil {
			t.Fatalf("HandleEmailServerError: %v", err)
		}
	}

	if repo.createdOnce != 3 {
		t.Errorf("CreateOnce called %d times, want one per relayed failure", repo.createdOnce)
	}
}

// A suppressed write is not an error: the row it wanted is already on screen.
func TestServerErrorSurvivesASuppressedWrite(t *testing.T) {
	repo := &stubCreateRepo{suppress: true}
	s := &JobsService{EmailAccountErrorRepository: repo}

	if err := s.HandleEmailServerError(context.Background(), models.EmailErrorEvent{
		EmailAccountID: uuid.NewString(),
		UserID:         uuid.NewString(),
		ErrorCode:      string(errx.MailErrorCodeImapUnknown),
		UserVisible:    true,
	}); err != nil {
		t.Fatalf("HandleEmailServerError: %v", err)
	}
	if repo.createdOnce != 1 {
		t.Fatalf("CreateOnce called %d times, want 1", repo.createdOnce)
	}
}
