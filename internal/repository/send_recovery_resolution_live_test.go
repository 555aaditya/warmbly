package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/models"
)

func TestLiveUnknownResolutionRejectsMissingEvidenceAndLiveExecution(t *testing.T) {
	f, r := lineageFixture(t)
	ctx := t.Context()
	repo := NewEmailRepostory(&db.DB{Pool: f.pool}, nil)
	if _, err := f.pool.Exec(ctx, `UPDATE tasks SET status='completed',completed_at=NOW()-INTERVAL '5 minutes',send_reserved_at=NOW()-INTERVAL '5 minutes',send_result_state='unknown' WHERE id=$1`, f.task); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='unknown',send_recovery_task_id=$2 WHERE id=$1`, f.sender, f.task); err != nil {
		t.Fatal(err)
	}
	valid := models.SendRecoveryResolution{HeldTaskID: f.task, HeldReason: "unknown", EvidenceType: "operator_confirmed_not_sent", ConfirmationReference: "Provider ticket fixture-907"}
	for _, mutate := range []func(*models.SendRecoveryResolution){
		func(v *models.SendRecoveryResolution) { v.ConfirmationReference = " " },
		func(v *models.SendRecoveryResolution) { v.ConfirmationReference = "unsafe\nreference" },
		func(v *models.SendRecoveryResolution) { v.HeldTaskID = uuid.New() },
		func(v *models.SendRecoveryResolution) { v.HeldReason = "permanent" },
		func(v *models.SendRecoveryResolution) { v.EvidenceType = "no_sent_copy" },
		func(v *models.SendRecoveryResolution) { v.EvidenceType = "operator_confirmed_sent" },
		func(v *models.SendRecoveryResolution) { v.MessageID = "unsent-cannot-have-message@example.test" },
		func(v *models.SendRecoveryResolution) { v.EvidenceTaskID = &f.task },
	} {
		v := valid
		mutate(&v)
		if _, xerr := repo.Update(ctx, f.org.String(), f.sender.String(), &models.UpdateEmail{SendRecoveryResolution: &v}); xerr == nil {
			t.Fatalf("unsafe evidence accepted: %+v", v)
		}
	}
	if _, err := f.pool.Exec(ctx, `UPDATE tasks SET send_executor_started_at=NOW() WHERE id=$1`, f.task); err != nil {
		t.Fatal(err)
	}
	if _, xerr := repo.Update(ctx, f.org.String(), f.sender.String(), &models.UpdateEmail{SendRecoveryResolution: &valid}); xerr == nil {
		t.Fatal("live provider execution resolved")
	}
	if _, err := f.pool.Exec(ctx, `UPDATE tasks SET send_executor_started_at=NOW()-INTERVAL '5 minutes' WHERE id=$1`, f.task); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(models.SendEmailResult{TaskID: f.task, Success: true, MessageID: "already-sent@example.test"})
	if _, err := f.pool.Exec(ctx, `UPDATE tasks SET send_executor_result=$2 WHERE id=$1`, f.task, raw); err != nil {
		t.Fatal(err)
	}
	if _, xerr := repo.Update(ctx, f.org.String(), f.sender.String(), &models.UpdateEmail{SendRecoveryResolution: &valid}); xerr == nil {
		t.Fatal("operator overrode durable provider outcome")
	}
	if _, err := f.pool.Exec(ctx, `UPDATE tasks SET send_executor_result=NULL WHERE id=$1`, f.task); err != nil {
		t.Fatal(err)
	}
	other := uuid.New()
	if _, err := f.pool.Exec(ctx, `INSERT INTO tasks(id,email_account_id,task_type,status,message_id,send_result_state)VALUES($1,$2,'email','completed','','unknown')`, other, f.sender); err != nil {
		t.Fatal(err)
	}
	if _, xerr := repo.Update(ctx, f.org.String(), f.sender.String(), &models.UpdateEmail{SendRecoveryResolution: &valid}); xerr != nil {
		t.Fatal(xerr)
	}
	results, err := r.ListUnappliedSendResults(ctx, 200)
	if err != nil || len(results) != 1 {
		t.Fatal("confirmed outcome unavailable", err, len(results))
	}
	if err := r.ApplySendResult(ctx, results[0], func(ctx context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var held bool
	var heldTask uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT send_recovery_hold,send_recovery_task_id FROM email_accounts WHERE id=$1`, f.sender).Scan(&held, &heldTask); err != nil {
		t.Fatal(err)
	}
	if !held || heldTask != other {
		t.Fatal("resolving one unknown freed another", held, heldTask)
	}
	native := models.SendEmailResult{TaskID: f.task, Success: true, MessageID: "late@example.test"}
	raw, err = json.Marshal(native)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE tasks SET send_executor_result=$2 WHERE id=$1`, f.task, raw); err != nil {
		t.Fatal(err)
	}
	results, err = r.ListUnappliedSendResults(ctx, 200)
	if err != nil || len(results) != 1 || results[0].TaskID != f.task {
		t.Fatal("late definitive outcome unavailable", err, len(results))
	}
	if err := r.ApplySendResult(ctx, results[0], func(context.Context) error { t.Fatal("late result changed committed accounting"); return nil }); err != nil {
		t.Fatal(err)
	}
	var reason string
	if err := f.pool.QueryRow(ctx, `SELECT send_recovery_reason,send_recovery_task_id FROM email_accounts WHERE id=$1`, f.sender).Scan(&reason, &heldTask); err != nil {
		t.Fatal(err)
	}
	if reason != "unknown" || heldTask != other {
		t.Fatal("late conflict displaced a different unknown hold", reason, heldTask)
	}
	results, err = r.ListUnappliedSendResults(ctx, 200)
	if err != nil || len(results) != 0 {
		t.Fatal("resolved conflict replayed on every sweep", err, len(results))
	}
	if _, err := f.pool.Exec(ctx, `UPDATE tasks SET status='completed',send_reserved_at=NOW()-INTERVAL '5 minutes',completed_at=NOW()-INTERVAL '4 minutes' WHERE id=$1`, other); err != nil {
		t.Fatal(err)
	}
	second := models.SendRecoveryResolution{HeldTaskID: other, HeldReason: "unknown", EvidenceType: "operator_confirmed_sent", MessageID: "second@example.test", ConfirmationReference: "Provider ticket fixture-908"}
	if _, xerr := repo.Update(ctx, f.org.String(), f.sender.String(), &models.UpdateEmail{SendRecoveryResolution: &second}); xerr != nil {
		t.Fatal(xerr)
	}
	results, err = r.ListUnappliedSendResults(ctx, 200)
	if err != nil || len(results) != 1 || results[0].TaskID != other {
		t.Fatal("second confirmation unavailable", err, len(results))
	}
	if err := r.ApplySendResult(ctx, results[0], func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT send_recovery_reason,send_recovery_task_id FROM email_accounts WHERE id=$1`, f.sender).Scan(&reason, &heldTask); err != nil {
		t.Fatal(err)
	}
	if reason != "conflict" || heldTask != f.task {
		t.Fatal("late conflict disappeared after other unknown was resolved", reason, heldTask)
	}
}

