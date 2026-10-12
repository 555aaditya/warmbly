package repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/models"
)

func TestLiveMailTransportImportSerializesWithConfirmedResult(t *testing.T) {
	f := newTransportFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := f.pool.Exec(ctx, `UPDATE tasks SET send_result_state=NULL WHERE id=$1`, f.task); err != nil {
		t.Fatal(err)
	}
	recovery := NewTaskRepository(f.pool).(SendResultRecovery)
	ready, proceed := make(chan struct{}, 1), make(chan struct{})
	resultDone, importDone := make(chan error, 1), make(chan error, 1)
	at := time.Now().UTC().Add(-time.Hour)
	go func() {
		resultDone <- recovery.ApplySendResult(ctx, models.SendEmailResult{TaskID: f.task, Success: true, SentAt: at}, func(ctx context.Context) error {
			ready <- struct{}{}
			select {
			case <-proceed:
			case <-ctx.Done():
				return ctx.Err()
			}
			if err := f.repo.RecordTransportSuccess(ctx, f.account, models.MailTransportSend, at, &f.task, f.generation); err != nil {
				return err
			}
			return nil
		})
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() {
		tx, err := f.pool.Begin(ctx)
		if err != nil {
			importDone <- err
			return
		}
		defer tx.Rollback(context.Background())
		var row json.RawMessage
		if err := f.pool.QueryRow(ctx, `SELECT to_jsonb(ea) || '{"name":"Restored"}'::jsonb FROM email_accounts ea WHERE id=$1`, f.account).Scan(&row); err != nil {
			importDone <- err
			return
		}
		written, err := NewOrgTransferRepository(f.db).InsertBatch(ctx, tx, "email_accounts", []string{"id", "user_id", "organization_id", "email", "name", "signature_plain", "signature_html", "provider"}, []json.RawMessage{row}, models.OrgImportConflictOverwrite, []string{"id"}, "id=$1", f.account, nil,
			[]ImportInvalidation{{Table: "email_account_transport_recovery", Column: "email_account_id", Key: "id", LockNamespaces: []int64{SendResultMailboxLockNamespace, MailTransportLockNamespace}}})
		if err == nil && written != 1 {
			err = errors.New("wrong actual import count")
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		importDone <- err
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory' AND query LIKE '%hashtextextended(keys.key%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("import did not wait for the native result's mailbox lock", ctx.Err())
		}
	}
	close(proceed)
	for _, done := range []<-chan error{resultDone, importDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("result/import error: %T %#v (%v)", err, err, err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	var confirmed bool
	var clocks int
	if err := f.pool.QueryRow(ctx, `SELECT send_result_state='sent' AND send_result_applied_at IS NOT NULL FROM tasks WHERE id=$1`, f.task).Scan(&confirmed); err != nil || !confirmed {
		t.Fatal("import erased confirmed task audit", err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM email_account_transport_recovery WHERE email_account_id=$1`, f.account).Scan(&clocks); err != nil || clocks != 0 {
		t.Fatal("result/import retained prior derived clock", clocks, err)
	}
}
