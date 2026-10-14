package tasks

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

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
