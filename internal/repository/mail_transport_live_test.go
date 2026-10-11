package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/models"
)

type transportFixture struct {
	db                  *db.DB
	pool                *pgxpool.Pool
	repo                EmailAccountErrorRepository
	account, user, task uuid.UUID
	generation          uuid.UUID
}

func newTransportFixture(t *testing.T) transportFixture {
	t.Helper()
	d, pool := liveContactDB(t)
	f := transportFixture{db: d, pool: pool, repo: NewEmailAccountErrorRepository(d), account: uuid.New(), user: uuid.New(), task: uuid.New()}
	org := uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users(id,first_name,last_name,email,password_hash) VALUES($1,'Transport','Test',$2,'x')`, []any{f.user, org.String() + "@test.local"}},
		{`INSERT INTO organizations(id,name,slug,owner_user_id) VALUES($1,'Transport',$2,$3)`, []any{org, org.String(), f.user}},
		{`INSERT INTO email_accounts(id,user_id,organization_id,email,name,signature_plain,signature_html,provider) VALUES($1,$2,$3,$4,'Transport','','','smtp_imap')`, []any{f.account, f.user, org, f.account.String() + "@test.local"}},
		{`INSERT INTO tasks(id,task_type,email_account_id,status,message_id,scheduled_at,send_result_state) VALUES($1,'warmup',$2,'completed','',NOW(),'sent')`, []any{f.task, f.account}},
	} {
		if _, err := pool.Exec(t.Context(), q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, q := range []struct {
			sql string
			id  uuid.UUID
		}{
			{`DELETE FROM email_accounts WHERE id=$1`, f.account},
			{`DELETE FROM organizations WHERE id=$1`, org},
			{`DELETE FROM users WHERE id=$1`, f.user},
		} {
			if _, err := pool.Exec(context.Background(), q.sql, q.id); err != nil {
				t.Error(err)
			}
		}
	})
	if err := pool.QueryRow(t.Context(), `SELECT transport_generation FROM email_accounts WHERE id=$1`, f.account).Scan(&f.generation); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f transportFixture) report(operation models.MailTransportOperation, at *time.Time) *CreateEmailAccountError {
	d := &CreateEmailAccountError{TransportGeneration: f.generation, EmailAccountID: f.account, UserID: f.user, ErrorCode: "SERVER_UNREACHABLE", Severity: "WARNING", ResolveMethod: "RETRY", Title: "Transport", Message: "Transport report", Operation: operation, ObservedAt: at}
	if operation == models.MailTransportSend {
		d.TaskID = &f.task
	}
	return d
}

func (f transportFixture) errors(t *testing.T, unresolved bool) []EmailAccountError {
	t.Helper()
	rows, err := f.repo.GetByAccountID(t.Context(), f.account, unresolved)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestLiveMailTransportOperationIsolationAndUnknownHold(t *testing.T) {
	f := newTransportFixture(t)
	old := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	for _, operation := range []models.MailTransportOperation{models.MailTransportUnknown, models.MailTransportSync, models.MailTransportSend} {
		if row, err := f.repo.CreateOnce(t.Context(), f.report(operation, &old)); err != nil || row == nil {
			t.Fatalf("%s: %v %v", operation, row, err)
		}
	}
	if rows := f.errors(t, true); len(rows) != 3 {
		t.Fatalf("operation classes merged: %+v", rows)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='unknown',send_recovery_task_id=$2 WHERE id=$1`, f.account, f.task); err != nil {
		t.Fatal(err)
	}
	at := old.Add(time.Minute)
	if err := f.repo.RecordTransportSuccess(t.Context(), f.account, models.MailTransportSync, at, nil, f.generation); err != nil {
		t.Fatal(err)
	}
	if rows := f.errors(t, true); len(rows) != 2 {
		t.Fatalf("sync did not isolate scope: %+v", rows)
	} else {
		for _, row := range rows {
			if row.Operation == models.MailTransportSync {
				t.Fatal("sync remained unresolved")
			}
		}
	}
	// A completed task is not provider confirmation.
	if _, err := f.pool.Exec(t.Context(), `UPDATE tasks SET send_result_state=NULL WHERE id=$1`, f.task); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.RecordTransportSuccess(t.Context(), f.account, models.MailTransportSend, at, &f.task, f.generation); err != nil {
		t.Fatal(err)
	}
	if len(f.errors(t, true)) != 2 {
		t.Fatal("completion alone cleared a send error")
	}
	foreign := uuid.New()
	if err := f.repo.RecordTransportSuccess(t.Context(), f.account, models.MailTransportSend, at, &foreign, f.generation); err != nil {
		t.Fatal(err)
	}
	if len(f.errors(t, true)) != 2 {
		t.Fatal("foreign task cleared a send error")
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE tasks SET send_result_state='sent' WHERE id=$1`, f.task); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.RecordTransportSuccess(t.Context(), f.account, models.MailTransportSend, at, &f.task, f.generation); err != nil {
		t.Fatal(err)
	}
	rows := f.errors(t, true)
	if len(rows) != 1 || rows[0].Operation != models.MailTransportUnknown {
		t.Fatalf("send cleared unknown operation: %+v", rows)
	}
	var held bool
	if err := f.pool.QueryRow(t.Context(), `SELECT send_recovery_hold FROM email_accounts WHERE id=$1`, f.account).Scan(&held); err != nil || !held {
		t.Fatalf("report recovery cleared send hold: %v %v", held, err)
	}
	if len(f.errors(t, false)) != 3 {
		t.Fatal("recovery deleted historical rows")
	}
	batch, xerr := f.repo.GetByAccountIDs(t.Context(), []uuid.UUID{f.account}, false)
	if xerr != nil || len(batch[f.account]) != 3 {
		t.Fatal("batched report model lost history", xerr)
	}
	byUser, xerr := f.repo.GetByUserID(t.Context(), f.user, 100)
	if xerr != nil || len(byUser) != 3 {
		t.Fatal("user report model lost history", xerr)
	}
}

func TestLiveMailTransportOutOfOrderFailureStaysHistorical(t *testing.T) {
	f := newTransportFixture(t)
	old := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	success := old.Add(time.Minute)
	for _, operation := range []models.MailTransportOperation{models.MailTransportSync, models.MailTransportSend} {
		var taskID *uuid.UUID
		if operation == models.MailTransportSend {
			taskID = &f.task
		}
		if err := f.repo.RecordTransportSuccess(t.Context(), f.account, operation, success, taskID, f.generation); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if row, err := f.repo.CreateOnce(t.Context(), f.report(operation, &old)); err != nil || row != nil {
				t.Fatalf("old failure notified/reopened outage: %v %v", row, err)
			}
		}
	}
	if len(f.errors(t, true)) != 0 || len(f.errors(t, false)) != 2 {
		t.Fatal("historical failure was lost, duplicated or reopened")
	}
	newer := success.Add(time.Minute)
	row, err := f.repo.CreateOnce(t.Context(), f.report(models.MailTransportSync, &newer))
	if err != nil || row == nil {
		t.Fatal("fresh failure was swallowed", err)
	}
	if err := f.repo.RecordTransportSuccess(t.Context(), f.account, models.MailTransportSync, old, nil, f.generation); err != nil {
		t.Fatal(err)
	}
	if len(f.errors(t, true)) != 1 {
		t.Fatal("old success cleared newer failure")
	}
	if row, err := f.repo.CreateOnce(t.Context(), f.report(models.MailTransportSync, &old)); err != nil || row != nil {
		t.Fatal("old redelivery created a new warning", err)
	}
	if rows := f.errors(t, true); len(rows) != 1 || !rows[0].CreatedAt.Equal(newer) {
		t.Fatalf("older error moved latest clock: %+v", rows)
	}
	// Worker failures have second precision; this ambiguous bucket stays held.
	if err := f.repo.RecordTransportSuccess(t.Context(), f.account, models.MailTransportSync, newer.Add(500*time.Millisecond), nil, f.generation); err != nil {
		t.Fatal(err)
	}
	if len(f.errors(t, true)) != 1 {
		t.Fatal("same-second success claimed temporal authority")
	}
	if err := f.repo.RecordTransportSuccess(t.Context(), f.account, models.MailTransportSync, newer.Add(2*time.Second), nil, f.generation); err != nil {
		t.Fatal(err)
	}
	if len(f.errors(t, true)) != 0 {
		t.Fatal("newer proven success did not recover")
	}
	if row, err := f.repo.CreateOnce(t.Context(), f.report(models.MailTransportUnknown, &old)); err != nil || row == nil {
		t.Fatal("known successes guessed legacy operation ownership", err)
	}
}

func TestLiveMailTransportClockFallbackAndConcurrentRecovery(t *testing.T) {
	f := newTransportFixture(t)
	base := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			at := base.Add(time.Duration(i) * time.Minute)
			if _, err := f.repo.CreateOnce(t.Context(), f.report(models.MailTransportSync, &at)); err != nil {
				t.Error(err)
			}
			if err := f.repo.RecordTransportSuccess(t.Context(), f.account, models.MailTransportSync, base.Add(20*time.Minute), nil, f.generation); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(f.errors(t, true)) != 0 {
		t.Fatal("concurrent error/recovery left a false outage")
	}
	var recovered time.Time
	if err := f.pool.QueryRow(t.Context(), `SELECT recovered_at FROM email_account_transport_recovery WHERE email_account_id=$1 AND operation='sync'`, f.account).Scan(&recovered); err != nil || !recovered.Equal(base.Add(20*time.Minute)) {
		t.Fatalf("success watermark regressed: %v %v", recovered, err)
	}
	zero := time.Time{}
	future := time.Now().Add(time.Hour)
	negative := time.Unix(-1, 0)
	for _, invalid := range []*time.Time{nil, &zero, &future, &negative} {
		before := time.Now()
		if _, err := f.repo.CreateOnce(t.Context(), f.report(models.MailTransportSync, invalid)); err != nil {
			t.Fatal(err)
		}
		rows := f.errors(t, true)
		if len(rows) != 1 || rows[0].CreatedAt.Before(before) || rows[0].ObservationSource != "receipt" || rows[0].ReceivedAt == nil {
			t.Fatalf("invalid worker time was not receipt-scoped: %+v", rows)
		}
	}
	current := f.errors(t, true)[0]
	if _, err := f.repo.CreateOnce(t.Context(), f.report(models.MailTransportSync, &base)); err != nil {
		t.Fatal(err)
	}
	if row := f.errors(t, true)[0]; !row.CreatedAt.Equal(current.CreatedAt) || row.ObservationSource != "receipt" {
		t.Fatalf("older delivery changed latest provenance: %+v", row)
	}
	for _, at := range []time.Time{zero, future, negative} {
		if err := f.repo.RecordTransportSuccess(t.Context(), f.account, models.MailTransportSync, at, nil, f.generation); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.errors(t, true)) != 1 {
		t.Fatal("invalid success clock cleared receipt-scoped failure")
	}
}

func TestLiveMailTransportRecoveryJoinsResultTransaction(t *testing.T) {
	f := newTransportFixture(t)
	old := time.Now().Add(-time.Hour)
	if _, err := f.repo.CreateOnce(t.Context(), f.report(models.MailTransportSend, &old)); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	ctx := context.WithValue(t.Context(), sendResultKey{}, sendResultContext{tx: tx, taskID: f.task})
	if err := f.repo.RecordTransportSuccess(ctx, f.account, models.MailTransportSend, old.Add(time.Minute), &f.task, f.generation); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.errors(t, true)) != 1 {
		t.Fatal("recovery escaped failed send-result transaction")
	}
	var n int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM email_account_transport_recovery WHERE email_account_id=$1`, f.account).Scan(&n); err != nil || n != 0 {
		t.Fatal("recovery watermark survived rollback", err)
	}
}

func TestLiveMailTransportSyncTimestampIsMonotonic(t *testing.T) {
	f := newTransportFixture(t)
	repo := NewEmailSyncStateRepository(f.db)
	newer := time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)
	older := newer.Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	zero := time.Time{}
	for _, at := range []*time.Time{&newer, &older, nil, &future, &zero} {
		if err := repo.Put(t.Context(), f.user, f.account, &models.SyncState{LastSyncedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	state, err := repo.Get(t.Context(), f.account)
	if err != nil || state.LastSyncedAt == nil || !state.LastSyncedAt.Equal(newer) {
		t.Fatalf("older redelivery regressed monitoring clock: %+v %v", state, err)
	}
	var accountAt time.Time
	if err := f.pool.QueryRow(t.Context(), `SELECT last_synced_at FROM email_accounts WHERE id=$1`, f.account).Scan(&accountAt); err != nil || !accountAt.Equal(newer) {
		t.Fatalf("account/table clocks diverged: %v %v", accountAt, err)
	}
}
