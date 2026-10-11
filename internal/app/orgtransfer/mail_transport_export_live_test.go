package orgtransfer

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/encrypt"
)

func TestLiveImportMailTransportExportOmitsGeneration(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		for _, conflict := range []models.OrgImportConflict{models.OrgImportConflictSkip, models.OrgImportConflictOverwrite} {
			label := "plaintext/" + string(conflict)
			if encrypted {
				label = "encrypted/" + string(conflict)
			}
			t.Run(label, func(t *testing.T) {
				svc, pool, f, mailbox, task, _ := overwriteTransportFixture(t)
				native := svc.(*service)
				enc, err := encrypt.NewEncrypter(make([]byte, 32))
				if err != nil {
					t.Fatal(err)
				}
				native.creds = enc
				password, err := enc.Encrypt("local-export-password")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), `INSERT INTO email_accounts_smtp_imap(email_account_id,smtp_host,smtp_port,smtp_user,smtp_password,imap_host,imap_port,imap_user,imap_password) VALUES($1,'smtp.test',587,'fixture',$2,'imap.test',993,'fixture',$2)`, mailbox, password); err != nil {
					t.Fatal(err)
				}
				source := importTransportGeneration(t, pool, mailbox)
				if _, err := pool.Exec(t.Context(), `UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='authentication',send_recovery_task_id=$2 WHERE id=$1`, mailbox, task); err != nil {
					t.Fatal(err)
				}
				groups := []models.OrgDataGroup{models.OrgDataGroupCore, models.OrgDataGroupSending}
				passphrase := ""
				if encrypted {
					passphrase = "local generation export test passphrase"
				}
				var archive bytes.Buffer
				manifest, err := svc.ExportTo(t.Context(), f.org, ExportOptions{Groups: groups, Passphrase: passphrase}, &archive, nil)
				if err != nil {
					t.Fatal(err)
				}
				if (manifest.Secrets != nil) != encrypted {
					t.Fatal("wrong archive secrets mode")
				}
				zr, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
				if err != nil {
					t.Fatal(err)
				}
				entries := indexEntries(zr)
				stored, err := readManifest(entries)
				if err != nil {
					t.Fatal(err)
				}
				for _, m := range []*Manifest{manifest, stored} {
					tables := manifestTables(m)
					mt, ok := tables["email_accounts"]
					if !ok || mt.Rows != 1 || slices.Contains(mt.Columns, "transport_generation") || !slices.Contains(mt.Columns, "name") || !slices.Contains(mt.Columns, "worker_id") || !slices.Contains(mt.Columns, "auth_checked_at") {
						t.Fatal("manifest lost portable columns or exported generation")
					}
					raw, err := json.Marshal(m)
					if err != nil {
						t.Fatal(err)
					}
					if bytes.Contains(raw, []byte("transport_generation")) || bytes.Contains(raw, []byte(source.String())) {
						t.Fatal("manifest leaked generation")
					}
				}
				entry := entries[dataPath("email_accounts")]
				if entry == nil {
					t.Fatal("mailbox data missing")
				}
				r, err := entry.Open()
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(r)
				r.Close()
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(data, []byte("transport_generation")) || bytes.Contains(data, []byte(source.String())) {
					t.Fatal("exported row leaked source generation")
				}
				var row map[string]json.RawMessage
				if err := json.Unmarshal(bytes.TrimSpace(data), &row); err != nil {
					t.Fatal(err)
				}
				if jsonString(row["id"]) != mailbox.String() || jsonString(row["name"]) != "Local" || jsonString(row["provider"]) != "smtp_imap" {
					t.Fatal("ordinary row columns changed")
				}
				if importTransportGeneration(t, pool, mailbox) != source {
					t.Fatal("export mutated source fence")
				}
				destination := uuid.New()
				if _, err := pool.Exec(t.Context(), `UPDATE email_accounts SET transport_generation=$2,name='Destination',send_recovery_hold=true,send_recovery_reason='authentication',send_recovery_task_id=$3 WHERE id=$1`, mailbox, destination, task); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), `INSERT INTO email_account_transport_recovery(email_account_id,operation,recovered_at) VALUES($1,'sync',NOW()-INTERVAL '1 hour'),($1,'send',NOW()-INTERVAL '1 hour')`, mailbox); err != nil {
					t.Fatal(err)
				}
				imported, err := svc.ImportFrom(t.Context(), f.org, bytes.NewReader(archive.Bytes()), ImportOptions{Groups: groups, Passphrase: passphrase, Conflict: conflict, ActorUserID: f.owner}, nil)
				if err != nil {
					t.Fatal(err)
				}
				current := importTransportGeneration(t, pool, mailbox)
				var clocks int
				var hold bool
				var name string
				if err := pool.QueryRow(t.Context(), `SELECT (SELECT COUNT(*) FROM email_account_transport_recovery WHERE email_account_id=$1),send_recovery_hold AND send_recovery_reason='authentication' AND send_recovery_task_id=$2,name FROM email_accounts WHERE id=$1`, mailbox, task).Scan(&clocks, &hold, &name); err != nil {
					t.Fatal(err)
				}
				if !hold {
					t.Fatal("portable export/import changed restrictive hold")
				}
				if conflict == models.OrgImportConflictSkip {
					if current != destination || clocks != 2 || name != "Destination" || imported.RowCounts["email_accounts"] != 0 {
						t.Fatal("skip changed destination fence/clock/count")
					}
				} else {
					if current == source || current == destination || current == uuid.Nil || clocks != 0 || name != "Local" || imported.RowCounts["email_accounts"] != 1 {
						t.Fatal("overwrite did not establish independent destination fence/clock/count")
					}
					if encrypted {
						var restored string
						if err := pool.QueryRow(t.Context(), `SELECT smtp_password FROM email_accounts_smtp_imap WHERE email_account_id=$1`, mailbox).Scan(&restored); err != nil {
							t.Fatal(err)
						}
						plain, err := enc.Decrypt(restored)
						if err != nil || plain != "local-export-password" {
							t.Fatal("encrypted credentials no longer portable", err)
						}
					}
				}
			})
		}
	}
}
