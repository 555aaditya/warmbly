package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
	"github.com/warmbly/warmbly/internal/scheduler"
)

func newUnknownWarmupFixture(t *testing.T) *warmupRecoveryFixture {
	t.Helper()
	f := newWarmupRecoveryFixture(t)
	result := models.SendEmailResult{TaskID: f.task, Error: &models.EmailSendError{Code: "SERVER_UNREACHABLE", Failure: &errx.SendFailure{
		Protocol: "smtp", Stage: "message accept", Disposition: errx.SendAmbiguous, Scope: "mailbox",
	}}}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE tasks SET message_id='<uncertain@sender.test>',send_executor_started_at=NOW()-INTERVAL '1 hour',send_executor_result=$2 WHERE id=$1`, f.task, raw)
	f.exec(t, `UPDATE warmup_tasks SET dispatch_started_at=NOW()-INTERVAL '1 hour',dispatch_result=$2 WHERE task_id=$1`, f.task, raw)
	f.exec(t, `INSERT INTO outbound_attempts(nonce,task_id,email_account_id,provider,attempted_at,recipient_count) VALUES($1,$2,$3,'smtp_imap',NOW()-INTERVAL '1 hour',1)`, f.nonce, f.task, f.sender.ID)
	account, xerr := f.svc.emailRepo.GetByID(t.Context(), f.sender.ID)
	if xerr != nil {
		t.Fatal(xerr)
	}
	f.sender = *account
	return f
}

func unknownWarmupSnapshot(t *testing.T, f *warmupRecoveryFixture) string {
	t.Helper()
	var snapshot string
	if err := f.pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
		'task',to_jsonb(t)-'status'-'updated_at'-'warmup_send_retired_at','warmup',to_jsonb(w),
		'tokens',(SELECT jsonb_agg(to_jsonb(wt)) FROM warmup_tokens wt WHERE wt.task_id=t.id),
		'attempts',(SELECT jsonb_agg(to_jsonb(a)) FROM outbound_attempts a WHERE a.task_id=t.id),
		'charges',(SELECT jsonb_agg(to_jsonb(s)) FROM warmup_statistics s WHERE s.email_account_id=t.email_account_id))::text
		FROM tasks t JOIN warmup_tasks w ON w.task_id=t.id WHERE t.id=$1`, f.task).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertUnknownWarmupRetired(t *testing.T, f *warmupRecoveryFixture) {
	t.Helper()
	var retired, held, unknown bool
	if err := f.pool.QueryRow(t.Context(), `SELECT t.warmup_send_retired_at IS NOT NULL AND t.status='cancelled',
		t.send_result_state='unknown' AND t.send_result_applied_at IS NULL AND t.send_released_at IS NULL,ea.send_recovery_hold
		FROM tasks t JOIN email_accounts ea ON ea.id=t.email_account_id WHERE t.id=$1`, f.task).Scan(&retired, &unknown, &held); err != nil || !retired || !unknown || held {
		t.Fatalf("retired=%t unknown=%t held=%t err=%v", retired, unknown, held, err)
	}
	for _, inspect := range []func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*repository.WarmupDispatchState, error){f.r.InspectOutbound, f.r.InspectWarmupDispatch} {
		state, err := inspect(t.Context(), f.task, f.sender.ID, f.worker)
		if err != nil || state.State != "denied" {
			t.Fatalf("retired command became sendable: %+v err=%v", state, err)
		}
	}
}

