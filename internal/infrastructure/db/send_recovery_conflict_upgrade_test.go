package db

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const conflictPredecessorVersion = 286
const conflictMigrationVersion = 287

func conflictUpgradeDatabase(t *testing.T) (*migrate.Migrate, *pgx.Conn, uint) {
	t.Helper()
	return conflictUpgradeDatabaseAt(t, conflictPredecessorVersion)
}

func conflictUpgradeDatabaseAt(t *testing.T, start uint) (*migrate.Migrate, *pgx.Conn, uint) {
	t.Helper()
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
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	name := "send_conflict_upgrade_" + uuid.NewString()[:8]
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+quoted); err != nil {
			t.Error(err)
		}
	})
	u.Path = "/" + name
	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	m, err := openMigrate(source, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.Close() })
	if err := m.Migrate(start); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(t.Context(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	assertConflictMigrationVersion(t, m, start, false)
	return m, conn, conflictMigrationVersion
}

func conflictMigrationCommand(t *testing.T, conn *pgx.Conn) *exec.Cmd {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "go", "run", "./cmd/migrate")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PRIMARY_DB="+conn.Config().ConnString())
	return cmd
}

func runConflictMigrationCLI(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	output, err := conflictMigrationCommand(t, conn).CombinedOutput()
	if err != nil {
		t.Fatalf("actual cmd/migrate failed: %v\n%s", err, output)
	}
	t.Log("actual cmd/migrate applied embedded production migrations")
}

func runConflictUpgrade(t *testing.T, m *migrate.Migrate, conn *pgx.Conn) {
	t.Helper()
	if conflictLatestMigration(t) > conflictMigrationVersion {
		if err := m.Migrate(conflictMigrationVersion); err != nil {
			t.Fatal(err)
		}
		t.Log("later migrations exist; native version-specific upgrade remains pinned to 287")
		return
	}
	runConflictMigrationCLI(t, conn)
}

func conflictLatestMigration(t *testing.T) uint {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var latest uint
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		version, err := strconv.ParseUint(strings.SplitN(entry.Name(), "_", 2)[0], 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		latest = max(latest, uint(version))
	}
	return latest
}

func assertConflictMigrationVersion(t *testing.T, m *migrate.Migrate, want uint, wantDirty bool) {
	t.Helper()
	got, dirty, err := m.Version()
	if err != nil || got != want || dirty != wantDirty {
		t.Fatalf("version=%d dirty=%v want=%d/%v err=%v", got, dirty, want, wantDirty, err)
	}
}

