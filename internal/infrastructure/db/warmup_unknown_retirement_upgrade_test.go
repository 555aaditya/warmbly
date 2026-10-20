package db

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestLiveWarmupUnknownRetirementUpgrade(t *testing.T) {
	dsn := os.Getenv("WARMBLY_TEST_DB")
	if dsn == "" {
		t.Skip("WARMBLY_TEST_DB not set")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1") {
		t.Fatal("upgrade requires local PostgreSQL")
	}
	admin, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	name := "warmup_retirement_upgrade_" + uuid.New().String()[:8]
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(t.Context(), "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+quoted); err != nil {
			t.Error(err)
		}
	}()
	u.Path = "/" + name
	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	m, err := openMigrate(source, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err = m.Migrate(314); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(t.Context(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	user, org, mailbox, task := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO users(id,email,first_name,last_name) VALUES($1,$2,'Upgrade','')`, user, user.String()+"@test.local")
	exec(`INSERT INTO organizations(id,name,owner_user_id) VALUES($1,'Upgrade',$2)`, org, user)
	exec(`INSERT INTO email_accounts(id,user_id,organization_id,email,name,signature_plain,signature_html,provider,send_recovery_hold,send_recovery_reason,send_recovery_task_id) VALUES($1,$2,$3,$4,'Upgrade','','','smtp_imap',true,'unknown',$5)`, mailbox, user, org, mailbox.String()+"@test.local", task)
	exec(`INSERT INTO tasks(id,email_account_id,task_type,status,message_id,send_result_state,send_reserved_at,send_executor_nonce,send_executor_started_at) VALUES($1,$2,'warmup','completed','<uncertain@test.local>','unknown',NOW(),gen_random_uuid(),NOW())`, task, mailbox)
	var before, after string
	query := `SELECT jsonb_build_object('task',to_jsonb(t)-'warmup_send_retired_at','account',to_jsonb(ea))::text FROM tasks t JOIN email_accounts ea ON ea.id=t.email_account_id WHERE t.id=$1`
	if err = conn.QueryRow(t.Context(), query, task).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = m.Migrate(315); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(t.Context(), query, task).Scan(&after); err != nil || before != after {
		t.Fatalf("migration rewrote historical outcome or hold: err=%v", err)
	}
	if err = m.Steps(-1); err != nil {
		t.Fatalf("empty retirement downgrade: %v", err)
	}
	if err = m.Steps(1); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE tasks SET status='cancelled',warmup_send_retired_at=NOW() WHERE id=$1`, task)
	for _, change := range []string{"status='pending'", "task_type='email'", "send_result_state='sent'", "send_result_applied_at=NOW()", "send_released_at=NOW()"} {
		if _, err = conn.Exec(t.Context(), `UPDATE tasks SET `+change+` WHERE id=$1`, task); err == nil {
			t.Fatalf("retirement invariant accepted %s", change)
		}
	}
	down, err := migrationsFS.ReadFile("migrations/000315_warmup_unknown_send_retirement.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(t.Context(), string(down)); err == nil || !strings.Contains(err.Error(), "cannot remove permanent warmup send retirement evidence") {
		t.Fatalf("rollback discarded replay fence: %v", err)
	}
	var retained bool
	if err = conn.QueryRow(t.Context(), `SELECT warmup_send_retired_at IS NOT NULL AND status='cancelled' AND send_result_state='unknown' FROM tasks WHERE id=$1`, task).Scan(&retained); err != nil || !retained {
		t.Fatalf("refused down changed retirement: retained=%t err=%v", retained, err)
	}
	version, dirty, err := m.Version()
	if err != nil || version != 315 || dirty {
		t.Fatalf("schema version=%d dirty=%t err=%v", version, dirty, err)
	}
}
