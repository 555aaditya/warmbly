package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/models"
)

func stopFixture(t *testing.T, protocol int) (*poolLinkFixture, *taskRepository, uuid.UUID, uuid.UUID, EmailRepository) {
	t.Helper()
	f, r, worker, nonce := newWarmupChargeFixture(t, false)
	if _, err := f.pool.Exec(t.Context(), `UPDATE fleet_nodes SET warmup_send_protocol=$1 WHERE id=$2`, protocol, worker); err != nil {
		t.Fatal(err)
	}
	if protocol == 2 {
		var address string
		if err := f.pool.QueryRow(t.Context(), `SELECT email FROM email_accounts WHERE id=$1`, f.recipient).Scan(&address); err != nil {
			t.Fatal(err)
		}
		var err error
		nonce, err = r.ReserveOutbound(t.Context(), OutboundReservation{TaskID: f.task, MailboxID: f.sender, OrganizationID: f.org, WorkerID: worker, Provider: models.InboxProviderSMTPIMAP, Recipients: []string{address}})
		if err != nil {
			t.Fatal(err)
		}
	}
	return f, r, worker, nonce, NewEmailRepostory(&db.DB{Pool: f.pool}, nil)
}

func TestLiveWarmupStopRetiresBothDirectionsBeforeResume(t *testing.T) {
	for _, protocol := range []int{1, 2} {
		for _, direction := range []string{"sender", "recipient"} {
			t.Run(fmt.Sprintf("%s/protocol%d", direction, protocol), func(t *testing.T) {
				f, r, worker, nonce, emails := stopFixture(t, protocol)
				mailbox := f.sender
				if direction == "recipient" {
					mailbox = f.recipient
				}
				if _, xerr := emails.SetWarmupLifecycle(t.Context(), uuid.NewString(), mailbox.String(), "stop"); xerr == nil {
					t.Fatal("cross-workspace stop succeeded")
				}
				for range 2 {
					row, xerr := emails.SetWarmupLifecycle(t.Context(), f.org.String(), mailbox.String(), "stop")
					if xerr != nil || row.TestSendingAllowed() || row.TestReceivingAllowed() || row.Warmup == nil {
						t.Fatal("stop lost mailbox or participation state", row, xerr)
					}
				}
				var status string
				var held bool
				var sent, tokens int
				if err := f.pool.QueryRow(t.Context(), `SELECT t.status,ea.send_recovery_hold,(SELECT emails_sent FROM warmup_statistics WHERE email_account_id=ea.id),
					(SELECT COUNT(*) FROM warmup_tokens WHERE task_id=t.id) FROM tasks t JOIN email_accounts ea ON ea.id=t.email_account_id WHERE t.id=$1`, f.task).Scan(&status, &held, &sent, &tokens); err != nil || status != "cancelled" || held || sent != 0 || tokens != 0 {
					t.Fatal("unstarted reservation was not retired/refunded exactly once", status, held, sent, tokens, err)
				}
				if _, xerr := emails.SetWarmupLifecycle(t.Context(), f.org.String(), mailbox.String(), "resume"); xerr != nil {
					t.Fatal(xerr)
				}
				if protocol == 1 {
					state, err := r.BeginWarmupDispatch(t.Context(), f.task, f.sender, worker, nonce)
					if err != nil || state.State != "denied" {
						t.Fatal("resumed mailbox revived stale protocol-1 command", state, err)
					}
				} else {
					state, err := r.BeginOutbound(t.Context(), f.task, f.sender, worker, nonce)
					if err == nil && state.State == "execute" {
						t.Fatal("resumed mailbox revived stale protocol-2 command")
					}
				}
			})
		}
	}
}