func TestLiveWarmupUnknownRetirementPreservesEvidenceAndFencesReplayOnce(t *testing.T) {
	f := newUnknownWarmupFixture(t)
	before := unknownWarmupSnapshot(t, f)
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := f.r.RetireUnknownWarmupSends(t.Context(), f.before, 100)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			total += n
			mu.Unlock()
		}()
	}
	wg.Wait()
	if total != 1 {
		t.Fatalf("retired %d times", total)
	}
	assertUnknownWarmupRetired(t, f)
	if after := unknownWarmupSnapshot(t, f); before != after {
		t.Fatalf("retirement changed send evidence or accounting: before=%s after=%s", before, after)
	}
	for _, begin := range []func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (*repository.WarmupDispatchState, error){f.r.BeginOutbound, f.r.BeginWarmupDispatch} {
		state, err := begin(t.Context(), f.task, f.sender.ID, f.worker, f.nonce)
		if err != nil || state.State != "denied" {
			t.Fatalf("late command executed: %+v err=%v", state, err)
		}
	}
	if _, err := f.r.AuthorizeWarmupDispatch(t.Context(), f.task, f.sender.ID, f.worker); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("retired task reauthorized: %v", err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE tasks SET status='pending' WHERE id=$1`, f.task); err == nil {
		t.Fatal("retired task can be revived")
	}
}

func TestLiveWarmupUnknownRetirementLeavesOtherHoldsAndConfirmedEvidenceProtected(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"recent reservation", `UPDATE tasks SET send_reserved_at=NOW() WHERE id=$1`},
		{"recent execution", `UPDATE tasks SET send_executor_started_at=NOW() WHERE id=$1`},
		{"recent warmup execution", `UPDATE warmup_tasks SET dispatch_started_at=NOW() WHERE task_id=$1`},
		{"campaign", `UPDATE tasks SET task_type='campaign' WHERE id=$1`},
		{"manual send", `UPDATE tasks SET task_type='email' WHERE id=$1`},
		{"placement", `UPDATE tasks SET task_type='placement' WHERE id=$1`},
		{"authentication hold", `UPDATE email_accounts SET send_recovery_reason='authentication' WHERE id=(SELECT email_account_id FROM tasks WHERE id=$1)`},
		{"permanent hold", `UPDATE email_accounts SET send_recovery_reason='permanent' WHERE id=(SELECT email_account_id FROM tasks WHERE id=$1)`},
		{"conflict hold", `UPDATE email_accounts SET send_recovery_reason='conflict' WHERE id=(SELECT email_account_id FROM tasks WHERE id=$1)`},
		{"changed held task", `UPDATE email_accounts SET send_recovery_task_id=gen_random_uuid() WHERE id=(SELECT email_account_id FROM tasks WHERE id=$1)`},
		{"applied result", `UPDATE tasks SET send_result_applied_at=NOW() WHERE id=$1`},
		{"confirmed sent", `UPDATE tasks SET send_result_state='sent' WHERE id=$1`},
		{"released reservation", `UPDATE tasks SET send_released_at=NOW() WHERE id=$1`},
		{"provider message ID", `UPDATE warmup_tokens SET sent_message_id='<confirmed@provider.test>' WHERE task_id=$1`},
		{"confirmed receipt", `INSERT INTO warmup_received(email_account_id,internal_id,sender_account_id,message_id,task_id) SELECT recipient_account_id,gen_random_uuid(),sender_account_id,'<confirmed@provider.test>',task_id FROM warmup_tokens WHERE task_id=$1`},
		{"unfenced legacy", `WITH updated AS(UPDATE tasks SET send_executor_nonce=NULL WHERE id=$1) UPDATE warmup_tasks SET dispatch_nonce=NULL WHERE task_id=$1`},
		{"definitive executor success", `UPDATE tasks SET send_executor_result=jsonb_build_object('task_id',id,'success',true) WHERE id=$1`},
		{"definitive dispatch success", `UPDATE warmup_tasks SET dispatch_result=jsonb_build_object('task_id',task_id,'success',true) WHERE task_id=$1`},
		{"definitive executor refusal", `UPDATE tasks SET send_executor_result=jsonb_build_object('task_id',id,'error',jsonb_build_object('code','RECIPIENT_REJECTED')) WHERE id=$1`},
		{"invalid result lineage", `UPDATE tasks SET send_executor_result=jsonb_build_object('task_id',gen_random_uuid()) WHERE id=$1`},
		{"malformed stored result", `UPDATE tasks SET send_executor_result='[]' WHERE id=$1`},
		{"queued confirmation", `INSERT INTO send_recovery_resolutions(organization_id,email_account_id,recovery_task_id,previous_reason,evidence_type,confirmation_reference,confirmed_result,recorded_locally_at) SELECT ea.organization_id,ea.id,t.id,'unknown','operator_confirmed_sent','test evidence',jsonb_build_object('task_id',t.id,'success',true),NOW() FROM tasks t JOIN email_accounts ea ON ea.id=t.email_account_id WHERE t.id=$1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newUnknownWarmupFixture(t)
			f.exec(t, tc.query, f.task)
			before := unknownWarmupSnapshot(t, f)
			n, err := f.r.RetireUnknownWarmupSends(t.Context(), f.before, 100)
			if err != nil || n != 0 {
				t.Fatalf("unsafe retirement: n=%d err=%v", n, err)
			}
			if after := unknownWarmupSnapshot(t, f); after != before {
				t.Fatal("protected evidence changed")
			}
			var held bool
			if err := f.pool.QueryRow(t.Context(), `SELECT send_recovery_hold FROM email_accounts WHERE id=$1`, f.sender.ID).Scan(&held); err != nil || !held {
				t.Fatalf("protected mailbox released: held=%t err=%v", held, err)
			}
		})
	}
}

