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

func TestLiveDelayedPreparationDuplicateMustNotCreateDefinitiveConflict(t *testing.T) {
	h := liveDB(t)
	f := newSendResultFixture(t, h)
	s := liveJobsService(h)
	s.WarmupRepo = repository.NewWarmupRepository(h.Pool)
	s.InitEvents()
	ctx := t.Context()
	task, worker := uuid.New(), uuid.New()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := h.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO fleet_nodes(id,role,active,last_seen_at,warmup_send_protocol)VALUES($1,'worker',true,NOW(),1)`, worker)
	exec(`INSERT INTO workers(id)VALUES($1)`, worker)
	t.Cleanup(func() { _, _ = h.Exec(context.Background(), `DELETE FROM fleet_nodes WHERE id=$1`, worker) })
	exec(`UPDATE email_accounts SET worker_id=$1,test_mode='legacy' WHERE id=$2`, worker, f.mailbox)
	exec(`INSERT INTO tasks(id,email_account_id,task_type,status,message_id)VALUES($1,$2,'warmup','active','')`, task, f.mailbox)
	exec(`INSERT INTO warmup_tasks(task_id,target_account_id,lineage_version)VALUES($1,$2,1)`, task, f.mailbox)
	dispatch := s.TaskRepo.(repository.WarmupDispatchRepository)
	nonce, err := dispatch.AuthorizeWarmupDispatch(ctx, task, f.mailbox, worker)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch.DeferWarmupDispatch(ctx, task, f.mailbox, worker, nonce, time.Now()); err != nil {
		t.Fatal(err)
	}
	result := models.SendEmailResult{TaskID: task, SentAt: time.Now().UTC(), Error: &models.EmailSendError{Failure: &errx.SendFailure{
		Protocol: "internal", Stage: "prepare", Disposition: errx.SendRetry, Scope: "mailbox", ObservedAt: time.Now().UTC(),
	}}}
	if err := s.HandleEvent(ctx, &models.JobEvent{Type: models.JobEventTypeEmailFailed, Body: result}); err != nil {
		t.Fatal(err)
	}
	claim := s.TaskRepo.(interface {
		ClaimWarmupTask(context.Context, uuid.UUID, time.Time) (bool, error)
	})
	if claimed, err := claim.ClaimWarmupTask(ctx, task, time.Now().Add(time.Second)); err != nil || !claimed {
		t.Fatalf("claim=%v error=%v", claimed, err)
	}
	if next, err := dispatch.AuthorizeWarmupDispatch(ctx, task, f.mailbox, worker); err != nil || next == nonce {
		t.Fatalf("new authorization nonce=%s error=%v", next, err)
	}
	if err := s.HandleEvent(ctx, &models.JobEvent{Type: models.JobEventTypeEmailFailed, Body: result}); err != nil {
		t.Fatal(err)
	}
	var state, reason string
	var applied, held, started bool
	var audits int
	if err := h.QueryRow(ctx, `SELECT t.send_result_state,t.send_result_applied_at IS NOT NULL,ea.send_recovery_hold,
		COALESCE(ea.send_recovery_reason,''),w.dispatch_started_at IS NOT NULL,
		(SELECT count(*) FROM send_recovery_resolutions r WHERE r.recovery_task_id=t.id AND r.evidence_type='authoritative_result_conflict')
		FROM tasks t JOIN email_accounts ea ON ea.id=t.email_account_id JOIN warmup_tasks w ON w.task_id=t.id WHERE t.id=$1`, task).Scan(&state, &applied, &held, &reason, &started, &audits); err != nil {
		t.Fatal(err)
	}
	if state != "unknown" || !applied || !held || reason != "unknown" || started || audits != 0 {
		t.Errorf("delayed preparation duplicate after new authorization: state=%s applied=%v held=%v reason=%s started=%v automatic audits=%d", state, applied, held, reason, started, audits)
	}
	recovery := s.TaskRepo.(interface {
		RecoverUnstartedWarmupDispatches(context.Context, time.Time, int) (int, error)
	})
	if n, err := recovery.RecoverUnstartedWarmupDispatches(ctx, time.Now().Add(time.Second), 1); err != nil || n != 1 {
		t.Fatalf("positively nonce-gated unstarted retirement: retired=%d error=%v", n, err)
	}
	var status string
	var sent int
	if err := h.QueryRow(ctx, `SELECT t.status,ea.send_recovery_hold,COALESCE(ea.send_recovery_reason,''),ws.emails_sent,
		(SELECT count(*) FROM send_recovery_resolutions r WHERE r.recovery_task_id=t.id AND r.evidence_type='authoritative_result_conflict')
		FROM tasks t JOIN email_accounts ea ON ea.id=t.email_account_id JOIN warmup_statistics ws ON ws.email_account_id=ea.id AND ws.date=CURRENT_DATE WHERE t.id=$1`, task).Scan(&status, &held, &reason, &sent, &audits); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" || held || sent != 0 || audits != 0 {
		t.Fatalf("after safe unstarted retirement: status=%s held=%v reason=%s charged=%d automatic audits=%d", status, held, reason, sent, audits)
	}
}