func TestLiveWarmupStopPreservesStartedUnknownAndAcceptedResults(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "accepted"}[accepted], func(t *testing.T) {
			f, r, worker, nonce, emails := stopFixture(t, 2)
			state, err := r.BeginOutbound(t.Context(), f.task, f.sender, worker, nonce)
			if err != nil || state.State != "execute" {
				t.Fatal(state, err)
			}
			result := models.SendEmailResult{TaskID: f.task, Success: true, MessageID: "accepted@example.test", SentAt: time.Now().UTC()}
			if accepted {
				if err := r.FinishOutbound(t.Context(), f.task, f.sender, worker, result); err != nil {
					t.Fatal(err)
				}
			}
			if _, xerr := emails.SetWarmupLifecycle(t.Context(), f.org.String(), f.recipient.String(), "stop"); xerr != nil {
				t.Fatal(xerr)
			}
			var status string
			var held bool
			var attempts, sent int
			if err := f.pool.QueryRow(t.Context(), `SELECT t.status,ea.send_recovery_hold,
				(SELECT COUNT(*) FROM outbound_attempts WHERE task_id=t.id),(SELECT emails_sent FROM warmup_statistics WHERE email_account_id=ea.id)
				FROM tasks t JOIN email_accounts ea ON ea.id=t.email_account_id WHERE t.id=$1`, f.task).Scan(&status, &held, &attempts, &sent); err != nil || status != "completed" || !held || attempts != 1 || sent != 1 {
				t.Fatal("stop erased accepted/uncertain execution evidence", status, held, attempts, sent, err)
			}
			state, err = r.InspectOutbound(t.Context(), f.task, f.sender, worker)
			if err != nil || !accepted && state.State != "started" || accepted && (state.State != "finished" || state.Result.MessageID != result.MessageID) {
				t.Fatal("stop lost durable replay", state, err)
			}
			if accepted {
				if err := r.ApplySendResult(t.Context(), result, func(context.Context) error { return nil }); err != nil {
					t.Fatal("late result could not reconcile", err)
				}
			}
		})
	}
}