func TestLiveWarmupUnknownRetirementLateResultsCannotChangeNewAdmission(t *testing.T) {
	f := newUnknownWarmupFixture(t)
	if n, err := f.r.RetireUnknownWarmupSends(t.Context(), f.before, 100); err != nil || n != 1 {
		t.Fatalf("retired=%d err=%v", n, err)
	}
	next := uuid.New()
	f.exec(t, `INSERT INTO tasks(id,email_account_id,task_type,status,message_id,send_result_state) VALUES($1,$2,'warmup','completed','<new-distinct@sender.test>','unknown')`, next, f.sender.ID)
	f.exec(t, `UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='unknown',send_recovery_task_id=$2 WHERE id=$1`, f.sender.ID, next)
	for _, result := range []models.SendEmailResult{
		{TaskID: f.task, Success: true},
		{TaskID: f.task, Error: &models.EmailSendError{Code: "RECIPIENT_REJECTED"}},
		{TaskID: f.task},
	} {
		called := false
		err := f.r.(repository.SendResultRecovery).ApplySendResult(t.Context(), result, func(context.Context) error { called = true; return nil })
		if err != nil || called {
			t.Fatalf("late result applied accounting: called=%t err=%v", called, err)
		}
	}
	var heldTask uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT send_recovery_task_id FROM email_accounts WHERE id=$1 AND send_recovery_hold`, f.sender.ID).Scan(&heldTask); err != nil || heldTask != next {
		t.Fatalf("late results changed new hold: task=%s err=%v", heldTask, err)
	}
}

func TestLiveWarmupUnknownRetirementAllowsDistinctNewSendAndKeepsCooldown(t *testing.T) {
	f := newUnknownWarmupFixture(t)
	until := time.Now().Add(time.Hour)
	f.exec(t, `UPDATE email_accounts SET send_cooldown_until=$2,send_cooldown_provider='smtp_imap' WHERE id=$1`, f.sender.ID, until)
	if n, err := f.r.RetireUnknownWarmupSends(t.Context(), f.before, 100); err != nil || n != 1 {
		t.Fatalf("retired=%d err=%v", n, err)
	}
	r := f.r.(repository.SendResultRecovery)
	a, err := r.GetSendAdmission(t.Context(), f.org, f.sender.ID, "smtp_imap", time.Now())
	if err != nil || a.Allowed || a.RecoveryHold || a.RetryAt == nil || !a.RetryAt.Equal(until.Truncate(time.Microsecond)) {
		t.Fatalf("cooldown bypassed: admission=%+v err=%v", a, err)
	}
	f.exec(t, `UPDATE email_accounts SET send_cooldown_until=NULL WHERE id=$1`, f.sender.ID)
	next, token := uuid.New(), uuid.New()
	f.exec(t, `INSERT INTO tasks(id,email_account_id,task_type,status,message_id) VALUES($1,$2,'warmup','active','<fresh@sender.test>')`, next, f.sender.ID)
	f.exec(t, `INSERT INTO warmup_tasks(task_id,lineage_version,target_account_id) VALUES($1,1,$2)`, next, f.atWorkspace)
	f.exec(t, `INSERT INTO warmup_tokens(token,task_id,sender_account_id,recipient_account_id) VALUES($1,$2,$3,$4)`, token, next, f.sender.ID, f.atWorkspace)
	nonce, err := f.r.AuthorizeWarmupDispatch(t.Context(), next, f.sender.ID, f.worker)
	if err != nil || nonce == f.nonce || nonce == uuid.Nil {
		t.Fatalf("new task was not authorized independently: nonce=%s err=%v", nonce, err)
	}
	nonce, err = f.r.ReserveOutbound(t.Context(), repository.OutboundReservation{
		TaskID: next, MailboxID: f.sender.ID, OrganizationID: f.org, WorkerID: f.worker, Provider: "smtp_imap",
		Recipients: []string{"pick-" + f.atWorkspace.String()[:8] + "@acme.test"},
	})
	if err != nil {
		t.Fatalf("new distinct task could not reserve outbound admission: %v", err)
	}
	state, err := f.r.BeginOutbound(t.Context(), next, f.sender.ID, f.worker, nonce)
	if err != nil || state.State != "execute" {
		t.Fatalf("distinct new send blocked: state=%+v err=%v", state, err)
	}
	old, err := f.r.BeginWarmupDispatch(t.Context(), f.task, f.sender.ID, f.worker, f.nonce)
	if err != nil || old.State != "denied" {
		t.Fatalf("old task revived by new authorization: state=%+v err=%v", old, err)
	}
}

func TestLiveWarmupUnknownRetirementAndDefinitiveResultSerialize(t *testing.T) {
	for i := range 10 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			f := newUnknownWarmupFixture(t)
			f.exec(t, `UPDATE tasks SET send_executor_result=NULL WHERE id=$1`, f.task)
			f.exec(t, `UPDATE warmup_tasks SET dispatch_result=NULL WHERE task_id=$1`, f.task)
			var wg sync.WaitGroup
			var n int
			var called bool
			wg.Add(2)
			go func() {
				defer wg.Done()
				var err error
				n, err = f.r.RetireUnknownWarmupSends(t.Context(), f.before, 100)
				if err != nil {
					t.Error(err)
				}
			}()
			go func() {
				defer wg.Done()
				if err := f.r.(repository.SendResultRecovery).ApplySendResult(t.Context(), models.SendEmailResult{TaskID: f.task, Success: true}, func(context.Context) error { called = true; return nil }); err != nil {
					t.Error(err)
				}
			}()
			wg.Wait()
			if n == 1 && called || n == 0 && !called {
				t.Fatalf("retirement and reconciliation must not both win or lose: retired=%d applied=%t", n, called)
			}
		})
	}
}

func TestLiveWarmupUnknownRetirementMovesHoldWithoutResurrectingRetiredTasks(t *testing.T) {
	f := newUnknownWarmupFixture(t)
	other := uuid.New()
	f.exec(t, `INSERT INTO tasks(id,email_account_id,task_type,status,message_id,send_result_state) VALUES($1,$2,'email','completed','','unknown')`, other, f.sender.ID)
	if n, err := f.r.RetireUnknownWarmupSends(t.Context(), f.before, 100); err != nil || n != 1 {
		t.Fatalf("retired=%d err=%v", n, err)
	}
	var heldTask uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT send_recovery_task_id FROM email_accounts WHERE id=$1 AND send_recovery_hold`, f.sender.ID).Scan(&heldTask); err != nil || heldTask != other {
		t.Fatalf("other unknown lost: task=%s err=%v", heldTask, err)
	}
	if err := f.r.(repository.SendResultRecovery).ApplySendResult(t.Context(), models.SendEmailResult{TaskID: other, Success: true}, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	assertUnknownWarmupRetired(t, f)
	// Simulate an outgoing old replica reasserting an unknown hold during deployment.
	f.exec(t, `UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='unknown',send_recovery_task_id=$2 WHERE id=$1`, f.sender.ID, f.task)
	if n, err := f.r.RetireUnknownWarmupSends(t.Context(), f.before, 100); err != nil || n != 1 {
		t.Fatalf("stale hold repair=%d err=%v", n, err)
	}
	assertUnknownWarmupRetired(t, f)
}

