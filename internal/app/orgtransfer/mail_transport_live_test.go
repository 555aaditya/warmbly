package orgtransfer

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

func TestLiveImportMailTransportEvidenceResetsUnknown(t *testing.T) {
	svc, pool := liveImportService(t)
	dest := newTenantFixture(t, pool)
	mailbox, task, report := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO email_accounts(id,user_id,organization_id,email,name,signature_plain,signature_html,provider) VALUES($1,$2,$3,$4,'Imported','','','smtp_imap')`, mailbox, dest.owner, dest.org, mailbox.String()+"@test.local"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM email_accounts WHERE id=$1`, mailbox) })
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	archive := tenantArchive(t, map[string][]map[string]any{
		"tasks":                {{"id": task, "email_account_id": mailbox, "task_type": "warmup", "status": "completed", "message_id": "", "completed_at": at}},
		"email_account_errors": {{"id": report, "email_account_id": mailbox, "user_id": dest.owner, "task_id": task, "error_code": "SERVER_UNREACHABLE", "severity": "WARNING", "resolve_method": "RETRY", "title": "Historical", "message": "Transport report", "operation": "send", "observation_source": "worker", "received_at": at, "created_at": at}},
	})
	if _, err := svc.ImportFrom(t.Context(), dest.org, archive, ImportOptions{Conflict: models.OrgImportConflictOverwrite, ActorUserID: dest.owner}, nil); err != nil {
		t.Fatal(err)
	}
	var op, source string
	var received *time.Time
	var taskID uuid.UUID
	var created time.Time
	if err := pool.QueryRow(t.Context(), `SELECT operation,observation_source,received_at,task_id,created_at FROM email_account_errors WHERE id=$1`, report).Scan(&op, &source, &received, &taskID, &created); err != nil {
		t.Fatal(err)
	}
	if op != "unknown" || source != "unknown" || received != nil || taskID != task || !created.Equal(at) {
		t.Fatalf("import changed history or retained operation authority: %s %s %v %v %v", op, source, received, taskID, created)
	}
	if ExcludedTables["email_account_transport_recovery"] == "" {
		t.Fatal("success authority missing from transfer exclusions")
	}
	for _, table := range Tables {
		if table.Name == "email_account_transport_recovery" {
			t.Fatal("instance-local success evidence travels")
		}
	}
}
