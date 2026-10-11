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

func TestLiveScheduledJobRetirementUpgradePreservesExecutionHistory(t *testing.T) {
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
	name := "job_retirement_upgrade_" + uuid.NewString()[:8]
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
	if err := m.Migrate(285); err != nil {
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
	names := []string{"fleet_quarantine", "active_overdue", "active_error"}
	before := map[string]string{}
	for _, job := range names {
		exec(`INSERT INTO scheduled_job_runs(name,service,interval_seconds,last_status,last_error,run_count,error_count,last_started_at,last_finished_at,last_duration_ms,run_requested_at,next_run_at,updated_at) VALUES($1,'backend',300,'error','historical failure',1850,7,now()-interval '2 days',now()-interval '2 days'+interval '2 seconds',2000,now()-interval '1 day',now()-interval '2 days',now()-interval '2 days')`, job)
		var record string
		if err := conn.QueryRow(t.Context(), `SELECT to_jsonb(j)::text FROM scheduled_job_runs j WHERE name=$1`, job).Scan(&record); err != nil {
			t.Fatal(err)
		}
		before[job] = record
	}
	assertHistory := func(upgraded bool, targetRetired bool) {
		t.Helper()
		for _, job := range names {
			query := `SELECT to_jsonb(j)::text FROM scheduled_job_runs j WHERE name=$1`
			if upgraded {
				query = `SELECT (to_jsonb(j)-'retired_at')::text FROM scheduled_job_runs j WHERE name=$1`
			}
			var record string
			if err := conn.QueryRow(t.Context(), query, job).Scan(&record); err != nil || record != before[job] {
				t.Fatalf("migration changed execution history for %s: err=%v", job, err)
			}
			if upgraded {
				var retired bool
				if err := conn.QueryRow(t.Context(), `SELECT retired_at IS NOT NULL FROM scheduled_job_runs WHERE name=$1`, job).Scan(&retired); err != nil || retired != (job == "fleet_quarantine" && targetRetired) {
					t.Fatalf("unexpected retirement for %s: retired=%v err=%v", job, retired, err)
				}
			}
		}
	}
	if err := m.Migrate(286); err != nil {
		t.Fatal(err)
	}
	assertHistory(true, true)
	if err := m.Steps(-1); err != nil {
		t.Fatal(err)
	}
	assertHistory(false, false)
	if err := m.Migrate(286); err != nil {
		t.Fatal(err)
	}
	assertHistory(true, true)
	if err := m.Steps(-1); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE scheduled_job_runs SET service='consumer' WHERE name='fleet_quarantine'`)
	var consumerRecord string
	if err := conn.QueryRow(t.Context(), `SELECT to_jsonb(j)::text FROM scheduled_job_runs j WHERE name='fleet_quarantine'`).Scan(&consumerRecord); err != nil {
		t.Fatal(err)
	}
	before[names[0]] = consumerRecord
	if err := m.Migrate(286); err != nil {
		t.Fatal(err)
	}
	assertHistory(true, false)
}