func TestLiveWarmupStopPreservesCompletedLegacyHistoryButDeniesExecution(t *testing.T) {
	f, r := lineageFixture(t)
	state, err := r.InspectWarmupDispatch(t.Context(), f.task, f.sender, uuid.New())
	if err != nil || state.State != "denied" {
		t.Fatal("missing warmup child row permitted legacy execution", state, err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO warmup_tasks(task_id,target_account_id)VALUES($1,$2)`, f.task, f.recipient); err != nil {
		t.Fatal(err)
	}
	emails := NewEmailRepostory(&db.DB{Pool: f.pool}, nil)
	if _, xerr := emails.SetWarmupLifecycle(t.Context(), f.org.String(), f.sender.String(), "stop"); xerr != nil {
		t.Fatal(xerr)
	}
	var status string
	if err := f.pool.QueryRow(t.Context(), `SELECT status FROM tasks WHERE id=$1`, f.task).Scan(&status); err != nil || status != "completed" {
		t.Fatal("legacy completion treated as proof of unsent mail", status, err)
	}
	state, err = r.InspectWarmupDispatch(t.Context(), f.task, f.sender, uuid.New())
	if err != nil || state.State != "denied" {
		t.Fatal("legacy queued command can bypass final approval", state, err)
	}
}

func TestLiveWarmupStopParticipationPatchPreservesOtherDirection(t *testing.T) {
	f, _, _, _, emails := stopFixture(t, 2)
	on, off := true, false
	mode := models.TestParticipationDiagnostic
	if _, xerr := emails.Update(t.Context(), f.org.String(), f.recipient.String(), &models.UpdateEmail{TestMode: &mode, TestSendEnabled: &off, TestReceiveEnabled: &on}); xerr != nil {
		t.Fatal(xerr)
	}
	var status string
	if err := f.pool.QueryRow(t.Context(), `SELECT status FROM tasks WHERE id=$1`, f.task).Scan(&status); err != nil || status != "completed" {
		t.Fatal("sender-only disable cancelled incoming work", status, err)
	}
	if _, xerr := emails.Update(t.Context(), f.org.String(), f.recipient.String(), &models.UpdateEmail{TestReceiveEnabled: &off}); xerr != nil {
		t.Fatal(xerr)
	}
	if err := f.pool.QueryRow(t.Context(), `SELECT status FROM tasks WHERE id=$1`, f.task).Scan(&status); err != nil || status != "cancelled" {
		t.Fatal("recipient disable did not retire incoming work", status, err)
	}
}

func TestLiveWarmupStopRetiresOpeningRecipientWithoutReplyTarget(t *testing.T) {
	f, _, _, _, emails := stopFixture(t, 2)
	if _, err := f.pool.Exec(t.Context(), `UPDATE warmup_tasks SET target_account_id=NULL WHERE task_id=$1`, f.task); err != nil {
		t.Fatal(err)
	}
	if _, xerr := emails.SetWarmupLifecycle(t.Context(), f.org.String(), f.recipient.String(), "stop"); xerr != nil {
		t.Fatal(xerr)
	}
	var status string
	if err := f.pool.QueryRow(t.Context(), `SELECT status FROM tasks WHERE id=$1`, f.task).Scan(&status); err != nil || status != "cancelled" {
		t.Fatal("opening recipient was not revoked through its token", status, err)
	}
}

func TestLiveWarmupStopRetiresPendingSenderWithoutChildRow(t *testing.T) {
	f, _ := lineageFixture(t)
	if _, err := f.pool.Exec(t.Context(), `UPDATE tasks SET status='pending' WHERE id=$1`, f.task); err != nil {
		t.Fatal(err)
	}
	emails := NewEmailRepostory(&db.DB{Pool: f.pool}, nil)
	if _, xerr := emails.SetWarmupLifecycle(t.Context(), f.org.String(), f.sender.String(), "stop"); xerr != nil {
		t.Fatal(xerr)
	}
	var status string
	if err := f.pool.QueryRow(t.Context(), `SELECT status FROM tasks WHERE id=$1`, f.task).Scan(&status); err != nil || status != "cancelled" {
		t.Fatal("pending warmup without its child row survived stop", status, err)
	}
}

func TestLiveWarmupStopUpgradeResumeRetiresPreviouslyOffCommands(t *testing.T) {
	for _, api := range []string{"lifecycle", "legacy_patch"} {
		t.Run(api, func(t *testing.T) {
			f, _, _, _, emails := stopFixture(t, 2)
			if _, err := f.pool.Exec(t.Context(), `UPDATE email_accounts SET test_mode='off',test_send_enabled=false,test_receive_enabled=false WHERE id=$1`, f.sender); err != nil {
				t.Fatal(err)
			}
			if api == "lifecycle" {
				if _, xerr := emails.SetWarmupLifecycle(t.Context(), f.org.String(), f.sender.String(), "resume"); xerr != nil {
					t.Fatal(xerr)
				}
			} else {
				on := true
				if _, xerr := emails.Update(t.Context(), f.org.String(), f.sender.String(), &models.UpdateEmail{Warmup: &on}); xerr != nil {
					t.Fatal(xerr)
				}
			}
			var status string
			if err := f.pool.QueryRow(t.Context(), `SELECT status FROM tasks WHERE id=$1`, f.task).Scan(&status); err != nil || status != "cancelled" {
				t.Fatal("upgrade re-enabled old stopped authorization", status, err)
			}
		})
	}
}

func TestLiveWarmupStopMailboxSwitchOffRetiresBothDirections(t *testing.T) {
	f, _, _, _, emails := stopFixture(t, 2)
	for _, status := range []string{"inactive", "active"} {
		if _, xerr := emails.Update(t.Context(), f.org.String(), f.recipient.String(), &models.UpdateEmail{Status: &status}); xerr != nil {
			t.Fatal(xerr)
		}
	}
	var status string
	if err := f.pool.QueryRow(t.Context(), `SELECT status FROM tasks WHERE id=$1`, f.task).Scan(&status); err != nil || status != "cancelled" {
		t.Fatal("mailbox switch-on revived stopped command", status, err)
	}
}

func TestLiveWarmupStopUpgradeRetiresOldPausedAndDirectionallyDisabledWork(t *testing.T) {
	for _, previous := range []string{"legacy_paused", "sender_disabled", "recipient_disabled"} {
		t.Run(previous, func(t *testing.T) {
			f, _, _, _, emails := stopFixture(t, 2)
			mailbox := f.sender
			query := `UPDATE email_accounts SET test_mode='diagnostic',test_send_enabled=false,test_receive_enabled=true WHERE id=$1`
			if previous == "legacy_paused" {
				query = `UPDATE email_accounts SET test_mode=NULL,warmup_paused_at=now() WHERE id=$1`
			} else if previous == "recipient_disabled" {
				mailbox = f.recipient
				query = `UPDATE email_accounts SET test_mode='diagnostic',test_send_enabled=true,test_receive_enabled=false WHERE id=$1`
			}
			if _, err := f.pool.Exec(t.Context(), query, mailbox); err != nil {
				t.Fatal(err)
			}
			if previous == "recipient_disabled" {
				on := true
				if _, xerr := emails.Update(t.Context(), f.org.String(), mailbox.String(), &models.UpdateEmail{TestReceiveEnabled: &on}); xerr != nil {
					t.Fatal(xerr)
				}
			} else if _, xerr := emails.SetWarmupLifecycle(t.Context(), f.org.String(), mailbox.String(), "resume"); xerr != nil {
				t.Fatal(xerr)
			}
			var status string
			if err := f.pool.QueryRow(t.Context(), `SELECT status FROM tasks WHERE id=$1`, f.task).Scan(&status); err != nil || status != "cancelled" {
				t.Fatal("upgrade revived previously disabled work", status, err)
			}
		})
	}
}

func TestLiveWarmupStopSerializesBeforeQueuedExecution(t *testing.T) {
	f, r, worker, nonce, emails := stopFixture(t, 2)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	blocker, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,268))`, f.task.String()); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan bool, 1)
	go func() {
		_, xerr := emails.SetWarmupLifecycle(ctx, f.org.String(), f.recipient.String(), "stop")
		stopped <- xerr == nil
	}()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var locked bool
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND mode='ExclusiveLock' AND granted
			AND objid=(hashtextextended($1,270)&4294967295)::oid)`, f.recipient.String()).Scan(&locked); err != nil {
			t.Fatal(err)
		}
		if locked {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("stop did not acquire participation lock")
		case <-ticker.C:
		}
	}
	executed := make(chan bool, 1)
	go func() {
		state, err := r.BeginOutbound(ctx, f.task, f.sender, worker, nonce)
		executed <- err == nil && state.State == "execute"
	}()
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if !<-stopped || <-executed {
		t.Fatal("queued command passed an in-progress stop")
	}
}

func TestLiveWarmupStopAlertUsesFreshExecutionNotLateResults(t *testing.T) {
	f, r, _, _, emails := stopFixture(t, 2)
	for _, mailbox := range []uuid.UUID{f.sender, f.recipient} {
		if _, xerr := emails.SetWarmupLifecycle(t.Context(), f.org.String(), mailbox.String(), "stop"); xerr != nil {
			t.Fatal(xerr)
		}
	}
	now := time.Now().UTC()
	if _, err := f.pool.Exec(t.Context(), `UPDATE tasks SET send_result_applied_at=$2 WHERE id=$1`, f.task, now); err != nil {
		t.Fatal(err)
	}
	check := func(at time.Time, count int) {
		t.Helper()
		rows, err := r.ListPostStopWarmupDispatches(t.Context(), at)
		if err != nil || len(rows) != count {
			t.Fatal("incorrect fresh start evidence", rows, err)
		}
	}
	check(now, 0)
	if _, err := f.pool.Exec(t.Context(), `UPDATE tasks SET send_executor_started_at=$2 WHERE id=$1`, f.task, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	check(now, 0)
	if _, err := f.pool.Exec(t.Context(), `UPDATE email_accounts SET warmup_stopped_at=$2 WHERE organization_id=$1`, f.org, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	check(now, 2)
	if _, err := f.pool.Exec(t.Context(), `UPDATE warmup_tasks SET target_account_id=NULL WHERE task_id=$1`, f.task); err != nil {
		t.Fatal(err)
	}
	f.token(t, "accepted@example.test", "Alert evidence", false)
	check(now, 2)
	check(now.Add(2*time.Hour), 0)
}
