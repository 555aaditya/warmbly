package orgtransfer

import (
	"github.com/warmbly/warmbly/internal/models"
	"testing"
	"time"
)

func TestLiveImportMailTransportCredentialOnlyWritesRotateFence(t *testing.T) {
	for _, table := range []string{"email_accounts_oauth", "email_accounts_smtp_imap"} {
		for _, conflict := range []models.OrgImportConflict{models.OrgImportConflictSkip, models.OrgImportConflictOverwrite} {
			t.Run(table+"/"+string(conflict), func(t *testing.T) {
				svc, pool, f, mailbox, _, _ := overwriteTransportFixture(t)
				row := map[string]any{"email_account_id": mailbox}
				switch table {
				case "email_accounts_oauth":
					row["access_token"], row["refresh_token"], row["expires_at"] = "new-access", "new-refresh", time.Now().Add(time.Hour)
					if _, err := pool.Exec(t.Context(), `INSERT INTO email_accounts_oauth(email_account_id,access_token,refresh_token,expires_at) VALUES($1,'old','old',NOW())`, mailbox); err != nil {
						t.Fatal(err)
					}
				case "email_accounts_smtp_imap":
					for _, c := range []string{"smtp_host", "smtp_user", "smtp_password", "imap_host", "imap_user", "imap_password"} {
						row[c] = "fixture"
					}
					row["smtp_port"], row["imap_port"] = 587, 993
					if _, err := pool.Exec(t.Context(), `INSERT INTO email_accounts_smtp_imap(email_account_id,smtp_host,smtp_port,smtp_user,smtp_password,imap_host,imap_port,imap_user,imap_password) VALUES($1,'old',25,'old','old','old',143,'old','old')`, mailbox); err != nil {
						t.Fatal(err)
					}
				}
				before := importTransportGeneration(t, pool, mailbox)
				if _, err := pool.Exec(t.Context(), `INSERT INTO email_account_transport_recovery(email_account_id,operation,recovered_at) VALUES($1,'sync',NOW()-INTERVAL '1 hour')`, mailbox); err != nil {
					t.Fatal(err)
				}
				result, err := svc.ImportFrom(t.Context(), f.org, tenantArchive(t, map[string][]map[string]any{table: {row}}), ImportOptions{Conflict: conflict, ActorUserID: f.owner}, nil)
				if err != nil {
					t.Fatal(err)
				}
				var clocks int
				if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM email_account_transport_recovery WHERE email_account_id=$1`, mailbox).Scan(&clocks); err != nil {
					t.Fatal(err)
				}
				current := importTransportGeneration(t, pool, mailbox)
				if conflict == models.OrgImportConflictSkip {
					if current != before || clocks != 1 || result.RowCounts[table] != 0 {
						t.Fatal("skipped credentials changed fence/count/clock")
					}
				} else if current == before || clocks != 0 || result.RowCounts[table] != 1 || result.RowCounts["email_accounts"] != 0 {
					t.Fatal("actual credential-only write did not rotate/invalidate accurately")
				}
			})
		}
	}
}
