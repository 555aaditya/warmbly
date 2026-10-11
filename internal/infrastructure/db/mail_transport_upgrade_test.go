package db

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

const mailTransportMigrationVersion = 288

func TestLiveMailTransportCLIStartup(t *testing.T) {
	m, c, _ := conflictUpgradeDatabaseAt(t, 284)
	runConflictMigrationCLI(t, c)
	assertConflictMigrationVersion(t, m, conflictLatestMigration(t), false)
	var typed bool
	if err := c.QueryRow(t.Context(), `SELECT to_regclass('email_account_transport_recovery') IS NOT NULL AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='email_account_errors' AND column_name='operation')`).Scan(&typed); err != nil || !typed {
		t.Fatal("actual cmd/migrate did not apply typed extension", err)
	}
}

func TestLiveMailTransportUpgradePreservesHistoryAndRollback(t *testing.T) {
	for _, baseline := range []uint{284, 287} {
		t.Run(fmt.Sprint(baseline), func(t *testing.T) { testMailTransportUpgrade(t, baseline) })
	}
}

func testMailTransportUpgrade(t *testing.T, baseline uint) {
	t.Helper()
	m, c, _ := conflictUpgradeDatabaseAt(t, baseline)
	user, org, mailbox, task := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	at := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Microsecond)
	exec := func(sql string, args ...any) { t.Helper(); conflictUpgradeExec(t, c, sql, args...) }
	exec(`INSERT INTO users(id,email,first_name,last_name) VALUES($1,$2,'Upgrade','')`, user, user.String()+"@test.local")
	exec(`INSERT INTO organizations(id,name,owner_user_id) VALUES($1,'Upgrade',$2)`, org, user)
	exec(`INSERT INTO email_accounts(id,user_id,organization_id,email,name,signature_plain,signature_html,provider,send_recovery_hold,send_recovery_reason,send_recovery_task_id) VALUES($1,$2,$3,$4,'Upgrade','','','smtp_imap',true,'unknown',$5)`, mailbox, user, org, mailbox.String()+"@test.local", task)
	exec(`INSERT INTO tasks(id,email_account_id,task_type,status,message_id,scheduled_at,send_result_state) VALUES($1,$2,'warmup','completed','',NOW(),'sent')`, task, mailbox)
	exec(`INSERT INTO warmup_statistics(email_account_id,date,emails_sent,emails_replied,target_volume) VALUES($1,'2001-01-01',17,4,25)`, mailbox)
	mailboxBefore := conflictUpgradeSnapshot(t, c, `SELECT (to_jsonb(e)-'transport_generation')::text FROM email_accounts e LIMIT 1`)
	taskBefore := conflictUpgradeSnapshot(t, c, `SELECT to_jsonb(t)::text FROM tasks t LIMIT 1`)
	accountingBefore := conflictUpgradeSnapshot(t, c, `SELECT jsonb_agg(to_jsonb(s) ORDER BY email_account_id,date)::text FROM warmup_statistics s`)
	legacyIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for i, id := range legacyIDs {
		var taskID *uuid.UUID
		var resolved *time.Time
		if i == 1 {
			taskID = &task
		}
		if i == 2 {
			resolved = &at
		}
		exec(`INSERT INTO email_account_errors(id,email_account_id,user_id,error_code,severity,resolve_method,title,message,task_id,created_at,resolved_at) VALUES($1,$2,$3,$4,'WARNING','RETRY','History','Report',$5,$6,$7)`, id, mailbox, user, []string{"SERVER_UNREACHABLE", "CONNECTION_LOST", "IMAP_UNKNOWN"}[i], taskID, at, resolved)
	}
	assertHistory := func(typed bool) {
		t.Helper()
		for i, id := range legacyIDs {
			var created time.Time
			var taskID *uuid.UUID
			var resolved *time.Time
			if err := c.QueryRow(t.Context(), `SELECT created_at,task_id,resolved_at FROM email_account_errors WHERE id=$1`, id).Scan(&created, &taskID, &resolved); err != nil {
				t.Fatal(err)
			}
			if !created.Equal(at) || i == 1 && (taskID == nil || *taskID != task) || i != 1 && taskID != nil || (resolved != nil) != (i == 2) {
				t.Fatal("migration changed historical clock, resolution or task context")
			}
			if typed {
				var op, clock string
				var receipt *time.Time
				if err := c.QueryRow(t.Context(), `SELECT operation,observation_source,received_at FROM email_account_errors WHERE id=$1`, id).Scan(&op, &clock, &receipt); err != nil || op != "unknown" || clock != "unknown" || receipt != nil {
					t.Fatalf("legacy proof guessed: %s %s %v %v", op, clock, receipt, err)
				}
			}
		}
		if mailboxBefore != conflictUpgradeSnapshot(t, c, `SELECT (to_jsonb(e)-'transport_generation')::text FROM email_accounts e LIMIT 1`) || taskBefore != conflictUpgradeSnapshot(t, c, `SELECT to_jsonb(t)::text FROM tasks t LIMIT 1`) || accountingBefore != conflictUpgradeSnapshot(t, c, `SELECT jsonb_agg(to_jsonb(s) ORDER BY email_account_id,date)::text FROM warmup_statistics s`) {
			t.Fatal("migration altered holds, task evidence or accounting")
		}
	}
	upgrade := func() {
		t.Helper()
		if err := m.Migrate(mailTransportMigrationVersion); err != nil {
			t.Fatal(err)
		}
		assertConflictMigrationVersion(t, m, mailTransportMigrationVersion, false)
		assertHistory(true)
	}
	upgrade()
	if _, err := c.Exec(t.Context(), `INSERT INTO email_account_errors(email_account_id,user_id,error_code,severity,resolve_method,title,message) VALUES($1,$2,'SERVER_UNREACHABLE','WARNING','RETRY','Old writer','Report') ON CONFLICT(email_account_id,error_code) WHERE resolved_at IS NULL DO NOTHING`, mailbox, user); err == nil {
		t.Fatal("old account-plus-code writer unexpectedly compatible with native-only operation index")
	}
	assertHistory(true)
	t.Log("old account-plus-code writer refused; it must be quiesced before changing the index")
	if err := m.Steps(-1); err != nil {
		t.Fatal(err)
	}
	assertConflictMigrationVersion(t, m, 287, false)
	assertHistory(false)
	upgrade()
	for _, op := range []string{"sync", "unknown"} {
		t.Run("refuse_"+op, func(t *testing.T) {
			id := uuid.New()
			exec(`INSERT INTO email_account_errors(id,email_account_id,user_id,error_code,severity,resolve_method,title,message,operation,observation_source) VALUES($1,$2,$3,'SERVER_UNREACHABLE','WARNING','RETRY','History','Same-code history',$4,'unknown')`, id, mailbox, user, op)
			if err := m.Steps(-1); err == nil {
				t.Fatal("lossy real-driver downgrade accepted")
			}
			assertConflictMigrationVersion(t, m, 287, true)
			assertHistory(true)
			var preserved bool
			if err := c.QueryRow(t.Context(), `SELECT resolved_at IS NULL AND message='Same-code history' AND operation=$2 AND observation_source='unknown' FROM email_account_errors WHERE id=$1`, id, op).Scan(&preserved); err != nil || !preserved {
				t.Fatal("downgrade changed or discarded colliding history", err)
			}
			var clockTable, nativeIndex bool
			if err := c.QueryRow(t.Context(), `SELECT to_regclass('email_account_transport_recovery') IS NOT NULL,to_regclass('idx_email_account_errors_one_unresolved_per_operation') IS NOT NULL`).Scan(&clockTable, &nativeIndex); err != nil || !clockTable || !nativeIndex {
				t.Fatal("refusal partially removed typed schema", err)
			}
			// Only restore driver metadata after verifying the failed down left schema intact.
			if err := m.Force(mailTransportMigrationVersion); err != nil {
				t.Fatal(err)
			}
			assertConflictMigrationVersion(t, m, mailTransportMigrationVersion, false)
			exec(`UPDATE email_account_errors SET resolved_at=$2,resolved_by='Independent fixture confirmation' WHERE id=$1`, id, at)
			if err := m.Steps(-1); err != nil {
				t.Fatal(err)
			}
			assertConflictMigrationVersion(t, m, 287, false)
			assertHistory(false)
			upgrade()
		})
	}
	t.Logf("real driver schema%d -> 288 -> 287 -> 288 preserves legacy evidence and holds/accounting; different/same-operation history refuses down atomically, verified schema metadata repair and safe down/reupgrade pass", baseline)
}
