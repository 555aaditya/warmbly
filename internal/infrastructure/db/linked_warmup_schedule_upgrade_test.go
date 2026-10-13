package db

import (
	"testing"

	"github.com/google/uuid"
)

func TestLiveLinkedWarmupScheduleUpgradePreservesExplicitProfilesAndTasks(t *testing.T) {
	m, c, _ := conflictUpgradeDatabaseAt(t, 291)
	user, org, instance, revoked := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec := func(sql string, args ...any) { t.Helper(); conflictUpgradeExec(t, c, sql, args...) }
	exec(`INSERT INTO users(id,email,first_name,last_name) VALUES($1,$2,'Upgrade','')`, user, user.String()+"@test.local")
	exec(`INSERT INTO organizations(id,name,owner_user_id) VALUES($1,'Upgrade',$2)`, org, user)
	exec(`INSERT INTO pool_link_instances(id,organization_id,name,token_hash) VALUES($1,$2,'Gateway',$3)`, instance, org, uuid.NewString())
	exec(`INSERT INTO pool_link_instances(id,organization_id,name,token_hash,revoked_at) VALUES($1,$2,'Revoked',$3,NOW())`, revoked, org, uuid.NewString())
	wantCorrection := map[uuid.UUID]bool{}
	for _, tc := range []struct {
		afterCreation                                                                bool
		name                                                                         string
		linked, managed, changed, custom, disabled, legacy, revoked, afterEnrollment bool
	}{
		{name: "untouched", linked: true},
		{name: "legacy_default", linked: true, legacy: true},
		{name: "edited_default", linked: true, changed: true},
		{name: "custom_ranges", linked: true, custom: true},
		{name: "direct"},
		{name: "managed", linked: true, managed: true},
		{name: "already_disabled", linked: true, disabled: true},
		{name: "revoked", linked: true, revoked: true},
		{name: "legacy_first_customer_save", linked: true, afterEnrollment: true},
		{name: "legacy_customer_save_before_enrollment", linked: true, afterCreation: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.New()
			wantCorrection[id] = tc.name == "untouched" || tc.name == "legacy_default"
			if tc.afterCreation || tc.afterEnrollment {
				exec(`INSERT INTO email_accounts(id,user_id,organization_id,email,name,signature_plain,signature_html,provider) VALUES($1,$2,$3,$4,'Upgrade','','','smtp_imap')`, id, user, org, id.String()+"@test.local")
			} else {
				exec(`WITH mailbox AS (
					INSERT INTO email_accounts(id,user_id,organization_id,email,name,signature_plain,signature_html,provider)
					VALUES($1,$2,$3,$4,'Upgrade','','','smtp_imap') RETURNING id)
					INSERT INTO email_account_behavior(email_account_id,enabled) SELECT id,$5 FROM mailbox`, id, user, org, id.String()+"@test.local", !tc.disabled)
			}
			if tc.afterCreation {
				exec(`INSERT INTO email_account_behavior(email_account_id,enabled) VALUES($1,$2)`, id, !tc.disabled)
			}
			if tc.linked {
				inst := instance
				if tc.revoked {
					inst = revoked
				}
				exec(`INSERT INTO pool_link_mailboxes(instance_id,remote_id,email_account_id,managed) VALUES($1,$2,$3,$4)`, inst, uuid.New(), id, tc.managed)
			}
			if tc.afterEnrollment {
				exec(`INSERT INTO email_account_behavior(email_account_id,enabled) VALUES($1,true)`, id)
			}
			if tc.changed {
				exec(`UPDATE email_account_behavior SET updated_at=created_at+INTERVAL '1 second' WHERE email_account_id=$1`, id)
			}
			if tc.custom {
				exec(`UPDATE email_account_behavior SET weekdays=127 WHERE email_account_id=$1`, id)
			}
			if tc.legacy {
				exec(`UPDATE email_account_behavior SET daily_limit_min=30,daily_limit_max=45 WHERE email_account_id=$1`, id)
			}
			exec(`INSERT INTO tasks(id,email_account_id,task_type,status,message_id,scheduled_at) VALUES($1,$2,'warmup','pending','',NOW()+INTERVAL '1 day')`, uuid.New(), id)
		})
	}
	tasks := conflictUpgradeSnapshot(t, c, `SELECT jsonb_agg(to_jsonb(t) ORDER BY id)::text FROM tasks t`)
	type profileSnapshot struct{ full, ranges string }
	profiles := map[uuid.UUID]profileSnapshot{}
	rows, err := c.Query(t.Context(), `SELECT b.email_account_id,to_jsonb(b)::text,(to_jsonb(b)-'enabled'-'updated_at')::text FROM email_account_behavior b`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uuid.UUID
		var state profileSnapshot
		if err := rows.Scan(&id, &state.full, &state.ranges); err != nil {
			t.Fatal(err)
		}
		profiles[id] = state
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if err := m.Migrate(292); err != nil {
		t.Fatal(err)
	}
	assertConflictMigrationVersion(t, m, 292, false)
	var corrected int
	if err := c.QueryRow(t.Context(), `SELECT COUNT(*) FROM email_account_behavior WHERE NOT enabled AND created_at<>updated_at`).Scan(&corrected); err != nil || corrected != 2 {
		t.Fatalf("corrected=%d err=%v", corrected, err)
	}
	for id, state := range profiles {
		var enabled bool
		var now profileSnapshot
		if err := c.QueryRow(t.Context(), `SELECT enabled,to_jsonb(b)::text,(to_jsonb(b)-'enabled'-'updated_at')::text FROM email_account_behavior b WHERE email_account_id=$1`, id).Scan(&enabled, &now.full, &now.ranges); err != nil {
			t.Fatal(err)
		}
		if !wantCorrection[id] {
			if now != state {
				t.Fatal("customer profile changed")
			}
			continue
		}
		if enabled || now.ranges != state.ranges {
			t.Fatal("default correction was missing or changed profile ranges")
		}
	}
	if tasks != conflictUpgradeSnapshot(t, c, `SELECT jsonb_agg(to_jsonb(t) ORDER BY id)::text FROM tasks t`) {
		t.Fatal("upgrade forced or changed queued tasks")
	}
	after := conflictUpgradeSnapshot(t, c, `SELECT jsonb_agg(to_jsonb(b) ORDER BY email_account_id)::text FROM email_account_behavior b`)
	if err := m.Steps(-1); err != nil {
		t.Fatal(err)
	}
	if after != conflictUpgradeSnapshot(t, c, `SELECT jsonb_agg(to_jsonb(b) ORDER BY email_account_id)::text FROM email_account_behavior b`) {
		t.Fatal("rollback re-enabled inherited workdays")
	}
	if err := m.Migrate(292); err != nil {
		t.Fatal(err)
	}
	if after != conflictUpgradeSnapshot(t, c, `SELECT jsonb_agg(to_jsonb(b) ORDER BY email_account_id)::text FROM email_account_behavior b`) {
		t.Fatal("repeat upgrade changed explicit profiles")
	}
}
