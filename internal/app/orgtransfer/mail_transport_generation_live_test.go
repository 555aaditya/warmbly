package orgtransfer

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func TestLiveImportMailTransportLateAndSpanningSuccess(t *testing.T) {
	for _, operation := range []models.MailTransportOperation{models.MailTransportSync, models.MailTransportSend} {
		for _, proof := range []string{"late", "spanning", "clock-skew", "legacy"} {
			t.Run(string(operation)+"/"+proof, func(t *testing.T) {
				svc, pool, f, mailbox, task, repo := overwriteTransportFixture(t)
				generation := importTransportGeneration(t, pool, mailbox)
				failedAt := time.Now().UTC().Add(-3 * time.Minute)
				var reportTask *uuid.UUID
				if operation == models.MailTransportSend {
					reportTask = &task
				}
				report, xerr := repo.CreateOnce(t.Context(), &repository.CreateEmailAccountError{TransportGeneration: generation, EmailAccountID: mailbox, UserID: f.owner, TaskID: reportTask, Operation: operation, ObservedAt: &failedAt, ErrorCode: "SERVER_UNREACHABLE", Severity: "WARNING", ResolveMethod: "RETRY", Title: "Transport", Message: "Preserved report"})
				if xerr != nil || report == nil {
					t.Fatal("native report", xerr)
				}
				if _, err := pool.Exec(t.Context(), `UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='authentication',send_recovery_task_id=$2 WHERE id=$1`, mailbox, task); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), `INSERT INTO warmup_statistics(email_account_id,date,emails_sent,emails_replied,target_volume) VALUES($1,CURRENT_DATE,7,3,10)`, mailbox); err != nil {
					t.Fatal(err)
				}
				if proof == "legacy" {
					generation = uuid.Nil
				}
				if _, err := pool.Exec(t.Context(), `UPDATE tasks SET send_result_state=NULL,send_result_applied_at=NULL WHERE id=$1`, task); err != nil {
					t.Fatal(err)
				}
				started, complete := make(chan struct{}), make(chan struct{})
				result := make(chan error, 1)
				go func() {
					captured := generation
					close(started)
					<-complete
					at := time.Now().UTC().Add(-time.Millisecond)
					if proof == "late" {
						at = at.Add(-time.Hour)
					}
					if proof == "clock-skew" {
						at = at.Add(-24 * time.Hour)
					}
					if operation == models.MailTransportSync {
						if err := repo.RecordTransportSuccess(t.Context(), mailbox, operation, at, nil, captured); err != nil {
							result <- err
						} else {
							result <- nil
						}
						return
					}
					outcome := models.SendEmailResult{TaskID: task, Success: true, MessageID: "accepted@example.test", SentAt: at, TransportGeneration: captured}
					result <- repository.NewTaskRepository(pool).(repository.SendResultRecovery).ApplySendResult(t.Context(), outcome, func(ctx context.Context) error {
						if err := repo.RecordTransportSuccess(ctx, mailbox, operation, at, &task, captured); err != nil {
							return err
						}
						return nil
					})
				}()
				<-started
				archive := tenantArchive(t, map[string][]map[string]any{"email_accounts": {{"id": mailbox, "user_id": f.owner, "organization_id": f.org, "email": mailbox.String() + "@restored.local", "name": "Restored", "signature_plain": "", "signature_html": "", "provider": "smtp_imap"}}})
				imported, err := svc.ImportFrom(t.Context(), f.org, archive, ImportOptions{Conflict: models.OrgImportConflictOverwrite, ActorUserID: f.owner}, nil)
				close(complete)
				if err != nil || imported.RowCounts["email_accounts"] != 1 {
					t.Fatal("actual overwrite failed", err)
				}
				if err := <-result; err != nil {
					t.Fatal("genuine result discarded", err)
				}
				current := importTransportGeneration(t, pool, mailbox)
				if current == generation || current == uuid.Nil {
					t.Fatal("generation did not rotate")
				}
				var clocks int
				if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM email_account_transport_recovery WHERE email_account_id=$1`, mailbox).Scan(&clocks); err != nil || clocks != 0 {
					t.Fatal("old completion recreated current recovery", clocks, err)
				}
				var unresolved bool
				if err := pool.QueryRow(t.Context(), `SELECT resolved_at IS NULL AND message='Preserved report' AND operation=$2 AND task_id IS NOT DISTINCT FROM $3::uuid FROM email_account_errors WHERE id=$1`, report.ID, operation, reportTask).Scan(&unresolved); err != nil || !unresolved {
					t.Fatal("old completion changed report evidence", err)
				}
				if operation == models.MailTransportSend {
					var confirmed bool
					if err := pool.QueryRow(t.Context(), `SELECT send_result_state='sent' AND send_result_applied_at IS NOT NULL FROM tasks WHERE id=$1`, task).Scan(&confirmed); err != nil || !confirmed {
						t.Fatal("confirmed task audit lost", err)
					}
				}
				var taskID *uuid.UUID
				if operation == models.MailTransportSend {
					freshTask := uuid.New()
					taskID = &freshTask
					if _, err := pool.Exec(t.Context(), `INSERT INTO tasks(id,email_account_id,task_type,status,message_id) VALUES($1,$2,'email','completed','')`, freshTask, mailbox); err != nil {
						t.Fatal(err)
					}
				}
				freshAt := time.Now().UTC().Add(-time.Millisecond)
				if taskID != nil {
					outcome := models.SendEmailResult{TaskID: *taskID, Success: true, MessageID: "fresh-accepted@example.test", SentAt: freshAt, TransportGeneration: current}
					if err := repository.NewTaskRepository(pool).(repository.SendResultRecovery).ApplySendResult(t.Context(), outcome, func(ctx context.Context) error {
						if xerr := repo.RecordTransportSuccess(ctx, mailbox, operation, freshAt, taskID, current); xerr != nil {
							return xerr
						}
						return nil
					}); err != nil {
						t.Fatal("fresh current native result", err)
					}
				} else if xerr := repo.RecordTransportSuccess(t.Context(), mailbox, operation, freshAt, nil, current); xerr != nil {
					t.Fatal(xerr)
				}
				if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM email_account_transport_recovery WHERE email_account_id=$1 AND operation=$2`, mailbox, operation).Scan(&clocks); err != nil || clocks != 1 {
					t.Fatal("fresh current success failed to recover", clocks, err)
				}
				if err := pool.QueryRow(t.Context(), `SELECT resolved_at IS NOT NULL FROM email_account_errors WHERE id=$1`, report.ID).Scan(&unresolved); err != nil || !unresolved {
					t.Fatal("fresh current success did not resolve native report", err)
				}
				var preserved bool
				if err := pool.QueryRow(t.Context(), `SELECT e.send_recovery_hold AND e.send_recovery_reason='authentication' AND e.send_recovery_task_id=$2 AND s.emails_sent=7 AND s.emails_replied=3 FROM email_accounts e JOIN warmup_statistics s ON s.email_account_id=e.id AND s.date=CURRENT_DATE WHERE e.id=$1`, mailbox, task).Scan(&preserved); err != nil || !preserved {
					t.Fatal("generation recovery changed restrictive hold/accounting", err)
				}
			})
		}
	}
}
