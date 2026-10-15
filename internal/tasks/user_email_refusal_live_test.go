package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/app/feature"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
	"github.com/warmbly/warmbly/internal/tasks/proto"
)

// A unibox send refused before dispatch provably never left: it ends failed
// with its admission code and no dead letter that looks retryable. One whose
// hand-off is unknown is kept as a dead letter, with no retry date (#956).
func TestLiveUniboxSendRefusalIsRecordedNotDeadLettered(t *testing.T) {
	handle := liveCampaignDB(t)
	f := newCampaignSendFixture(t, handle.Pool)
	for _, tc := range []struct {
		name       string
		fail       error
		wantStatus string
		wantDetail string
		deadLetter bool
	}{
		{"admission refused", repository.ErrManualSendLimit, "failed", "MANUAL_SEND_LIMIT_REACHED", false},
		{"worker offline", fmt.Errorf("%w: no worker", ErrWorkerOffline), "failed", "offline", false},
		{"hand-off unknown", fmt.Errorf("%w: publish timed out", ErrSendDispatchUnknown), "dead_lettered", "may already be on its way", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := liveCampaignService(t, handle, &recordingSender{fail: tc.fail})
			taskID := queueUniboxReply(t, handle.Pool, f.mailbox, "", "")
			if xerr := svc.HandleUserEmailTask(&proto.ProcessTask{TaskId: taskID.String()}); xerr != nil {
				t.Fatalf("handle reply: %v", xerr)
			}
			ctx := context.Background()
			var status, detail string
			if err := handle.Pool.QueryRow(ctx, `SELECT t.status, COALESCE(f.message,'') FROM tasks t
				LEFT JOIN task_failures f ON f.task_id = t.id WHERE t.id = $1`, taskID).Scan(&status, &detail); err != nil {
				t.Fatal(err)
			}
			if status != tc.wantStatus {
				t.Errorf("status = %q, want %q", status, tc.wantStatus)
			}
			if !strings.Contains(detail, tc.wantDetail) {
				t.Errorf("failure detail = %q, want it to name %q", detail, tc.wantDetail)
			}
			var letters int
			var retryAt *time.Time
			if err := handle.Pool.QueryRow(ctx, `SELECT COUNT(*), MAX(next_retry_at) FROM task_dead_letters WHERE task_id = $1`, taskID).Scan(&letters, &retryAt); err != nil {
				t.Fatal(err)
			}
			if (letters > 0) != tc.deadLetter {
				t.Errorf("dead letters = %d, want any = %v", letters, tc.deadLetter)
			}
			if retryAt != nil {
				t.Errorf("unibox dead letter carries next_retry_at %v, but it is never replayed", retryAt)
			}
		})
	}
}

// failingFailureRecord loses the write that records a refusal.
type failingFailureRecord struct {
	repository.TaskRepository
	recovery repository.SendResultRecovery
}

func (f failingFailureRecord) RecordTaskFailure(context.Context, uuid.UUID, string, string) error {
	return errors.New("connection reset")
}

func (f failingFailureRecord) ApplySendResult(ctx context.Context, r models.SendEmailResult, after func(context.Context) error) error {
	return f.recovery.ApplySendResult(ctx, r, after)
}

func (f failingFailureRecord) GetSendAdmission(ctx context.Context, org, mailbox uuid.UUID, p models.InboxProvider, at time.Time) (*repository.SendAdmission, error) {
	return f.recovery.GetSendAdmission(ctx, org, mailbox, p, at)
}

// A refusal that cannot be recorded is not acknowledged: nothing was
// reserved, so the task goes back to pending and the delivery is retried.
func TestLiveUnrecordedUniboxRefusalIsRetried(t *testing.T) {
	handle := liveCampaignDB(t)
	f := newCampaignSendFixture(t, handle.Pool)
	svc := liveCampaignService(t, handle, &recordingSender{fail: repository.ErrManualSendLimit})
	svc.taskRepo = failingFailureRecord{TaskRepository: svc.taskRepo, recovery: svc.taskRepo.(repository.SendResultRecovery)}
	taskID := queueUniboxReply(t, handle.Pool, f.mailbox, "", "")
	if xerr := svc.HandleUserEmailTask(&proto.ProcessTask{TaskId: taskID.String()}); xerr == nil {
		t.Fatal("an unrecorded refusal was acknowledged")
	}
	var status string
	if err := handle.Pool.QueryRow(context.Background(), `SELECT status FROM tasks WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Errorf("status = %q, want pending so the retry runs it again", status)
	}
}

// planGate answers the two gate questions a unibox send asks.
type planGate struct {
	feature.FeatureGateService
	daily int
}

func (g planGate) CanUseUnibox(context.Context, uuid.UUID) (bool, *errx.Error) { return true, nil }

func (g planGate) GetDailyEmailLimit(context.Context, uuid.UUID) (int, *errx.Error) {
	return g.daily, nil
}

// The workspace's plan sizes the allowance the send is reserved under: twice
// the plan's daily limit with a floor, and no bound on an unlimited plan or a
// self-hosted instance.
func TestLiveUniboxSendCarriesThePlanAllowance(t *testing.T) {
	handle := liveCampaignDB(t)
	f := newCampaignSendFixture(t, handle.Pool)
	for _, tc := range []struct {
		name  string
		daily int
		want  int
	}{
		{"starter", 150, 300},
		{"free trial", 20, 100},
		{"unlimited or self-hosted", -1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender := &recordingSender{}
			svc := liveCampaignService(t, handle, sender)
			svc.featureGate = planGate{daily: tc.daily}
			taskID := queueUniboxReply(t, handle.Pool, f.mailbox, "", "")
			if xerr := svc.HandleUserEmailTask(&proto.ProcessTask{TaskId: taskID.String()}); xerr != nil {
				t.Fatalf("handle reply: %v", xerr)
			}
			if got := sender.message(t, 0).ManualPlanAllowance; got != tc.want {
				t.Errorf("plan allowance = %d, want %d", got, tc.want)
			}
		})
	}
}