func conflictUpgradeExec(t *testing.T, conn *pgx.Conn, query string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func conflictUpgradeSnapshot(t *testing.T, conn *pgx.Conn, query string) string {
	t.Helper()
	var data string
	if err := conn.QueryRow(t.Context(), query).Scan(&data); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestLiveSendRecoveryConflictUpgradePreservesHistoricalEvidence(t *testing.T) {
	testSendRecoveryConflictUpgrade(t, conflictPredecessorVersion, false)
}

func TestLiveSendRecoveryConflictCLIStartup(t *testing.T) {
	m, conn, _ := conflictUpgradeDatabaseAt(t, 284)
	runConflictMigrationCLI(t, conn)
	assertConflictMigrationVersion(t, m, conflictLatestMigration(t), false)
}

func TestLiveSendRecoveryConflictProduction284UpgradePreservesHistoricalEvidence(t *testing.T) {
	testSendRecoveryConflictUpgrade(t, 284, false)
}

func TestLiveSendRecoveryConflictHistoricalOnlyDownRefuses(t *testing.T) {
	testSendRecoveryConflictUpgrade(t, conflictPredecessorVersion, true)
}

func testSendRecoveryConflictUpgrade(t *testing.T, start uint, historicalOnly bool) {
	t.Helper()
	m, conn, candidate := conflictUpgradeDatabaseAt(t, start)
	user, org, mailbox, task := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	conflictUpgradeExec(t, conn, `SET TIME ZONE 'Pacific/Honolulu'`)
	conflictUpgradeExec(t, conn, `INSERT INTO users(id,email,first_name,last_name)VALUES($1,$2,'Upgrade','')`, user, user.String()+"@example.test")
	conflictUpgradeExec(t, conn, `INSERT INTO organizations(id,name,owner_user_id)VALUES($1,'Upgrade',$2)`, org, user)
	conflictUpgradeExec(t, conn, `INSERT INTO email_accounts(id,user_id,organization_id,email,name,signature_plain,signature_html,provider,send_recovery_hold,send_recovery_reason,send_recovery_task_id)
		VALUES($1,$2,$3,$4,'Upgrade','','','smtp_imap',true,'unknown',$5)`, mailbox, user, org, mailbox.String()+"@example.test", task)
	conflictUpgradeExec(t, conn, `INSERT INTO tasks(id,email_account_id,task_type,status,message_id,send_reserved_at,send_result_state)
		VALUES($1,$2,'email','completed','',NOW()-INTERVAL '1 hour','unknown')`, task, mailbox)
	conflictUpgradeExec(t, conn, `INSERT INTO warmup_statistics(email_account_id,date,emails_sent,emails_replied,target_volume)
		VALUES($1,'2001-01-01',17,4,25)`, mailbox)
	for _, evidence := range []string{"authentication_repaired", "operator_provider_confirmation", "operator_confirmed_sent"} {
		reason := "conflict"
		if evidence == "authentication_repaired" {
			reason = "authentication"
		} else if evidence == "operator_confirmed_sent" {
			reason = "unknown"
		}
		conflictUpgradeExec(t, conn, `INSERT INTO send_recovery_resolutions(organization_id,email_account_id,recovery_task_id,evidence_task_id,previous_reason,evidence_type,confirmation_reference,confirmed_result,created_at,conflict_detected_at)
			VALUES($1,$2,$3,$3,$4,$5,'Historical fixture',CASE WHEN $4='unknown' THEN jsonb_build_object('task_id',$3::uuid::text,'success',true) END,'2001-01-01Z','2099-01-01Z')`, org, mailbox, task, reason, evidence)
	}
	auditBefore := conflictUpgradeSnapshot(t, conn, `SELECT jsonb_agg(to_jsonb(r) ORDER BY id)::text FROM send_recovery_resolutions r`)
	mailboxBefore := conflictUpgradeSnapshot(t, conn, `SELECT to_jsonb(e)::text FROM email_accounts e LIMIT 1`)
	taskBefore := conflictUpgradeSnapshot(t, conn, `SELECT to_jsonb(t)::text FROM tasks t LIMIT 1`)
	accountingBefore := conflictUpgradeSnapshot(t, conn, `SELECT jsonb_agg(to_jsonb(s) ORDER BY email_account_id,date)::text FROM warmup_statistics s`)
	runConflictUpgrade(t, m, conn)
	assertConflictMigrationVersion(t, m, candidate, false)
	auditAfter := conflictUpgradeSnapshot(t, conn, `SELECT jsonb_agg(to_jsonb(r)-'recorded_locally_at'-'conflict_resolved_at'-'conflicting_result_state' ORDER BY id)::text FROM send_recovery_resolutions r`)
	if auditBefore != auditAfter || mailboxBefore != conflictUpgradeSnapshot(t, conn, `SELECT to_jsonb(e)::text FROM email_accounts e LIMIT 1`) || taskBefore != conflictUpgradeSnapshot(t, conn, `SELECT to_jsonb(t)::text FROM tasks t LIMIT 1`) || accountingBefore != conflictUpgradeSnapshot(t, conn, `SELECT jsonb_agg(to_jsonb(s) ORDER BY email_account_id,date)::text FROM warmup_statistics s`) {
		t.Fatal("upgrade changed historical evidence, task/accounting or mailbox/hold state")
	}
	var trusted, projected, badDefaults int
	if err := conn.QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE recorded_locally_at IS NOT NULL),count(*) FILTER(WHERE conflicting_result_state IS NOT NULL OR conflict_resolved_at IS NOT NULL) FROM send_recovery_resolutions`).Scan(&trusted, &projected); err != nil || trusted != 0 || projected != 0 {
		t.Fatalf("historical evidence acquired local authority: trusted=%d projected=%d err=%v", trusted, projected, err)
	}
	if err := conn.QueryRow(t.Context(), `SELECT count(*) FROM information_schema.columns WHERE table_name='send_recovery_resolutions' AND column_name IN('recorded_locally_at','conflict_resolved_at','conflicting_result_state') AND (is_nullable<>'YES' OR column_default IS NOT NULL)`).Scan(&badDefaults); err != nil || badDefaults != 0 {
		t.Fatalf("new fields infer provenance: badDefaults=%d err=%v", badDefaults, err)
	}
	if historicalOnly {
		assertConflictDownRefusal(t, m, conn, candidate)
		return
	}
	// Local-only uniqueness leaves history intact and permits a fresh confirmation.
	conflictUpgradeExec(t, conn, `INSERT INTO send_recovery_resolutions(organization_id,email_account_id,recovery_task_id,previous_reason,evidence_type,confirmation_reference,recorded_locally_at)
		VALUES($1,$2,$3,'unknown','operator_confirmed_not_sent','Fresh fixture',clock_timestamp())`, org, mailbox, task)
	for _, state := range []string{"sent", "failed"} {
		t.Run("automatic_"+state, func(t *testing.T) {
			newTask := uuid.New()
			conflictUpgradeExec(t, conn, `INSERT INTO tasks(id,email_account_id,task_type,status,message_id,send_result_state,send_result_applied_at)
				VALUES($1,$2,'email','completed','',$3,NOW())`, newTask, mailbox, map[string]string{"sent": "failed", "failed": "sent"}[state])
			insert := `INSERT INTO send_recovery_resolutions(organization_id,email_account_id,recovery_task_id,previous_reason,evidence_type,conflicting_result_state,conflict_detected_at,recorded_locally_at)
				VALUES($1,$2,$3,'conflict','authoritative_result_conflict',$4,clock_timestamp(),clock_timestamp()) RETURNING id`
			var audit uuid.UUID
			if err := conn.QueryRow(t.Context(), insert, org, mailbox, newTask, state).Scan(&audit); err != nil {
				t.Fatal(err)
			}
			for _, mutation := range []string{
				`confirmed_result='{"success":true}'`, `evidence_task_id=recovery_task_id`, `confirmation_reference='not automatic'`,
				`previous_reason='unknown'`, `conflicting_result_state=NULL`, `conflicting_result_state='unknown'`, `conflict_detected_at=NULL`,
			} {
				_, err := conn.Exec(t.Context(), `UPDATE send_recovery_resolutions SET `+mutation+` WHERE id=$1`, audit)
				var pe *pgconn.PgError
				if !errors.As(err, &pe) || pe.Code != "23514" {
					t.Fatalf("automatic evidence mutation %s accepted or wrong failure: %v", mutation, err)
				}
			}
			if err := conn.QueryRow(t.Context(), insert, org, mailbox, newTask, state).Scan(&audit); err == nil {
				t.Fatal("duplicate local automatic evidence accepted")
			}
		})
	}
	t.Logf("real migrations 1..%d -> %d: historical rows/hold/accounting preserved; no provenance backfill; both payload-free conflict states and constraints verified", start, candidate)
	assertConflictDownRefusal(t, m, conn, candidate)
}

func assertConflictDownRefusal(t *testing.T, m *migrate.Migrate, conn *pgx.Conn, candidate uint) {
	t.Helper()
	rowsQuery := `SELECT jsonb_agg(to_jsonb(r) ORDER BY id)::text FROM send_recovery_resolutions r`
	columnsQuery := `SELECT jsonb_agg(to_jsonb(c) ORDER BY ordinal_position)::text FROM information_schema.columns c WHERE table_schema='public' AND table_name='send_recovery_resolutions'`
	constraintsQuery := `SELECT jsonb_agg(jsonb_build_object('name',conname,'definition',pg_get_constraintdef(oid)) ORDER BY conname)::text FROM pg_constraint WHERE conrelid='send_recovery_resolutions'::regclass`
	indexesQuery := `SELECT jsonb_agg(to_jsonb(i) ORDER BY indexname)::text FROM pg_indexes i WHERE schemaname='public' AND tablename='send_recovery_resolutions'`
	queries := []string{rowsQuery, columnsQuery, constraintsQuery, indexesQuery}
	before := make([]string, len(queries))
	for i, query := range queries {
		before[i] = conflictUpgradeSnapshot(t, conn, query)
	}
	err := m.Steps(-1)
	if err == nil || !strings.Contains(err.Error(), "Recovery audit evidence remains") {
		t.Fatalf("evidence-bearing down did not refuse: %v", err)
	}
	for i, query := range queries {
		if before[i] != conflictUpgradeSnapshot(t, conn, query) {
			t.Fatalf("refused down changed preserved rows or schema projection %d", i)
		}
	}
	assertConflictMigrationVersion(t, m, candidate-1, true)
	var dirty migrate.ErrDirty
	if err := m.Up(); !errors.As(err, &dirty) {
		t.Fatalf("dirty rejected downgrade was silently retried: %v", err)
	}
	if output, err := conflictMigrationCommand(t, conn).CombinedOutput(); err == nil || !strings.Contains(string(output), "Dirty database version") {
		t.Fatalf("actual cmd/migrate silently retried dirty downgrade: %v\n%s", err, output)
	}
	for i, query := range queries {
		if before[i] != conflictUpgradeSnapshot(t, conn, query) {
			t.Fatalf("rejected CLI restart changed preserved rows or schema projection %d", i)
		}
	}
	t.Log("real runner down refused without row/column/constraint/index changes; schema_migrations is dirty at down target, not a successful downgrade")
}

func TestLiveSendRecoveryConflictEmptyDownUp(t *testing.T) {
	m, conn, candidate := conflictUpgradeDatabase(t)
	before := conflictUpgradeSnapshot(t, conn, `SELECT jsonb_agg(to_jsonb(c) ORDER BY ordinal_position)::text FROM information_schema.columns c WHERE table_schema='public' AND table_name='send_recovery_resolutions'`)
	constraintsQuery := `SELECT jsonb_agg(jsonb_build_object('name',conname,'definition',pg_get_constraintdef(oid)) ORDER BY conname)::text FROM pg_constraint WHERE conrelid='send_recovery_resolutions'::regclass`
	constraintsBefore := conflictUpgradeSnapshot(t, conn, constraintsQuery)
	runConflictUpgrade(t, m, conn)
	assertConflictMigrationVersion(t, m, candidate, false)
	if err := m.Steps(-1); err != nil {
		t.Fatal(err)
	}
	assertConflictMigrationVersion(t, m, conflictPredecessorVersion, false)
	after := conflictUpgradeSnapshot(t, conn, `SELECT jsonb_agg(to_jsonb(c) ORDER BY ordinal_position)::text FROM information_schema.columns c WHERE table_schema='public' AND table_name='send_recovery_resolutions'`)
	if before != after || constraintsBefore != conflictUpgradeSnapshot(t, conn, constraintsQuery) {
		t.Fatal("empty downgrade did not restore original columns and constraints")
	}
	var indexRestored bool
	if err := conn.QueryRow(t.Context(), `SELECT pg_get_expr(indpred,indrelid) NOT LIKE '%recorded_locally_at%' FROM pg_index WHERE indexrelid='send_recovery_unknown_resolution'::regclass`).Scan(&indexRestored); err != nil || !indexRestored {
		t.Fatalf("old unknown uniqueness not restored: %v %v", indexRestored, err)
	}
	runConflictUpgrade(t, m, conn)
	assertConflictMigrationVersion(t, m, candidate, false)
	t.Log("real runner empty-table candidate down -> 286 -> up restores original schema and re-applies extension")
}
