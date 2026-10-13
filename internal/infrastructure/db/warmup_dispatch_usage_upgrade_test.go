package db

import (
	"testing"

	"github.com/google/uuid"
)

func TestLiveWarmupDispatchUsageIndexUpgradePreservesEvidenceAndHolds(t *testing.T) {
	m, conn, _ := conflictUpgradeDatabaseAt(t, 291)
	user, org, mailbox, heldTask := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	conflictUpgradeExec(t, conn, `INSERT INTO users(id,email,first_name,last_name) VALUES($1,$2,'Upgrade','')`, user, user.String()+"@example.test")
	conflictUpgradeExec(t, conn, `INSERT INTO organizations(id,name,owner_user_id) VALUES($1,'Upgrade',$2)`, org, user)
	conflictUpgradeExec(t, conn, `INSERT INTO email_accounts(id,user_id,organization_id,email,name,signature_plain,signature_html,provider,send_recovery_hold,send_recovery_reason,send_recovery_task_id)
		VALUES($1,$2,$3,$4,'Upgrade','','','smtp_imap',true,'unknown',$5)`, mailbox, user, org, mailbox.String()+"@example.test", heldTask)
	conflictUpgradeExec(t, conn, `INSERT INTO tasks(id,email_account_id,task_type,status,message_id,send_reserved_at,send_result_state)
		VALUES($1,$2,'warmup','completed','',NOW()-INTERVAL '1 hour','unknown')`, heldTask, mailbox)
	conflictUpgradeExec(t, conn, `INSERT INTO tasks(email_account_id,task_type,status,message_id,completed_at)
		VALUES($1,'warmup','completed','confirmed@upgrade.test',NOW()-INTERVAL '2 days'),
		      ($1,'email','active','active@upgrade.test',NULL)`, mailbox)
	const snapshot = `SELECT jsonb_build_object(
		'tasks',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM tasks t),
		'accounts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM email_accounts a))::text`
	before := conflictUpgradeSnapshot(t, conn, snapshot)
	for _, version := range []uint{292, 291, 292} {
		if err := m.Migrate(version); err != nil {
			t.Fatal(err)
		}
		assertConflictMigrationVersion(t, m, version, false)
		if after := conflictUpgradeSnapshot(t, conn, snapshot); after != before {
			t.Fatalf("migration to %d changed send evidence or mailbox recovery holds", version)
		}
		var present bool
		if err := conn.QueryRow(t.Context(), `SELECT to_regclass('public.idx_tasks_dispatch_usage') IS NOT NULL`).Scan(&present); err != nil || present != (version == 292) {
			t.Fatalf("migration %d index present=%v error=%v", version, present, err)
		}
		if present {
			var usable bool
			if err := conn.QueryRow(t.Context(), `SELECT indisvalid AND indisready FROM pg_index
				WHERE indexrelid='public.idx_tasks_dispatch_usage'::regclass`).Scan(&usable); err != nil || !usable {
				t.Fatalf("migration %d index usable=%v error=%v", version, usable, err)
			}
		}
	}
}
