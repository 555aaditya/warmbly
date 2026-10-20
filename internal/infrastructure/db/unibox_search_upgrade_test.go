package db

import (
	"context"
	"net/url"
	"os"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestLiveUniboxSearchUpgrade(t *testing.T) {
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
	name := "unibox_search_upgrade_" + uuid.New().String()[:8]
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE DATABASE "+quoted); err != nil {
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
	if err := m.Migrate(314); err != nil {
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
	user, org, mailbox, message := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO users(id,email,first_name,last_name) VALUES($1,'search@upgrade.test','Search','Upgrade')`, user)
	exec(`INSERT INTO organizations(id,name,owner_user_id) VALUES($1,'Search upgrade',$2)`, org, user)
	exec(`INSERT INTO email_accounts(id,user_id,organization_id,email,name,signature_plain,signature_html,provider)
		VALUES($1,$2,$3,'mailbox@upgrade.test','Search','','','smtp_imap')`, mailbox, user, org)
	exec(`INSERT INTO unibox_emails(id,user_id,email_id,from_addr,to_addr,cc,subject,body_text)
		VALUES($1,$2,$3,ARRAY['Existing Sender <sender@upgrade.test>'],ARRAY['Recipient'],ARRAY['Copy'],'History','Original body')`, message, user, mailbox)
	if err := m.Up(); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		var joined string
		if err := conn.QueryRow(t.Context(), `SELECT public.unibox_participants(from_addr,to_addr,cc)
			FROM unibox_emails WHERE id=$1 AND subject='History' AND body_text='Original body'`, message).Scan(&joined); err != nil || joined != "Existing Sender <sender@upgrade.test>\nRecipient\nCopy" {
			t.Fatal("upgrade changed existing mail", joined, err)
		}
		var valid bool
		if err := conn.QueryRow(t.Context(), `SELECT count(*)=2 AND bool_and(indisvalid AND indisready)
			FROM pg_index WHERE indexrelid IN ('public.idx_unibox_emails_participants_trgm'::regclass,'public.idx_unibox_emails_date'::regclass)`).Scan(&valid); err != nil || !valid {
			t.Fatal("search indexes not usable", valid, err)
		}
		if err := conn.QueryRow(t.Context(), `SELECT public.unibox_participants(NULL,ARRAY['Only recipient'],NULL)`).Scan(&joined); err != nil || joined != "Only recipient" {
			t.Fatal("null participant arrays", joined, err)
		}
	}
	check()
	if err := m.Migrate(314); err != nil {
		t.Fatal("rollback", err)
	}
	var removed bool
	if err := conn.QueryRow(t.Context(), `SELECT to_regprocedure('public.unibox_participants(text[],text[],text[])') IS NULL
		AND to_regclass('public.idx_unibox_emails_participants_trgm') IS NULL
		AND to_regclass('public.idx_unibox_emails_date') IS NULL`).Scan(&removed); err != nil || !removed {
		t.Fatal("rollback left search objects", removed, err)
	}
	if err := m.Up(); err != nil {
		t.Fatal("reapply", err)
	}
	check()
}