func TestLiveWarmupUnknownRetirementLateReceiptDoesNotWaitOrTriggerActions(t *testing.T) {
	f := newUnknownWarmupFixture(t)
	if n, err := f.r.RetireUnknownWarmupSends(t.Context(), f.before, 100); err != nil || n != 1 {
		t.Fatalf("retired=%d err=%v", n, err)
	}
	var token uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT token FROM warmup_tokens WHERE task_id=$1`, f.task).Scan(&token); err != nil {
		t.Fatal(err)
	}
	called := false
	matched, err := f.svc.warmupRepo.(repository.WarmupReceiptProcessor).ProcessWarmupReceipt(t.Context(), token, f.atWorkspace,
		repository.WarmupReceiptProof{SenderAddress: f.sender.Email, Token: token, MessageID: "<uncertain@sender.test>"},
		func(*models.WarmupToken) error { called = true; return nil })
	if err != nil || !matched || called {
		t.Fatalf("retired receipt deferred or authorized actions: matched=%t called=%t err=%v", matched, called, err)
	}
}

func TestLiveWarmupUnknownRetirementSkipsProtectedRowsWithoutStarvingLaterMailboxes(t *testing.T) {
	protected := newUnknownWarmupFixture(t)
	protected.exec(t, `UPDATE tasks SET send_reserved_at=NOW()-INTERVAL '2 hours',send_executor_result='[]' WHERE id=$1`, protected.task)
	next := uuid.New()
	protected.exec(t, `INSERT INTO tasks(id,email_account_id,task_type,status,message_id,send_result_state,send_reserved_at,send_executor_nonce,send_executor_started_at)
		VALUES($1,$2,'warmup','completed','','unknown',NOW()-INTERVAL '1 hour',gen_random_uuid(),NOW()-INTERVAL '1 hour')`, next, protected.atSmallHost)
	protected.exec(t, `INSERT INTO warmup_tasks(task_id) VALUES($1)`, next)
	protected.exec(t, `UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='unknown',send_recovery_task_id=$2 WHERE id=$1`, protected.atSmallHost, next)
	t.Cleanup(func() { _, _ = protected.pool.Exec(context.Background(), `DELETE FROM tasks WHERE id=$1`, next) })
	if n, err := protected.r.RetireUnknownWarmupSends(t.Context(), protected.before, 1); err != nil || n != 1 {
		t.Fatalf("protected result starved eligible mailbox: retired=%d err=%v", n, err)
	}
	var retired bool
	if err := protected.pool.QueryRow(t.Context(), `SELECT t.warmup_send_retired_at IS NOT NULL AND t.status='cancelled' AND NOT ea.send_recovery_hold
		FROM tasks t JOIN email_accounts ea ON ea.id=t.email_account_id WHERE t.id=$1`, next).Scan(&retired); err != nil || !retired {
		t.Fatalf("later eligible mailbox remained stuck: retired=%t err=%v", retired, err)
	}
	var held bool
	if err := protected.pool.QueryRow(t.Context(), `SELECT send_recovery_hold FROM email_accounts WHERE id=$1`, protected.sender.ID).Scan(&held); err != nil || !held {
		t.Fatalf("protected mailbox unblocked: held=%t err=%v", held, err)
	}
}

func TestLiveWarmupUnknownRetirementReconcilerSeedsOnceAndClearsActiveWarnings(t *testing.T) {
	f := newUnknownWarmupFixture(t)
	f.svc.taskRepo = repository.NewTaskRepository(f.pool)
	f.svc.scheduler = scheduler.NewSchedulerService(f.svc.taskRepo, f.svc.warmupRepo, nil, f.svc.emailRepo, nil, nil, nil)
	f.svc.tasksClient = noopTaskScheduler{}
	counts := func() map[string]int64 {
		t.Helper()
		for _, source := range repository.NewMonitoringRepository(f.pool).Sources() {
			if source.ID != "sends" {
				continue
			}
			out, err := source.Collect(t.Context(), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			result := map[string]int64{}
			for _, metric := range out.Metrics {
				if metric.ID == "unknown_results" || metric.ID == "reservations_over_30m" {
					if metric.Count == nil {
						t.Fatalf("recovery warning %s unavailable", metric.ID)
					}
					result[metric.ID] = *metric.Count
				}
			}
			return result
		}
		t.Fatal("send monitoring source absent")
		return nil
	}
	before := counts()
	for range 2 {
		if _, err := f.svc.ReconcileWarmupSchedules(t.Context(), 500); err != nil {
			t.Fatal(err)
		}
	}
	assertUnknownWarmupRetired(t, f)
	var pending int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM tasks WHERE email_account_id=$1 AND task_type='warmup' AND status='pending' AND id<>$2`, f.sender.ID, f.task).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("normal scheduling failed to seed exactly one distinct wakeup: pending=%d err=%v", pending, err)
	}
	after := counts()
	if len(before) != 2 || len(after) != 2 {
		t.Fatalf("missing recovery warnings: before=%v after=%v", before, after)
	}
	for id, count := range before {
		if after[id] != count-1 {
			t.Fatalf("retired history remains an active warning: %s before=%d after=%d", id, count, after[id])
		}
	}
}
