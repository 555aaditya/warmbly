package orgtransfer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

func TestLiveImportMailTransportCheckpointCountsAndUnchangedRows(t *testing.T) {
	for _, conflict := range []models.OrgImportConflict{models.OrgImportConflictSkip, models.OrgImportConflictOverwrite} {
		t.Run(string(conflict), func(t *testing.T) {
			svc, pool, f, mailbox, task, repo := overwriteTransportFixture(t)
			generationBefore := importTransportGeneration(t, pool, mailbox)
			other, inserted := uuid.New(), uuid.New()
			for _, id := range []uuid.UUID{other} {
				if _, err := pool.Exec(t.Context(), `INSERT INTO email_accounts(id,user_id,organization_id,email,name,signature_plain,signature_html,provider) VALUES($1,$2,$3,$4,'Unchanged','','','smtp_imap')`, id, f.owner, f.org, id.String()+"@test.local"); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				_, _ = pool.Exec(context.Background(), `DELETE FROM email_accounts WHERE id=ANY($1::uuid[])`, []uuid.UUID{other, inserted})
			})
			at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
			if _, err := pool.Exec(t.Context(), `INSERT INTO email_account_transport_recovery(email_account_id,operation,recovered_at) SELECT a,op,$2 FROM unnest($1::uuid[]) a CROSS JOIN unnest(ARRAY['sync','send']) op`, []uuid.UUID{mailbox, other}, at); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), `UPDATE email_accounts SET send_recovery_hold=true,send_recovery_task_id=$2,send_recovery_reason='unknown',auth_checked_at=$3 WHERE id=$1`, mailbox, task, at); err != nil {
				t.Fatal(err)
			}
			row := func(id uuid.UUID) map[string]any {
				return map[string]any{"id": id, "user_id": f.owner, "organization_id": f.org, "email": "restored-" + id.String() + "@test.local", "name": "Restored", "signature_plain": "", "signature_html": "", "provider": "smtp_imap"}
			}
			archive := tenantArchive(t, map[string][]map[string]any{"email_accounts": {row(mailbox), row(inserted)}})
			for pass := range 2 {
				result, err := svc.ImportFrom(t.Context(), f.org, archive, ImportOptions{Conflict: conflict, ActorUserID: f.owner}, nil)
				if err != nil {
					t.Fatal(err)
				}
				want := int64(2)
				if conflict == models.OrgImportConflictSkip {
					want = int64(1 - pass)
				}
				if result.RowCounts["email_accounts"] != want {
					t.Fatal("invalidation corrupted actual import count", result.RowCounts, want)
				}
				current := importTransportGeneration(t, pool, mailbox)
				if (current == generationBefore) != (conflict == models.OrgImportConflictSkip) {
					t.Fatal("generation reset did not follow actual overwrite")
				}
				generationBefore = current
				var kept, affected int
				if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FILTER (WHERE email_account_id=$1),COUNT(*) FILTER (WHERE email_account_id=$2) FROM email_account_transport_recovery`, other, mailbox).Scan(&kept, &affected); err != nil || kept != 2 || affected != map[bool]int{true: 2, false: 0}[conflict == models.OrgImportConflictSkip] {
					t.Fatal("affected/unaffected checkpoint selection", kept, affected, err)
				}
			}
			var held, taskPreserved bool
			var checked time.Time
			if err := pool.QueryRow(t.Context(), `SELECT send_recovery_hold,send_recovery_task_id=$2,auth_checked_at FROM email_accounts WHERE id=$1`, mailbox, task).Scan(&held, &taskPreserved, &checked); err != nil || !held || !taskPreserved || !checked.Equal(at) {
				t.Fatal("import changed unrelated hold/auth state", err)
			}
			if err := repo.RecordTransportSuccess(t.Context(), mailbox, models.MailTransportSend, at.Add(time.Minute), &task, importTransportGeneration(t, pool, mailbox)); err != nil {
				t.Fatal("confirmed source task audit lost", err)
			}
		})
	}
}

func TestLiveImportMailTransportWaitsForPriorRecovery(t *testing.T) {
	svc, pool, f, mailbox, task, _ := overwriteTransportFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,285))`, mailbox.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO email_account_transport_recovery(email_account_id,operation,recovered_at,source_task_id) VALUES($1,'send',NOW()-INTERVAL '1 hour',$2)`, mailbox, task); err != nil {
		t.Fatal(err)
	}
	archive := tenantArchive(t, map[string][]map[string]any{"email_accounts": {{"id": mailbox, "user_id": f.owner, "organization_id": f.org, "email": mailbox.String() + "@test.local", "name": "Restored", "signature_plain": "", "signature_html": "", "provider": "smtp_imap"}}})
	started, finished := make(chan struct{}, 1), make(chan error, 1)
	go func() {
		result, err := svc.ImportFrom(ctx, f.org, archive, ImportOptions{Conflict: models.OrgImportConflictOverwrite, ActorUserID: f.owner}, func(_ int, stage string) {
			if strings.Contains(stage, "importing email_accounts") {
				started <- struct{}{}
			}
		})
		if err == nil && result.RowCounts["email_accounts"] != 1 {
			err = errors.New("import count changed")
		}
		finished <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory' AND query LIKE '%hashtextextended(keys.key%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("import did not serialize with prior success", ctx.Err())
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var clocks int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM email_account_transport_recovery WHERE email_account_id=$1`, mailbox).Scan(&clocks); err != nil || clocks != 0 {
		t.Fatal("concurrently committed prior clock escaped invalidation", clocks, err)
	}
}

