package repository

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

var recoverableTransportCodes = []string{"SERVER_UNREACHABLE", "CONNECTION_LOST", "RESOURCE_NOT_FOUND", "IMAP_UNKNOWN", "SENDING_TOO_FAST", "QUOTA_EXCEEDED"}

func TestLiveMailTransportLegacyRecoveryRequiresBothOperations(t *testing.T) {
	for _, first := range []models.MailTransportOperation{models.MailTransportSync, models.MailTransportSend} {
		t.Run(string(first), func(t *testing.T) {
			f := newTransportFixture(t)
			at := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			codes := append(append([]string{}, recoverableTransportCodes...), "INVALID_CREDENTIALS", "AUTHENTICATION_FAILED", "DOMAIN_AUTH_REJECTED", "SYNC_FLOOD", "SYNC_FAIR_USE", "RATE_LIMIT_EXCEEDED")
			for _, code := range codes {
				if _, err := f.pool.Exec(t.Context(), `INSERT INTO email_account_errors(id,email_account_id,user_id,error_code,severity,resolve_method,title,message,created_at)
				    VALUES($1,$2,$3,$4,'WARNING','RETRY','Legacy','Legacy report',$5)`, uuid.New(), f.account, f.user, code, at); err != nil {
					t.Fatal(err)
				}
			}
			success := func(op models.MailTransportOperation, clock time.Time, generation uuid.UUID) {
				t.Helper()
				var task *uuid.UUID
				if op == models.MailTransportSend {
					task = &f.task
				}
				if err := f.repo.RecordTransportSuccess(t.Context(), f.account, op, clock, task, generation); err != nil {
					t.Fatal(err)
				}
			}
			second := models.MailTransportSend
			if first == second {
				second = models.MailTransportSync
			}
			success(first, at.Add(time.Minute), f.generation)
			success(second, at.Add(time.Minute), uuid.New())
			success(second, at, f.generation)
			if len(f.errors(t, true)) != len(codes) {
				t.Fatal("one transport, an old success, or a foreign generation cleared legacy failures")
			}
			success(second, at.Add(2*time.Minute), f.generation)
			if rows := f.errors(t, true); len(rows) != len(codes)-len(recoverableTransportCodes) {
				t.Fatalf("legacy temporary reports did not recover or actionable reports were hidden: %+v", rows)
			}
			for _, row := range f.errors(t, false) {
				if (row.ResolvedAt != nil) != slices.Contains(recoverableTransportCodes, row.ErrorCode) {
					t.Fatal("wrong error category recovered", row)
				}
				if row.Operation != models.MailTransportUnknown || row.ObservationSource != "unknown" || !row.CreatedAt.Equal(at) || row.ReceivedAt != nil {
					t.Fatal("recovery rewrote historical evidence", row)
				}
			}
		})
	}
}

func TestLiveMailTransportUnknownDelayedAndFreshFailures(t *testing.T) {
	f := newTransportFixture(t)
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	for _, op := range []models.MailTransportOperation{models.MailTransportSync, models.MailTransportSend} {
		var task *uuid.UUID
		if op == models.MailTransportSend {
			task = &f.task
		}
		if err := f.repo.RecordTransportSuccess(t.Context(), f.account, op, at.Add(time.Minute), task, f.generation); err != nil {
			t.Fatal(err)
		}
	}
	for _, code := range recoverableTransportCodes {
		for range 2 {
			d := f.report(models.MailTransportUnknown, &at)
			d.ErrorCode = code
			if row, err := f.repo.CreateOnce(t.Context(), d); err != nil || row != nil {
				t.Fatal("late recovered report reopened or notified an outage", row, err)
			}
		}
	}
	if len(f.errors(t, true)) != 0 || len(f.errors(t, false)) != len(recoverableTransportCodes) {
		t.Fatal("late reports were duplicated, lost, or left unresolved")
	}
	fresh := at.Add(2 * time.Minute)
	for _, clock := range []*time.Time{&fresh, nil} {
		if row, err := f.repo.CreateOnce(t.Context(), f.report(models.MailTransportUnknown, clock)); err != nil || clock != nil && row == nil {
			t.Fatal("a newer or receipt-scoped failure disappeared", row, err)
		}
	}
	if rows := f.errors(t, true); len(rows) != 1 || rows[0].ObservationSource != "receipt" {
		t.Fatal("old recovery cleared a fresh failure", rows)
	}
}