func TestLiveOrdinarySendResultsNeedNoRecoveryConfirmation(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(fmt.Sprintf("success_%t", success), func(t *testing.T) {
			f, r := lineageFixture(t)
			ctx := t.Context()
			if _, err := f.pool.Exec(ctx, `UPDATE email_accounts SET status='active',send_recovery_hold=false,send_recovery_task_id=NULL,send_recovery_reason=NULL,send_cooldown_until=NULL,provider='smtp_imap' WHERE id=$1`, f.sender); err != nil {
				t.Fatal(err)
			}
			result := models.SendEmailResult{TaskID: f.task, Success: success}
			want := "sent"
			if !success {
				want = "failed"
				result.Error = &models.EmailSendError{Failure: &errx.SendFailure{Disposition: errx.SendPermanent, Scope: "recipient"}}
			}
			calls := 0
			for range 2 {
				if err := r.ApplySendResult(ctx, result, func(context.Context) error { calls++; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			var state string
			var applied bool
			if err := f.pool.QueryRow(ctx, `SELECT send_result_state,send_result_applied_at IS NOT NULL FROM tasks WHERE id=$1`, f.task).Scan(&state, &applied); err != nil || state != want || !applied || calls != 1 {
				t.Fatalf("ordinary result did not apply once: state=%s applied=%v callbacks=%d error=%v", state, applied, calls, err)
			}
			admission, err := r.GetSendAdmission(ctx, f.org, f.sender, models.InboxProviderSMTPIMAP, time.Now())
			if err != nil || !admission.Allowed || admission.RecoveryHold {
				t.Fatalf("ordinary result required admin recovery: admission=%+v error=%v", admission, err)
			}
			var audits int
			if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM send_recovery_resolutions WHERE email_account_id=$1`, f.sender).Scan(&audits); err != nil || audits != 0 {
				t.Fatalf("ordinary result fabricated recovery evidence: rows=%d error=%v", audits, err)
			}
		})
	}
}

func TestLiveDefinitiveConflictDirectionsDuplicatesAndRestrictiveHolds(t *testing.T) {
	for _, sentFirst := range []bool{false, true} {
		for _, hold := range []string{"none", "unheld_pointer", "unheld_reason", "unheld_pointer_and_reason", "unknown", "authentication", "permanent", "conflict", "unclassified"} {
			t.Run(fmt.Sprintf("sent_first_%t/%s", sentFirst, hold), func(t *testing.T) {
				f, r := lineageFixture(t)
				ctx := t.Context()
				exec := func(q string, args ...any) {
					t.Helper()
					if _, err := f.pool.Exec(ctx, q, args...); err != nil {
						t.Fatal(err)
					}
				}
				exec(`UPDATE tasks SET status='completed',send_result_state='unknown' WHERE id=$1`, f.task)
				failed := models.SendEmailResult{TaskID: f.task, LegacyErrorMsg: "raw-sentinel", Error: &models.EmailSendError{
					Message: "raw-sentinel", Failure: &errx.SendFailure{Disposition: errx.SendPermanent, Scope: "recipient"},
				}}
				sent := models.SendEmailResult{TaskID: f.task, Success: true, MessageID: "raw-sentinel@example.test", ProviderMsgID: "raw-sentinel"}
				first, conflicting := failed, sent
				prior, outcome := "failed", "sent"
				if sentFirst {
					first, conflicting, prior, outcome = sent, failed, "sent", "failed"
				}
				var callbacks atomic.Int64
				apply := func(context.Context) error { callbacks.Add(1); return nil }
				if err := r.ApplySendResult(ctx, first, apply); err != nil {
					t.Fatal(err)
				}
				var appliedAt time.Time
				if err := f.pool.QueryRow(ctx, `SELECT send_result_applied_at FROM tasks WHERE id=$1`, f.task).Scan(&appliedAt); err != nil {
					t.Fatal(err)
				}
				other := uuid.New()
				exec(`INSERT INTO tasks(id,task_type,email_account_id,status,message_id,send_result_state,send_result_applied_at)VALUES($1,'email',$2,'completed','','sent',NOW())`, other, f.sender)
				switch hold {
				case "none":
					exec(`UPDATE email_accounts SET send_recovery_hold=false,send_recovery_reason=NULL,send_recovery_task_id=NULL WHERE id=$1`, f.sender)
				case "unheld_pointer":
					exec(`UPDATE email_accounts SET send_recovery_hold=false,send_recovery_reason=NULL,send_recovery_task_id=$2 WHERE id=$1`, f.sender, other)
				case "unheld_reason":
					exec(`UPDATE email_accounts SET send_recovery_hold=false,send_recovery_reason='permanent',send_recovery_task_id=NULL WHERE id=$1`, f.sender)
				case "unheld_pointer_and_reason":
					exec(`UPDATE email_accounts SET send_recovery_hold=false,send_recovery_reason='permanent',send_recovery_task_id=$2 WHERE id=$1`, f.sender, other)
				case "unclassified":
					exec(`UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason=NULL,send_recovery_task_id=NULL WHERE id=$1`, f.sender)
				case "unknown":
					exec(`UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='unknown',send_recovery_task_id=$2 WHERE id=$1`, f.sender, f.task)
				default:
					exec(`UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason=$2,send_recovery_task_id=$3 WHERE id=$1`, f.sender, hold, other)
				}
				duplicate := func() {
					t.Helper()
					var wg sync.WaitGroup
					results := make(chan error, 8)
					start := make(chan struct{})
					for range 8 {
						wg.Add(1)
						go func() {
							defer wg.Done()
							<-start
							results <- r.ApplySendResult(ctx, conflicting, apply)
						}()
					}
					close(start)
					wg.Wait()
					close(results)
					for err := range results {
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				duplicate()
				var count int
				var clean bool
				if err := f.pool.QueryRow(ctx, `SELECT COUNT(*),BOOL_AND(conflicting_result_state=$2 AND confirmed_result IS NULL
					AND evidence_type='authoritative_result_conflict' AND previous_reason='conflict' AND recorded_locally_at IS NOT NULL
					AND confirmation_reference='' AND evidence_task_id IS NULL AND to_jsonb(r)::text NOT LIKE '%raw-sentinel%')
					FROM send_recovery_resolutions r WHERE recovery_task_id=$1`, f.task, outcome).Scan(&count, &clean); err != nil || count != 1 || !clean {
					t.Fatalf("automatic evidence duplicated, leaked payload or gained operator authority: count=%d clean=%v error=%v", count, clean, err)
				}
				var held bool
				var reason string
				var task uuid.UUID
				readHold := func() {
					t.Helper()
					if err := f.pool.QueryRow(ctx, `SELECT send_recovery_hold,COALESCE(send_recovery_reason,''),COALESCE(send_recovery_task_id,'00000000-0000-0000-0000-000000000000'::uuid) FROM email_accounts WHERE id=$1`, f.sender).Scan(&held, &reason, &task); err != nil {
						t.Fatal(err)
					}
				}
				readHold()
				wantReason, wantTask := hold, other
				unheld := hold == "none" || hold == "unheld_pointer" || hold == "unheld_reason" || hold == "unheld_pointer_and_reason"
				if unheld || hold == "unknown" {
					wantReason, wantTask = "conflict", f.task
				} else if hold == "unclassified" {
					wantReason, wantTask = "", uuid.Nil
				}
				if !held || reason != wantReason || task != wantTask {
					t.Fatalf("restrictive hold lost or replaced: held=%v reason=%s task=%s", held, reason, task)
				}
				if unheld || hold == "unknown" {
					if hold == "none" {
						exec(`UPDATE send_recovery_resolutions SET conflict_detected_at=clock_timestamp()+INTERVAL '10 years' WHERE recovery_task_id=$1 AND evidence_type='authoritative_result_conflict'`, f.task)
					}
					repo := NewEmailRepostory(&db.DB{Pool: f.pool}, nil)
					resolution := &models.UpdateEmail{SendRecoveryResolution: &models.SendRecoveryResolution{
						HeldTaskID: f.task, HeldReason: "conflict", EvidenceType: "operator_provider_confirmation", EvidenceTaskID: &f.task, ConfirmationReference: "Fresh local provider confirmation",
					}}
					if _, xerr := repo.Update(ctx, f.org.String(), f.sender.String(), resolution); xerr != nil {
						t.Fatal(xerr)
					}
					duplicate()
					readHold()
					if held {
						t.Fatal("duplicate contradiction undid fresh local operator confirmation")
					}
				}
				var state string
				var lastApplied time.Time
				if err := f.pool.QueryRow(ctx, `SELECT send_result_state,send_result_applied_at FROM tasks WHERE id=$1`, f.task).Scan(&state, &lastApplied); err != nil || state != prior || !lastApplied.Equal(appliedAt) || callbacks.Load() != 1 {
					t.Fatalf("duplicate/conflict changed original accounting: state=%s callbacks=%d error=%v", state, callbacks.Load(), err)
				}
			})
		}
	}
}

func TestLiveSendRecoveryResolutionRequiresEvidenceAndRetainsUnknown(t *testing.T) {
	f, _ := lineageFixture(t)
	ctx := t.Context()
	repo := NewEmailRepostory(&db.DB{Pool: f.pool}, nil)
	held := f.task
	if _, err := f.pool.Exec(ctx, `UPDATE tasks SET status='failed',send_result_state='failed',send_result_applied_at=NOW(),completed_at=NOW()-INTERVAL '2 minutes' WHERE id=$1`, held); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='authentication',send_recovery_task_id=$2,last_synced_at=NOW()-INTERVAL '3 minutes' WHERE id=$1`, f.sender, held); err != nil {
		t.Fatal(err)
	}
	resolve := &models.UpdateEmail{SendRecoveryResolution: &models.SendRecoveryResolution{HeldTaskID: held, HeldReason: "authentication", EvidenceType: "authentication_repaired"}}
	if _, xerr := repo.Update(ctx, f.org.String(), f.sender.String(), resolve); xerr == nil {
		t.Fatal("stale sync cleared authentication hold")
	}
	unknown := uuid.New()
	if _, err := f.pool.Exec(ctx, `INSERT INTO tasks(id,task_type,email_account_id,status,message_id,send_result_state)VALUES($1,'email',$2,'active','','unknown')`, unknown, f.sender); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE email_accounts SET last_synced_at=NOW() WHERE id=$1`, f.sender); err != nil {
		t.Fatal(err)
	}
	if _, xerr := repo.Update(ctx, f.org.String(), f.sender.String(), resolve); xerr == nil {
		t.Fatal("unknown send was discarded by authentication repair")
	}
	if _, err := f.pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, unknown); err != nil {
		t.Fatal(err)
	}
	if _, xerr := repo.Update(ctx, f.org.String(), f.sender.String(), resolve); xerr == nil {
		t.Fatal("fresh inbox sync alone cleared outbound authentication hold")
	}
	evidence := uuid.New()
	if _, err := f.pool.Exec(ctx, `INSERT INTO tasks(id,task_type,email_account_id,status,message_id,send_result_state,send_result_applied_at,send_executor_started_at)VALUES($1,'email',$2,'completed','evidence@example.test','sent',NOW(),NOW())`, evidence, f.sender); err != nil {
		t.Fatal(err)
	}
	resolve.SendRecoveryResolution.EvidenceTaskID = &evidence
	if _, xerr := repo.Update(ctx, f.org.String(), f.sender.String(), resolve); xerr != nil {
		t.Fatal(xerr)
	}
	var heldNow bool
	var history int
	if err := f.pool.QueryRow(ctx, `SELECT send_recovery_hold FROM email_accounts WHERE id=$1`, f.sender).Scan(&heldNow); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM send_recovery_resolutions WHERE email_account_id=$1 AND recovery_task_id=$2 AND evidence_type='authentication_repaired'`, f.sender, held).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if heldNow || history != 1 {
		t.Fatal("repair was not atomically audited", heldNow, history)
	}

	if _, err := f.pool.Exec(ctx, `UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='conflict',send_recovery_task_id=$2 WHERE id=$1`, f.sender, held); err != nil {
		t.Fatal(err)
	}
	reference := "provider trace reviewed at " + time.Now().UTC().Format(time.RFC3339)
	confirmation := &models.UpdateEmail{SendRecoveryResolution: &models.SendRecoveryResolution{HeldTaskID: held, HeldReason: "conflict", EvidenceType: "operator_provider_confirmation", EvidenceTaskID: &held, ConfirmationReference: reference}}
	if _, xerr := repo.Update(ctx, uuid.New().String(), f.sender.String(), confirmation); xerr == nil {
		t.Fatal("cross-organization resolution accepted")
	}
	if _, xerr := repo.Update(ctx, f.org.String(), f.sender.String(), confirmation); xerr != nil {
		t.Fatal(xerr)
	}
	if err := f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM send_recovery_resolutions WHERE email_account_id=$1 AND previous_reason='conflict' AND confirmation_reference=$2`, f.sender, reference).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if history != 1 {
		t.Fatal("operator evidence reference was not retained", history)
	}
}

func TestLiveUnstartedDispatchRetainsDeferredOutcomeConflict(t *testing.T) {
	for _, action := range []string{"cancel outbound", "defer warmup"} {
		for _, anotherUnknown := range []bool{false, true} {
			t.Run(action+map[bool]string{false: "/conflict", true: "/another unknown then conflict"}[anotherUnknown], func(t *testing.T) {
				f, r := lineageFixture(t)
				ctx := t.Context()
				exec := func(query string, args ...any) {
					t.Helper()
					if _, err := f.pool.Exec(ctx, query, args...); err != nil {
						t.Fatal(err)
					}
				}
				exec(`UPDATE tasks SET status='completed',completed_at=NOW()-INTERVAL '5 minutes',send_result_state='unknown' WHERE id=$1`, f.task)
				exec(`UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='unknown',send_recovery_task_id=$2 WHERE id=$1`, f.sender, f.task)
				resolution := models.SendRecoveryResolution{HeldTaskID: f.task, HeldReason: "unknown", EvidenceType: "operator_confirmed_not_sent", ConfirmationReference: "Provider confirmation fixture"}
				repo := NewEmailRepostory(&db.DB{Pool: f.pool}, nil)
				if _, xerr := repo.Update(ctx, f.org.String(), f.sender.String(), &models.UpdateEmail{SendRecoveryResolution: &resolution}); xerr != nil {
					t.Fatal(xerr)
				}
				results, err := r.ListUnappliedSendResults(ctx, 200)
				if err != nil || len(results) != 1 || results[0].TaskID != f.task {
					t.Fatal("confirmed outcome unavailable", err, len(results))
				}
				if err := r.ApplySendResult(ctx, results[0], func(context.Context) error { return nil }); err != nil {
					t.Fatal(err)
				}
				task, worker, nonce := uuid.New(), uuid.New(), uuid.New()
				exec(`INSERT INTO fleet_nodes(id,role,active,last_seen_at,warmup_send_protocol)VALUES($1,'worker',true,NOW(),2)`, worker)
				t.Cleanup(func() { _, _ = f.pool.Exec(context.Background(), `DELETE FROM fleet_nodes WHERE id=$1`, worker) })
				lane := "email"
				var executorNonce any = nonce
				if action == "defer warmup" {
					lane = "warmup"
					executorNonce = nil
				}
				exec(`INSERT INTO tasks(id,email_account_id,task_type,status,message_id,send_result_state,send_executor_worker,send_executor_nonce)
				VALUES($1,$2,$3,'completed','','unknown',$4,$5)`, task, f.sender, lane, worker, executorNonce)
				if action == "defer warmup" {
					exec(`INSERT INTO warmup_tasks(task_id,dispatch_worker_id,dispatch_nonce)VALUES($1,$2,$3)`, task, worker, nonce)
				}
				exec(`UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='unknown',send_recovery_task_id=$2 WHERE id=$1`, f.sender, task)
				if err := r.ApplySendResult(ctx, models.SendEmailResult{TaskID: f.task, Success: true, MessageID: "late-native@example.test"}, func(context.Context) error {
					t.Fatal("contradictory result changed committed accounting")
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				var recorded bool
				if err := f.pool.QueryRow(ctx, `SELECT conflict_detected_at IS NOT NULL FROM send_recovery_resolutions WHERE recovery_task_id=$1 AND previous_reason='unknown'`, f.task).Scan(&recorded); err != nil || !recorded {
					t.Fatal("native contradiction not retained", recorded, err)
				}
				other := uuid.New()
				if anotherUnknown {
					exec(`INSERT INTO tasks(id,email_account_id,task_type,status,message_id,send_result_state)VALUES($1,$2,'email','completed','','unknown')`, other, f.sender)
				}
				if action == "defer warmup" {
					err = r.DeferWarmupDispatch(ctx, task, f.sender, worker, nonce, time.Now().Add(time.Hour))
				} else {
					err = r.CancelOutbound(ctx, task, f.sender, worker, nonce)
				}
				if err != nil {
					t.Fatal(err)
				}
				var held bool
				var reason *string
				var heldTask *uuid.UUID
				if err := f.pool.QueryRow(ctx, `SELECT send_recovery_hold,send_recovery_reason,send_recovery_task_id FROM email_accounts WHERE id=$1`, f.sender).Scan(&held, &reason, &heldTask); err != nil {
					t.Fatal(err)
				}
				if anotherUnknown {
					if !held || reason == nil || *reason != "unknown" || heldTask == nil || *heldTask != other {
						t.Fatal("unstarted dispatch did not transfer the remaining unknown hold", held, reason, heldTask)
					}
					if err := r.ApplySendResult(ctx, models.SendEmailResult{TaskID: other, Success: true}, func(context.Context) error { return nil }); err != nil {
						t.Fatal(err)
					}
					if err := f.pool.QueryRow(ctx, `SELECT send_recovery_hold,send_recovery_reason,send_recovery_task_id FROM email_accounts WHERE id=$1`, f.sender).Scan(&held, &reason, &heldTask); err != nil {
						t.Fatal(err)
					}
				}
				if !held || reason == nil || *reason != "conflict" || heldTask == nil || *heldTask != f.task {
					t.Fatalf("unstarted dispatch erased authoritative conflict: held=%v reason=%v task=%v", held, reason, heldTask)
				}
				admission, err := r.GetSendAdmission(ctx, f.org, f.sender, models.InboxProviderSMTPIMAP, time.Now())
				if err != nil || admission.Allowed || admission.HoldReason != "conflict" {
					t.Fatal("conflicting outcomes allowed another provider send", admission, err)
				}
				if action == "defer warmup" {
					state, err := r.BeginWarmupDispatch(ctx, task, f.sender, worker, nonce)
					if err != nil || state.State == "execute" {
						t.Fatal("deferred nonce still executable", state, err)
					}
				} else {
					state, err := r.InspectOutbound(ctx, task, f.sender, worker)
					if err != nil || state.State != "denied" {
						t.Fatal("cancelled nonce still executable", state, err)
					}
				}
			})
		}
	}
}

func TestLivePriorDefinitiveConflictWithoutOperatorResolution(t *testing.T) {
	f, r := lineageFixture(t)
	ctx := t.Context()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE tasks SET status='completed',send_result_state='unknown' WHERE id=$1`, f.task)
	exec(`UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='unknown',send_recovery_task_id=$2 WHERE id=$1`, f.sender, f.task)
	failure := models.SendEmailResult{TaskID: f.task, Error: &models.EmailSendError{Failure: &errx.SendFailure{Disposition: errx.SendPermanent, Scope: "recipient"}}}
	if err := r.ApplySendResult(ctx, failure, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	task, worker, nonce := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO fleet_nodes(id,role,active,last_seen_at,warmup_send_protocol)VALUES($1,'worker',true,NOW(),2)`, worker)
	t.Cleanup(func() { _, _ = f.pool.Exec(context.Background(), `DELETE FROM fleet_nodes WHERE id=$1`, worker) })
	exec(`INSERT INTO tasks(id,email_account_id,task_type,status,message_id,send_result_state,send_executor_worker,send_executor_nonce)
		VALUES($1,$2,'email','completed','','unknown',$3,$4)`, task, f.sender, worker, nonce)
	exec(`UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='unknown',send_recovery_task_id=$2 WHERE id=$1`, f.sender, task)
	if err := r.ApplySendResult(ctx, models.SendEmailResult{TaskID: f.task, Success: true, MessageID: "late-native@example.test"}, func(context.Context) error {
		t.Fatal("contradictory result changed committed accounting")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var recorded, initial int
	var evidenceType, outcome string
	if err := f.pool.QueryRow(ctx, `SELECT COUNT(*),COUNT(*) FILTER(WHERE confirmed_result IS NOT NULL),
		MAX(evidence_type),MAX(conflicting_result_state)
		FROM send_recovery_resolutions WHERE recovery_task_id=$1 AND evidence_type='authoritative_result_conflict' AND conflict_detected_at IS NOT NULL`, f.task).
		Scan(&recorded, &initial, &evidenceType, &outcome); err != nil {
		t.Fatal(err)
	}
	if recorded != 1 || initial != 0 || evidenceType != "authoritative_result_conflict" || outcome != "sent" {
		t.Fatal("contradiction provenance not durably retained", recorded, initial, evidenceType, outcome)
	}
	if err := r.CancelOutbound(ctx, task, f.sender, worker, nonce); err != nil {
		t.Fatal(err)
	}
	var held bool
	var reason string
	var heldTask uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT send_recovery_hold,send_recovery_reason,send_recovery_task_id FROM email_accounts WHERE id=$1`, f.sender).
		Scan(&held, &reason, &heldTask); err != nil || !held || reason != "conflict" || heldTask != f.task {
		t.Fatal("ordinary authoritative contradiction was lost", held, reason, heldTask, err)
	}
	confirmation := &models.UpdateEmail{SendRecoveryResolution: &models.SendRecoveryResolution{
		HeldTaskID: f.task, HeldReason: "conflict", EvidenceType: "operator_provider_confirmation", EvidenceTaskID: &f.task,
		ConfirmationReference: "Provider evidence for the previously failed native result",
	}}
	repo := NewEmailRepostory(&db.DB{Pool: f.pool}, nil)
	if _, xerr := repo.Update(ctx, f.org.String(), f.sender.String(), confirmation); xerr != nil {
		t.Fatal("conflict cannot be confirmed by operator", xerr)
	}
}

func TestLiveAuthenticationRepairRetainsUnresolvedOutcomeConflict(t *testing.T) {
	f, r := lineageFixture(t)
	ctx := t.Context()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE tasks SET status='completed',completed_at=NOW()-INTERVAL '5 minutes',send_result_state='unknown' WHERE id=$1`, f.task)
	exec(`UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='unknown',send_recovery_task_id=$2 WHERE id=$1`, f.sender, f.task)
	repo := NewEmailRepostory(&db.DB{Pool: f.pool}, nil)
	resolution := models.SendRecoveryResolution{HeldTaskID: f.task, HeldReason: "unknown", EvidenceType: "operator_confirmed_not_sent", ConfirmationReference: "Provider ticket fixture"}
	if _, xerr := repo.Update(ctx, f.org.String(), f.sender.String(), &models.UpdateEmail{SendRecoveryResolution: &resolution}); xerr != nil {
		t.Fatal(xerr)
	}
	results, err := r.ListUnappliedSendResults(ctx, 200)
	if err != nil || len(results) != 1 || results[0].TaskID != f.task {
		t.Fatal("operator confirmation not available", err, len(results))
	}
	if err := r.ApplySendResult(ctx, results[0], func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := r.ApplySendResult(ctx, models.SendEmailResult{TaskID: f.task, Success: true, MessageID: "late-native@example.test"}, func(context.Context) error {
		t.Fatal("contradiction re-applied accounting")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	evidenceTask := uuid.New()
	exec(`INSERT INTO tasks(id,task_type,email_account_id,status,message_id,send_result_state,send_result_applied_at,send_executor_started_at)
		VALUES($1,'email',$2,'completed','later@example.test','sent',NOW(),NOW())`, evidenceTask, f.sender)
	exec(`UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='authentication',send_recovery_task_id=$2,last_synced_at=NOW() WHERE id=$1`, f.sender, f.task)
	repair := &models.UpdateEmail{SendRecoveryResolution: &models.SendRecoveryResolution{
		HeldTaskID: f.task, HeldReason: "authentication", EvidenceType: "authentication_repaired", EvidenceTaskID: &evidenceTask,
	}}
	if _, xerr := repo.Update(ctx, f.org.String(), f.sender.String(), repair); xerr != nil {
		t.Fatal(xerr)
	}
	var held bool
	var reason string
	var task uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT send_recovery_hold,COALESCE(send_recovery_reason,''),COALESCE(send_recovery_task_id,'00000000-0000-0000-0000-000000000000'::uuid) FROM email_accounts WHERE id=$1`, f.sender).
		Scan(&held, &reason, &task); err != nil || !held || reason != "conflict" || task != f.task {
		t.Fatal("authentication repair erased contradictory outcome hold", held, reason, task, err)
	}
}

func TestLiveConcurrentAuthenticationRepairRetainsNewOutcomeConflict(t *testing.T) {
	f, r := lineageFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE tasks SET status='completed',completed_at=NOW()-INTERVAL '5 minutes',send_result_state='unknown' WHERE id=$1`, f.task)
	exec(`UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='unknown',send_recovery_task_id=$2 WHERE id=$1`, f.sender, f.task)
	repo := NewEmailRepostory(&db.DB{Pool: f.pool}, nil)
	confirmation := &models.UpdateEmail{SendRecoveryResolution: &models.SendRecoveryResolution{
		HeldTaskID: f.task, HeldReason: "unknown", EvidenceType: "operator_confirmed_not_sent", ConfirmationReference: "Provider ticket fixture",
	}}
	if _, xerr := repo.Update(ctx, f.org.String(), f.sender.String(), confirmation); xerr != nil {
		t.Fatal(xerr)
	}
	failure := models.SendEmailResult{TaskID: f.task, Error: &models.EmailSendError{Failure: &errx.SendFailure{Disposition: errx.SendPermanent, Scope: "recipient"}}}
	if err := r.ApplySendResult(ctx, failure, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	evidenceTask := uuid.New()
	exec(`INSERT INTO tasks(id,task_type,email_account_id,status,message_id,send_result_state,send_result_applied_at,send_executor_started_at)
		VALUES($1,'email',$2,'completed','','sent',NOW(),NOW())`, evidenceTask, f.sender)
	authTask := uuid.New()
	exec(`INSERT INTO tasks(id,task_type,email_account_id,status,message_id,completed_at,send_result_state,send_result_applied_at)
		VALUES($1,'email',$2,'completed','',NOW()-INTERVAL '5 minutes','failed',NOW()-INTERVAL '5 minutes')`, authTask, f.sender)
	exec(`UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='authentication',send_recovery_task_id=$2 WHERE id=$1`, f.sender, authTask)
	key := int32(f.task.ID() & 0x7fffffff)
	name := fmt.Sprintf("hold_repair_%x", f.task.ID())
	exec(fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
		IF NEW.id='%s'::uuid AND NOT OLD.send_recovery_hold AND NOT NEW.send_recovery_hold
		AND OLD.send_recovery_reason IS NULL AND NEW.send_recovery_reason IS NULL THEN
		PERFORM pg_advisory_xact_lock(938,%d); END IF; RETURN NEW; END $$`, name, f.sender, key))
	exec(fmt.Sprintf(`CREATE TRIGGER %s BEFORE UPDATE ON email_accounts FOR EACH ROW EXECUTE FUNCTION %s()`, name, name))
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON email_accounts; DROP FUNCTION IF EXISTS %s()`, name, name))
	})
	barrier, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer barrier.Rollback(context.Background())
	if _, err := barrier.Exec(ctx, `SELECT pg_advisory_xact_lock(938,$1)`, key); err != nil {
		t.Fatal(err)
	}
	repair := &models.UpdateEmail{SendRecoveryResolution: &models.SendRecoveryResolution{
		HeldTaskID: authTask, HeldReason: "authentication", EvidenceType: "authentication_repaired", EvidenceTaskID: &evidenceTask,
	}}
	repaired := make(chan error, 1)
	go func() {
		_, xerr := repo.Update(ctx, f.org.String(), f.sender.String(), repair)
		if xerr != nil {
			repaired <- xerr
		} else {
			repaired <- nil
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	waiting := false
	for time.Now().Before(deadline) {
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND classid=938 AND objid=$1::oid)`, key).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !waiting {
		t.Fatal("repair did not reach its hold-refresh snapshot barrier")
	}
	result := make(chan error, 1)
	go func() {
		result <- r.ApplySendResult(ctx, models.SendEmailResult{TaskID: f.task, Success: true}, func(context.Context) error {
			return fmt.Errorf("contradiction re-applied accounting")
		})
	}()
	committedBeforeRefresh := false
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
		committedBeforeRefresh = true
	case <-time.After(time.Second):
	}
	if err := barrier.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if !committedBeforeRefresh {
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	}
	if err := <-repaired; err != nil {
		t.Fatal(err)
	}
	var held bool
	var reason string
	var task uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT send_recovery_hold,COALESCE(send_recovery_reason,''),COALESCE(send_recovery_task_id,'00000000-0000-0000-0000-000000000000'::uuid) FROM email_accounts WHERE id=$1`, f.sender).Scan(&held, &reason, &task); err != nil || !held || reason != "conflict" || task != f.task {
		t.Fatalf("repair lost a concurrent native conflict: held=%v reason=%s task=%s result_committed_before_refresh=%v error=%v", held, reason, task, committedBeforeRefresh, err)
	}
}

func TestLiveHistoricalConfirmationRequiresFreshOrIndependentAuthority(t *testing.T) {
	for _, sent := range []bool{false, true} {
		for _, authority := range []string{"history only", "fresh confirmation", "executor result", "warmup result"} {
			t.Run(fmt.Sprintf("sent=%v/%s", sent, authority), func(t *testing.T) {
				f, r := lineageFixture(t)
				ctx := t.Context()
				exec := func(query string, args ...any) {
					t.Helper()
					if _, err := f.pool.Exec(ctx, query, args...); err != nil {
						t.Fatal(err)
					}
				}
				exec(`UPDATE tasks SET status='completed',completed_at=NOW()-INTERVAL '5 minutes',send_result_state='unknown' WHERE id=$1`, f.task)
				exec(`UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='unknown',send_recovery_task_id=$2 WHERE id=$1`, f.sender, f.task)
				failure := &models.EmailSendError{Failure: &errx.SendFailure{Disposition: errx.SendPermanent, Scope: "recipient"}}
				history := models.SendEmailResult{TaskID: f.task, Success: !sent, Error: failure}
				rawHistory, err := json.Marshal(history)
				if err != nil {
					t.Fatal(err)
				}
				exec(`INSERT INTO send_recovery_resolutions(organization_id,email_account_id,recovery_task_id,previous_reason,evidence_type,confirmation_reference,confirmed_result)
					VALUES($1,$2,$3,'unknown','operator_confirmed_sent','Historical fixture',$4)`, f.org, f.sender, f.task, rawHistory)
				results, err := r.ListUnappliedSendResults(ctx, 200)
				if err != nil || len(results) != 0 {
					t.Fatalf("history gained authority: results=%v error=%v", results, err)
				}
				if authority != "history only" {
					actual := models.SendEmailResult{TaskID: f.task, Success: sent, Error: failure, MessageID: "native@example.test"}
					raw, err := json.Marshal(actual)
					if err != nil {
						t.Fatal(err)
					}
					switch authority {
					case "fresh confirmation":
						resolution := models.SendRecoveryResolution{HeldTaskID: f.task, HeldReason: "unknown", EvidenceType: "operator_confirmed_not_sent", ConfirmationReference: "Fresh provider fixture"}
						if sent {
							resolution.EvidenceType, resolution.MessageID = "operator_confirmed_sent", "fresh@example.test"
						}
						repo := NewEmailRepostory(&db.DB{Pool: f.pool}, nil)
						if _, xerr := repo.Update(ctx, f.org.String(), f.sender.String(), &models.UpdateEmail{SendRecoveryResolution: &resolution}); xerr != nil {
							t.Fatal(xerr)
						}
					case "executor result":
						exec(`UPDATE tasks SET send_executor_result=$2 WHERE id=$1`, f.task, raw)
					case "warmup result":
						exec(`INSERT INTO warmup_tasks(task_id,dispatch_result)VALUES($1,$2)`, f.task, raw)
					}
					results, err = r.ListUnappliedSendResults(ctx, 200)
					if err != nil || len(results) != 1 || results[0].TaskID != f.task || results[0].Success != sent {
						t.Fatalf("fresh authority unavailable or replaced by history: results=%v error=%v", results, err)
					}
				}
				var held bool
				if err := f.pool.QueryRow(ctx, `SELECT send_recovery_hold FROM email_accounts WHERE id=$1`, f.sender).Scan(&held); err != nil || !held {
					t.Fatal("authority released hold before accounting", held, err)
				}
				applied := 0
				for _, result := range results {
					for range 2 {
						if err := r.ApplySendResult(ctx, result, func(context.Context) error { applied++; return nil }); err != nil {
							t.Fatal(err)
						}
					}
				}
				var state string
				var historyCount int
				if err := f.pool.QueryRow(ctx, `SELECT t.send_result_state,ea.send_recovery_hold,
					(SELECT COUNT(*) FROM send_recovery_resolutions r WHERE r.recovery_task_id=t.id AND r.recorded_locally_at IS NULL AND r.confirmed_result=$2::jsonb)
					FROM tasks t JOIN email_accounts ea ON ea.id=t.email_account_id WHERE t.id=$1`, f.task, rawHistory).Scan(&state, &held, &historyCount); err != nil {
					t.Fatal(err)
				}
				wantState, wantApplied := "unknown", 0
				if authority != "history only" {
					wantApplied, wantState = 1, "failed"
					if sent {
						wantState = "sent"
					}
				}
				if state != wantState || applied != wantApplied || held != (authority == "history only") || historyCount != 1 {
					t.Fatalf("state=%s applied=%d held=%v preserved history=%d", state, applied, held, historyCount)
				}
			})
		}
	}
}

func TestLiveExpandedConflictSchemaRequiresUpgradedReadersAndWriters(t *testing.T) {
	f, r := lineageFixture(t)
	ctx := t.Context()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	failure := models.SendEmailResult{TaskID: f.task, Error: &models.EmailSendError{Failure: &errx.SendFailure{Disposition: errx.SendPermanent, Scope: "recipient"}}}
	if err := r.ApplySendResult(ctx, failure, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := r.ApplySendResult(ctx, models.SendEmailResult{TaskID: f.task, Success: true}, func(context.Context) error { t.Fatal("contradiction re-applied accounting"); return nil }); err != nil {
		t.Fatal(err)
	}
	other := uuid.New()
	exec(`INSERT INTO tasks(id,email_account_id,task_type,status,message_id,send_result_state)VALUES($1,$2,'email','failed','','failed')`, other, f.sender)
	exec(`UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='unknown',send_recovery_task_id=$2 WHERE id=$1`, f.sender, other)
	old, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Rollback(ctx)
	// Pre-extension hold selection does not recognize typed automatic conflicts.
	if _, err := old.Exec(ctx, `WITH next_hold AS (
		SELECT id,reason FROM (
		 SELECT t.id,'unknown' AS reason,1 AS priority,t.created_at FROM tasks t
		 WHERE t.email_account_id=$1 AND t.send_result_state='unknown'
		 UNION ALL
		 SELECT t.id,'conflict',2,t.created_at FROM send_recovery_resolutions r JOIN tasks t ON t.id=r.recovery_task_id
		 WHERE t.email_account_id=$1 AND r.previous_reason='unknown' AND r.conflict_detected_at IS NOT NULL
		 AND NOT EXISTS(SELECT 1 FROM send_recovery_resolutions resolved WHERE resolved.recovery_task_id=t.id AND resolved.previous_reason='conflict')
		) candidates ORDER BY priority,created_at,id LIMIT 1)
		UPDATE email_accounts ea SET send_recovery_task_id=n.id,send_recovery_hold=n.id IS NOT NULL,send_recovery_reason=n.reason
		FROM (SELECT 1) singleton LEFT JOIN next_hold n ON true
		WHERE ea.id=$1 AND ea.send_recovery_task_id=$2 AND ea.send_recovery_reason='unknown'`, f.sender, other); err != nil {
		t.Fatal(err)
	}
	var held bool
	if err := old.QueryRow(ctx, `SELECT send_recovery_hold FROM email_accounts WHERE id=$1`, f.sender).Scan(&held); err != nil || held {
		t.Fatal("fixture no longer demonstrates old writer losing the conflict", held, err)
	}
	if err := old.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	updated, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer updated.Rollback(ctx)
	if err := refreshUnknownSendHold(ctx, updated, f.sender, other); err != nil {
		t.Fatal(err)
	}
	var reason string
	var heldTask uuid.UUID
	if err := updated.QueryRow(ctx, `SELECT send_recovery_hold,send_recovery_reason,send_recovery_task_id FROM email_accounts WHERE id=$1`, f.sender).Scan(&held, &reason, &heldTask); err != nil || !held || reason != "conflict" || heldTask != f.task {
		t.Fatal("updated writer lost conflict", held, reason, heldTask, err)
	}
	if err := updated.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(models.SendEmailResult{TaskID: other, Success: true})
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE tasks SET status='completed',send_result_state='unknown',send_result_applied_at=NULL WHERE id=$1`, other)
	exec(`INSERT INTO send_recovery_resolutions(organization_id,email_account_id,recovery_task_id,previous_reason,evidence_type,confirmed_result)
		VALUES($1,$2,$3,'unknown','operator_confirmed_sent',$4)`, f.org, f.sender, other, raw)
	var oldReadable bool
	if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM send_recovery_resolutions r JOIN tasks t ON t.id=r.recovery_task_id
		WHERE t.id=$1 AND r.confirmed_result IS NOT NULL AND t.send_result_applied_at IS NULL AND t.status<>'cancelled')`, other).Scan(&oldReadable); err != nil || !oldReadable {
		t.Fatal("fixture no longer demonstrates ungated historical authority", oldReadable, err)
	}
	results, err := r.ListUnappliedSendResults(ctx, 200)
	if err != nil || len(results) != 0 {
		t.Fatal("updated reader accepted historical authority", results, err)
	}
}