func TestLiveImportMailTransportCheckpointForeignAndRollback(t *testing.T) {
	for _, late := range []bool{false, true} {
		label := "foreign_mailbox"
		if late {
			label = "later_failure_rolls_back"
		}
		t.Run(label, func(t *testing.T) {
			svc, pool, f, mailbox, _, _ := overwriteTransportFixture(t)
			generationBefore := importTransportGeneration(t, pool, mailbox)
			foreign := newTenantFixture(t, pool)
			at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
			if _, err := pool.Exec(t.Context(), `INSERT INTO email_account_transport_recovery(email_account_id,operation,recovered_at) VALUES($1,'sync',$2),($1,'send',$2)`, mailbox, at); err != nil {
				t.Fatal(err)
			}
			row := map[string]any{"id": mailbox, "user_id": f.owner, "organization_id": f.org, "name": "Attempted restore", "email": mailbox.String() + "@test.local", "signature_plain": "", "signature_html": "", "provider": "smtp_imap"}
			data := map[string][]map[string]any{"email_accounts": {row}}
			if late {
				data["contacts"] = []map[string]any{{"id": foreign.contact, "organization_id": f.org, "email": "foreign@test.local"}}
			} else {
				foreignMailbox := uuid.New()
				if _, err := pool.Exec(t.Context(), `INSERT INTO email_accounts(id,user_id,organization_id,email,name,signature_plain,signature_html,provider) VALUES($1,$2,$3,$4,'Foreign','','','smtp_imap')`, foreignMailbox, foreign.owner, foreign.org, foreignMailbox.String()+"@test.local"); err != nil {
					t.Fatal(err)
				}
				row["id"] = foreignMailbox
			}
			if _, err := svc.ImportFrom(t.Context(), f.org, tenantArchive(t, data), ImportOptions{Conflict: models.OrgImportConflictOverwrite, ActorUserID: f.owner}, nil); !errors.Is(err, ErrForeignRows) {
				t.Fatal("foreign import accepted", err)
			}
			var clocks int
			var name string
			if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM email_account_transport_recovery WHERE email_account_id=$1 AND recovered_at=$2`, mailbox, at).Scan(&clocks); err != nil || clocks != 2 {
				t.Fatal("rejected/rolled-back import lost clocks", clocks, err)
			}
			if err := pool.QueryRow(t.Context(), `SELECT name FROM email_accounts WHERE id=$1`, mailbox).Scan(&name); err != nil || name != "Local" {
				t.Fatal("rejected/rolled-back import changed mailbox", name, err)
			}
			if importTransportGeneration(t, pool, mailbox) != generationBefore {
				t.Fatal("foreign/rolled-back import rotated generation")
			}
		})
	}
}
