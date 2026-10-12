package orgtransfer

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func overwriteTransportFixture(t *testing.T) (Service, *pgxpool.Pool, tenantFixture, uuid.UUID, uuid.UUID, repository.EmailAccountErrorRepository) {
	t.Helper()
	svc, pool := liveImportService(t)
	f := newTenantFixture(t, pool)
	mailbox, task := uuid.New(), uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO email_accounts(id,user_id,organization_id,email,name,signature_plain,signature_html,provider)
		VALUES($1,$2,$3,$4,'Local','','','smtp_imap')`, mailbox, f.owner, f.org, mailbox.String()+"@test.local"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM email_accounts WHERE id=$1`, mailbox); err != nil {
			t.Error(err)
		}
	})
	if _, err := pool.Exec(t.Context(), `INSERT INTO tasks(id,email_account_id,task_type,status,message_id,send_result_state)
		VALUES($1,$2,'email','completed','','sent')`, task, mailbox); err != nil {
		t.Fatal(err)
	}
	return svc, pool, f, mailbox, task, repository.NewEmailAccountErrorRepository(&db.DB{Pool: pool})
}

func importTransportGeneration(t *testing.T, pool *pgxpool.Pool, mailbox uuid.UUID) uuid.UUID {
	t.Helper()
	var generation uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT transport_generation FROM email_accounts WHERE id=$1`, mailbox).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	return generation
}

func TestLiveImportMailTransportOverwriteResetsKnownReport(t *testing.T) {
	for _, operation := range []models.MailTransportOperation{models.MailTransportSync, models.MailTransportSend} {
		for _, old := range []bool{true, false} {
			for _, conflict := range []models.OrgImportConflict{models.OrgImportConflictSkip, models.OrgImportConflictOverwrite} {
				label := string(operation) + "/new_archive/" + string(conflict)
				if old {
					label = string(operation) + "/old_archive/" + string(conflict)
				}
				t.Run(label, func(t *testing.T) {
					svc, pool, f, mailbox, task, repo := overwriteTransportFixture(t)
					report := uuid.New()
					local := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Microsecond)
					archived := local.Add(-time.Hour)
					var reportTask *uuid.UUID
					if operation == models.MailTransportSend {
						reportTask = &task
					}
					if _, err := pool.Exec(t.Context(), `INSERT INTO email_account_errors(id,email_account_id,user_id,error_code,severity,resolve_method,title,message,task_id,created_at,operation,observation_source,received_at)
						VALUES($1,$2,$3,'SERVER_UNREACHABLE','WARNING','RETRY','Local','Local report',$4,$5,$6,'worker',$5)`, report, mailbox, f.owner, reportTask, local, operation); err != nil {
						t.Fatal(err)
					}
					row := map[string]any{"id": report, "email_account_id": mailbox, "user_id": f.owner, "error_code": "SERVER_UNREACHABLE", "severity": "WARNING", "resolve_method": "RETRY", "title": "Archive", "message": "Imported historical report", "task_id": reportTask, "created_at": archived}
					if !old {
						row["operation"] = operation
						row["observation_source"] = "worker"
						row["received_at"] = archived
					}
					archive := tenantArchive(t, map[string][]map[string]any{"email_account_errors": {row}})
					if _, err := svc.ImportFrom(t.Context(), f.org, archive, ImportOptions{Conflict: conflict, ActorUserID: f.owner}, nil); err != nil {
						t.Fatal(err)
					}
					var taskID *uuid.UUID
					if operation == models.MailTransportSend {
						taskID = &task
					}
					if err := repo.RecordTransportSuccess(t.Context(), mailbox, operation, local.Add(-30*time.Minute), taskID, importTransportGeneration(t, pool, mailbox)); err != nil {
						t.Fatal(err)
					}
					var op, source, message string
					var received, resolved *time.Time
					var storedTask *uuid.UUID
					var created time.Time
					if err := pool.QueryRow(t.Context(), `SELECT operation,observation_source,received_at,resolved_at,task_id,created_at,message FROM email_account_errors WHERE id=$1`, report).Scan(&op, &source, &received, &resolved, &storedTask, &created, &message); err != nil {
						t.Fatal(err)
					}
					wantOp, wantSource, wantCreated, wantMessage := "unknown", "unknown", archived, "Imported historical report"
					wantReceived := received == nil
					if conflict == models.OrgImportConflictSkip {
						wantOp = string(operation)
						wantSource = "worker"
						wantCreated = local
						wantMessage = "Local report"
						wantReceived = received != nil && received.Equal(local)
					}
					taskPreserved := storedTask == nil && reportTask == nil || storedTask != nil && reportTask != nil && *storedTask == *reportTask
					if op != wantOp || source != wantSource || !wantReceived || resolved != nil || !taskPreserved || !created.Equal(wantCreated) || message != wantMessage {
						t.Fatalf("overwrite inherited recovery authority or lost history: operation=%s source=%s receipt_present=%v resolved=%v task_preserved=%v clock_preserved=%v message=%s", op, source, received != nil, resolved != nil, taskPreserved, created.Equal(wantCreated), message)
					}
				})
			}
		}
	}
}

func TestLiveImportMailTransportOverwriteClearsDestinationClock(t *testing.T) {
	for _, conflict := range []models.OrgImportConflict{models.OrgImportConflictSkip, models.OrgImportConflictOverwrite} {
		t.Run(string(conflict), func(t *testing.T) {
			svc, pool, f, mailbox, task, repo := overwriteTransportFixture(t)
			at := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Microsecond)
			for _, op := range []models.MailTransportOperation{models.MailTransportSync, models.MailTransportSend} {
				var source *uuid.UUID
				if op == models.MailTransportSend {
					source = &task
				}
				if err := repo.RecordTransportSuccess(t.Context(), mailbox, op, at, source, importTransportGeneration(t, pool, mailbox)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := pool.Exec(t.Context(), `UPDATE email_accounts SET auth_checked_at=$2,send_recovery_hold=true,send_recovery_reason='unknown',send_recovery_task_id=$3 WHERE id=$1`, mailbox, at, task); err != nil {
				t.Fatal(err)
			}
			archive := tenantArchive(t, map[string][]map[string]any{"email_accounts": {{"id": mailbox, "user_id": f.owner, "organization_id": f.org, "email": "restored-" + mailbox.String() + "@test.local", "name": "Archive", "signature_plain": "", "signature_html": "", "provider": "smtp_imap", "send_recovery_hold": true, "send_recovery_reason": "unknown", "send_recovery_task_id": task}}})
			if _, err := svc.ImportFrom(t.Context(), f.org, archive, ImportOptions{Conflict: conflict, ActorUserID: f.owner}, nil); err != nil {
				t.Fatal(err)
			}
			var name, email string
			var held bool
			var heldTask uuid.UUID
			var checked time.Time
			if err := pool.QueryRow(t.Context(), `SELECT name,email,send_recovery_hold,send_recovery_task_id,auth_checked_at FROM email_accounts WHERE id=$1`, mailbox).Scan(&name, &email, &held, &heldTask, &checked); err != nil {
				t.Fatal(err)
			}
			wantName := "Archive"
			wantEmail := "restored-" + mailbox.String() + "@test.local"
			if conflict == models.OrgImportConflictSkip {
				wantName = "Local"
				wantEmail = mailbox.String() + "@test.local"
			}
			if name != wantName || email != wantEmail || !held || heldTask != task || !checked.Equal(at) {
				t.Fatal("not genuine overwrite or broad mailbox reset changed unrelated metadata/hold")
			}
			for _, op := range []models.MailTransportOperation{models.MailTransportSync, models.MailTransportSend} {
				observed := at.Add(-time.Hour)
				data := &repository.CreateEmailAccountError{TransportGeneration: importTransportGeneration(t, pool, mailbox), EmailAccountID: mailbox, UserID: f.owner, ErrorCode: "CONNECTION_LOST", Severity: "WARNING", ResolveMethod: "RETRY", Title: "Delayed", Message: "Typed delayed failure", Operation: op, ObservedAt: &observed}
				if op == models.MailTransportSend {
					data.TaskID = &task
				}
				created, err := repo.CreateOnce(t.Context(), data)
				if err != nil {
					t.Fatal(err)
				}
				var resolved bool
				if err := pool.QueryRow(t.Context(), `SELECT resolved_at IS NOT NULL FROM email_account_errors WHERE email_account_id=$1 AND error_code='CONNECTION_LOST' AND operation=$2`, mailbox, op).Scan(&resolved); err != nil {
					t.Fatal(err)
				}
				kept := conflict == models.OrgImportConflictSkip
				if resolved != kept || (created == nil) != kept {
					t.Errorf("stale destination %s clock suppressed post-overwrite failure: resolved=%v notification_suppressed=%v skip=%v", op, resolved, created == nil, kept)
				}
			}
		})
	}
}

func TestLiveImportMailTransportOverwriteKeepsAmbiguousHistory(t *testing.T) {
	svc, pool, f, mailbox, task, repo := overwriteTransportFixture(t)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	legacy := uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO email_account_errors(id,email_account_id,user_id,error_code,severity,resolve_method,title,message,created_at)
		VALUES($1,$2,$3,'SERVER_UNREACHABLE','WARNING','RETRY','Legacy','Unknown legacy report',$4)`, legacy, mailbox, f.owner, at); err != nil {
		t.Fatal(err)
	}
	known, err := repo.CreateOnce(t.Context(), &repository.CreateEmailAccountError{TransportGeneration: importTransportGeneration(t, pool, mailbox), EmailAccountID: mailbox, UserID: f.owner,
		ErrorCode: "SERVER_UNREACHABLE", Severity: "WARNING", ResolveMethod: "RETRY", Title: "Known", Message: "Known send report",
		Operation: models.MailTransportSend, TaskID: &task, ObservedAt: &at})
	if err != nil || known == nil {
		t.Fatalf("seed known report alongside genuine legacy unknown: %v", err)
	}
	archive := tenantArchive(t, map[string][]map[string]any{"email_account_errors": {{
		"id": known.ID, "email_account_id": mailbox, "user_id": f.owner, "error_code": "SERVER_UNREACHABLE", "severity": "WARNING",
		"resolve_method": "RETRY", "title": "Archive", "message": "Imported known report", "task_id": task, "created_at": at,
		"operation": "send", "observation_source": "worker", "received_at": at,
	}}})
	if _, err := svc.ImportFrom(t.Context(), f.org, archive, ImportOptions{Conflict: models.OrgImportConflictOverwrite, ActorUserID: f.owner}, nil); err != nil {
		var unchanged bool
		if check := pool.QueryRow(t.Context(), `SELECT COUNT(*)=2 AND BOOL_AND(resolved_at IS NULL)
			AND COUNT(*) FILTER (WHERE operation='unknown' AND message='Unknown legacy report')=1
			AND COUNT(*) FILTER (WHERE operation='send' AND message='Known send report')=1
			FROM email_account_errors WHERE email_account_id=$1`, mailbox).Scan(&unchanged); check != nil || !unchanged {
			t.Fatalf("failed import did not preserve both original unresolved histories: unchanged=%v err=%v", unchanged, check)
		}
		t.Fatalf("safe operation reset collides with unresolved unknown history: %v", err)
	}
	var reports int
	if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM email_account_errors WHERE email_account_id=$1 AND resolved_at IS NULL`, mailbox).Scan(&reports); err != nil || reports != 2 {
		t.Fatalf("history lost or invented resolution: reports=%d err=%v", reports, err)
	}
	for _, oldArchive := range []bool{true, false} {
		row := map[string]any{"id": known.ID, "email_account_id": mailbox, "user_id": f.owner, "error_code": "SERVER_UNREACHABLE", "severity": "WARNING", "resolve_method": "RETRY", "title": "History", "message": "Historical send report", "created_at": at, "task_id": task}
		if !oldArchive {
			row["operation"], row["observation_source"], row["received_at"] = "send", "worker", at
		}
		for range 2 {
			result, err := svc.ImportFrom(t.Context(), f.org, tenantArchive(t, map[string][]map[string]any{"email_account_errors": {row}}), ImportOptions{Conflict: models.OrgImportConflictOverwrite, ActorUserID: f.owner}, nil)
			if err != nil || result.RowCounts["email_account_errors"] != 1 {
				t.Fatal("repeated old/new history overwrite failed or wrong count", err)
			}
		}
	}
	active, err := repo.CreateOnce(t.Context(), &repository.CreateEmailAccountError{TransportGeneration: importTransportGeneration(t, pool, mailbox), EmailAccountID: mailbox, UserID: f.owner, ErrorCode: "SERVER_UNREACHABLE", Severity: "WARNING", ResolveMethod: "RETRY", Title: "Native", Message: "Native send failure", Operation: models.MailTransportSend, TaskID: &task, ObservedAt: &at})
	if err != nil || active == nil || active.ID == known.ID || active.ID == legacy {
		t.Fatal("historical report collided with local dedupe", err)
	}
	if err := repo.RecordTransportSuccess(t.Context(), mailbox, models.MailTransportSend, at.Add(time.Minute), &task, importTransportGeneration(t, pool, mailbox)); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM email_account_errors WHERE id=ANY($1::uuid[]) AND resolved_at IS NULL AND observation_source='unknown' AND operation='unknown'`, []uuid.UUID{known.ID, legacy}).Scan(&reports); err != nil || reports != 2 {
		t.Fatal("local recovery altered imported/legacy reports", reports, err)
	}
	var retainedTask uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT task_id FROM email_account_errors WHERE id=$1`, known.ID).Scan(&retainedTask); err != nil || retainedTask != task {
		t.Fatal("history lost source task", retainedTask, err)
	}
}