func TestLiveMailTransportProviderThrottleRecoveryIsOperationScoped(t *testing.T) {
	for _, code := range []string{"SENDING_TOO_FAST", "QUOTA_EXCEEDED"} {
		t.Run(code, func(t *testing.T) {
			f := newTransportFixture(t)
			at := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			for _, op := range []models.MailTransportOperation{models.MailTransportSync, models.MailTransportSend} {
				d := f.report(op, &at)
				d.ErrorCode = code
				if _, err := f.repo.CreateOnce(t.Context(), d); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.repo.RecordTransportSuccess(t.Context(), f.account, models.MailTransportSync, at.Add(time.Minute), nil, f.generation); err != nil {
				t.Fatal(err)
			}
			if rows := f.errors(t, true); len(rows) != 1 || rows[0].Operation != models.MailTransportSend {
				t.Fatal("sync success cleared a sending limit or failed to clear a sync limit", rows)
			}
			if err := f.repo.RecordTransportSuccess(t.Context(), f.account, models.MailTransportSend, at.Add(2*time.Minute), &f.task, f.generation); err != nil {
				t.Fatal(err)
			}
			if len(f.errors(t, true)) != 0 {
				t.Fatal("confirmed send left a provider throttle unresolved")
			}
		})
	}
}

func TestLiveMailTransportLegacyRecoveryMigration(t *testing.T) {
	f := newTransportFixture(t)
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	for _, op := range []models.MailTransportOperation{models.MailTransportSync, models.MailTransportSend} {
		var task *uuid.UUID
		if op == models.MailTransportSend {
			task = &f.task
		}
		if err := f.repo.RecordTransportSuccess(t.Context(), f.account, op, at.Add(time.Minute), task, f.generation); err != nil {
			t.Fatal(err)
		}
	}
	for _, code := range append(append([]string{}, recoverableTransportCodes...), "INVALID_CREDENTIALS") {
		for _, clock := range []time.Time{at, at.Add(2 * time.Minute), time.Unix(0, 0)} {
			if _, err := f.pool.Exec(t.Context(), `INSERT INTO email_account_errors(id,email_account_id,user_id,error_code,severity,resolve_method,title,message,created_at)
			    VALUES($1,$2,$3,$4,'WARNING','RETRY','Legacy','Legacy report',$5)`, uuid.New(), f.account, f.user, code, clock); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Emulate provider-limit rows left unresolved by the previous recovery implementation.
	for _, op := range []models.MailTransportOperation{models.MailTransportSync, models.MailTransportSend} {
		if _, err := f.pool.Exec(t.Context(), `INSERT INTO email_account_errors(id,email_account_id,user_id,error_code,severity,resolve_method,title,message,created_at,operation,observation_source)
		    VALUES($1,$2,$3,'SENDING_TOO_FAST','WARNING','RETRY','Throttle','Throttle report',$4,$5,'worker')`, uuid.New(), f.account, f.user, at, op); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile("../infrastructure/db/migrations/000313_recovered_legacy_mailbox_errors.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	query := strings.ReplaceAll(strings.ReplaceAll(string(data), "BEGIN;", ""), "COMMIT;", "")
	for range 2 {
		if _, err := tx.Exec(t.Context(), query); err != nil {
			t.Fatal(err)
		}
		var resolved, incorrect int
		if err := tx.QueryRow(t.Context(), `SELECT COUNT(*) FILTER(WHERE resolved_at IS NOT NULL AND operation='unknown'),
		    COUNT(*) FILTER(WHERE resolved_at IS NOT NULL AND operation='unknown' AND (created_at<>$2 OR error_code='INVALID_CREDENTIALS' OR observation_source<>'unknown'))
		    FROM email_account_errors WHERE email_account_id=$1`, f.account, at).Scan(&resolved, &incorrect); err != nil || resolved != len(recoverableTransportCodes) || incorrect != 0 {
			t.Fatalf("migration lost ordering, provenance or authentication protection: resolved=%d incorrect=%d err=%v", resolved, incorrect, err)
		}
		var scoped int
		if err := tx.QueryRow(t.Context(), `SELECT COUNT(*) FROM email_account_errors WHERE email_account_id=$1 AND operation IN ('send','sync') AND resolved_at IS NOT NULL`, f.account).Scan(&scoped); err != nil || scoped != 2 {
			t.Fatal("migration left recovered provider limits active", scoped, err)
		}
	}
}
