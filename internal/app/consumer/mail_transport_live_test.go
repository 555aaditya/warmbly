package jobs

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func TestLiveMailTransportConfirmedSendReconcilesOnlySend(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed", true: "rollback"}[rollback], func(t *testing.T) {
			d := liveDB(t)
			s := liveJobsService(d)
			s.EmailAccountErrorRepository = repository.NewEmailAccountErrorRepository(d)
			f := newSendResultFixture(t, d)
			task := f.stampSend(t, s)
			var generation uuid.UUID
			if err := d.QueryRow(t.Context(), `SELECT transport_generation FROM email_accounts WHERE id=$1`, f.mailbox).Scan(&generation); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-time.Hour)
			for _, op := range []models.MailTransportOperation{models.MailTransportUnknown, models.MailTransportSync, models.MailTransportSend} {
				data := &repository.CreateEmailAccountError{TransportGeneration: generation, EmailAccountID: f.mailbox, UserID: f.user, Operation: op, ObservedAt: &old, ErrorCode: "SERVER_UNREACHABLE", Severity: "WARNING", ResolveMethod: "RETRY", Title: "Transport", Message: "Transport report"}
				if op == models.MailTransportSend {
					data.TaskID = &task
				}
				if _, err := s.EmailAccountErrorRepository.CreateOnce(t.Context(), data); err != nil {
					t.Fatal(err)
				}
			}
			result := models.SendEmailResult{TransportGeneration: generation, TaskID: task, SentAt: old.Add(time.Minute), MessageID: "<confirmed@test.local>"}
			if rollback {
				// A real write after recovery fails, forcing the parent reconciliation to roll back.
				result.MessageID = string(make([]byte, 256))
				if err := s.HandleEmailSent(t.Context(), result); err == nil {
					t.Fatal("invalid database text unexpectedly reconciled")
				}
			} else if err := s.HandleEmailSent(t.Context(), result); err != nil {
				t.Fatal(err)
			}
			rows, err := s.EmailAccountErrorRepository.GetByAccountID(t.Context(), f.mailbox, true)
			if err != nil {
				t.Fatal(err)
			}
			want := 2
			if rollback {
				want = 3
			}
			if len(rows) != want {
				t.Fatalf("unresolved=%d want=%d", len(rows), want)
			}
			var n int
			if err := d.QueryRow(t.Context(), `SELECT count(*) FROM email_account_transport_recovery WHERE email_account_id=$1`, f.mailbox).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if rollback && n != 0 || !rollback && n != 1 {
				t.Fatalf("recovery transaction did not settle atomically: %d", n)
			}
			if !rollback {
				var source uuid.UUID
				if err := d.QueryRow(t.Context(), `SELECT source_task_id FROM email_account_transport_recovery WHERE email_account_id=$1 AND operation='send'`, f.mailbox).Scan(&source); err != nil || source != task {
					t.Fatal("confirmed task provenance missing", err)
				}
				for _, row := range rows {
					if row.Operation == models.MailTransportSend {
						t.Fatal("send transport did not recover")
					}
				}
			}
		})
	}
}

func TestLiveMailTransportUnreadableSyncStateCannotAcquireRecovery(t *testing.T) {
	d := liveDB(t)
	f := newSendResultFixture(t, d)
	stateRepo := repository.NewEmailSyncStateRepository(d)
	errRepo := repository.NewEmailAccountErrorRepository(d)
	s := &JobsService{EmailSyncStateRepository: stateRepo, EmailAccountErrorRepository: errRepo}
	var generation uuid.UUID
	if err := d.QueryRow(t.Context(), `SELECT transport_generation FROM email_accounts WHERE id=$1`, f.mailbox).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)
	if err := stateRepo.Put(t.Context(), f.user, f.mailbox, &models.SyncState{LastSyncedAt: &at}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(t.Context(), `UPDATE email_sync_state SET backfill_cursor='{"google_recovery":"unreadable"}' WHERE email_id=$1`, f.mailbox); err != nil {
		t.Fatal(err)
	}
	if _, err := stateRepo.Get(t.Context(), f.mailbox); err == nil {
		t.Fatal("fixture did not fail the native previous-state read")
	}
	observed := at.Add(-time.Minute)
	if _, err := errRepo.CreateOnce(t.Context(), &repository.CreateEmailAccountError{EmailAccountID: f.mailbox, UserID: f.user, Operation: models.MailTransportSync, TransportGeneration: generation, ObservedAt: &observed, ErrorCode: "SERVER_UNREACHABLE", Severity: "WARNING", ResolveMethod: "RETRY", Title: "Transport", Message: "Transport report"}); err != nil {
		t.Fatal(err)
	}
	counts := func() (int, int) {
		t.Helper()
		var recovery, unresolved int
		if err := d.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM email_account_transport_recovery WHERE email_account_id=$1),(SELECT count(*) FROM email_account_errors WHERE email_account_id=$1 AND resolved_at IS NULL)`, f.mailbox).Scan(&recovery, &unresolved); err != nil {
			t.Fatal(err)
		}
		return recovery, unresolved
	}
	event := &models.JobEventSyncState{EmailID: f.mailbox, UserID: f.user, State: models.SyncState{TransportGeneration: generation, LastSyncedAt: &at}}
	err := s.HandleSyncState(t.Context(), event)
	if recovery, unresolved := counts(); err == nil || recovery != 0 || unresolved != 1 {
		t.Fatalf("unreadable equal-timestamp relay was accepted: err=%v recovery=%d unresolved=%d", err, recovery, unresolved)
	}
	if _, err := stateRepo.Get(t.Context(), f.mailbox); err == nil {
		t.Fatal("failed relay overwrote unreadable previous state")
	}
	if _, err := d.Exec(t.Context(), `UPDATE email_sync_state SET backfill_cursor='{}' WHERE email_id=$1`, f.mailbox); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleSyncState(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if recovery, unresolved := counts(); recovery != 0 || unresolved != 1 {
		t.Fatal("readable equal-timestamp retry acquired recovery")
	}
	fresh := at.Add(10 * time.Second)
	event.State.LastSyncedAt = &fresh
	if err := s.HandleSyncState(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if recovery, unresolved := counts(); recovery != 1 || unresolved != 0 {
		t.Fatal("fresh typed completed pass failed to recover")
	}
}
