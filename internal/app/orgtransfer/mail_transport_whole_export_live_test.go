package orgtransfer

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func assertExportJSONEqual(t *testing.T, actual, expected []byte) {
	t.Helper()
	var a, b any
	for _, item := range []struct {
		raw []byte
		out *any
	}{{actual, &a}, {expected, &b}} {
		decoder := json.NewDecoder(bytes.NewReader(item.raw))
		decoder.UseNumber()
		if err := decoder.Decode(item.out); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("portable evidence changed", a, b)
	}
}

func TestLiveImportMailTransportWholeArchiveOmitsGeneration(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		for _, sent := range []bool{false, true} {
			label := "plaintext"
			if encrypted {
				label = "encrypted"
			}
			if sent {
				label += "/sent"
			} else {
				label += "/failed"
			}
			t.Run(label, func(t *testing.T) {
				svc, pool, f, mailbox, task, reports := overwriteTransportFixture(t)
				source := importTransportGeneration(t, pool, mailbox)
				now := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
				normal := models.SendEmailResult{TransportGeneration: source, TaskID: task, Success: sent, MessageID: "accepted@fixture.test", ProviderMsgID: "provider-proof", ThreadID: "conversation-proof", SentAt: now, LegacyErrorMsg: "preserved historical text"}
				if !sent {
					normal.Error = &models.EmailSendError{Code: "RECIPIENT_REJECTED", Type: "permanent", Message: "Preserved failure evidence"}
				}
				raw, err := json.Marshal(normal)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), `UPDATE tasks SET send_executor_result=$2 WHERE id=$1`, task, raw); err != nil {
					t.Fatal(err)
				}
				warmTask := uuid.New()
				warm := normal
				warm.TaskID = warmTask
				warm.ProviderMsgID = "warm-provider-proof"
				warmRaw, err := json.Marshal(warm)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), `INSERT INTO tasks(id,email_account_id,task_type,status,message_id,send_result_state) VALUES($1,$2,'warmup','completed','warm-message','sent')`, warmTask, mailbox); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), `INSERT INTO warmup_tasks(task_id,dispatch_result) VALUES($1,$2)`, warmTask, warmRaw); err != nil {
					t.Fatal(err)
				}
				audit := uuid.New()
				evidence := "operator_confirmed_sent"
				if !sent {
					evidence = "operator_confirmed_not_sent"
				}
				if _, err := pool.Exec(t.Context(), `INSERT INTO send_recovery_resolutions(id,organization_id,email_account_id,recovery_task_id,previous_reason,evidence_type,confirmation_reference,confirmed_result,recorded_locally_at) VALUES($1,$2,$3,$4,'unknown',$5,'Preserved source confirmation',$6,NOW())`, audit, f.org, mailbox, task, evidence, raw); err != nil {
					t.Fatal(err)
				}
				syncRepo := repository.NewEmailSyncStateRepository(&db.DB{Pool: pool})
				state := models.SyncState{TransportGeneration: source, LastSyncedAt: &now, BackfillStatus: models.SyncBackfillComplete, BackfillCursor: models.SyncCursor{PageToken: "source-cursor"}, BackfillSynced: 17}
				if err := syncRepo.Put(t.Context(), f.owner, mailbox, &state); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					_, _ = pool.Exec(context.Background(), `DELETE FROM email_sync_state WHERE email_id=$1`, mailbox)
				})
				persisted, err := syncRepo.Get(t.Context(), mailbox)
				if err != nil || persisted == nil || persisted.TransportGeneration != uuid.Nil || persisted.BackfillSynced != 17 {
					t.Fatal("sync persistence inventory changed", err)
				}
				if _, err := reports.CreateOnce(t.Context(), &repository.CreateEmailAccountError{EmailAccountID: mailbox, UserID: f.owner, TaskID: &task, TransportGeneration: source, Operation: models.MailTransportSend, ErrorCode: "SERVER_UNREACHABLE", Severity: "WARNING", ResolveMethod: "RETRY", Title: "Preserved transport observation", Message: "Preserved report evidence", ObservedAt: &now}); err != nil {
					t.Fatal(err)
				}
				passphrase := ""
				if encrypted {
					passphrase = "whole archive export regression passphrase"
				}
				var archive bytes.Buffer
				manifest, err := svc.ExportTo(t.Context(), f.org, ExportOptions{Passphrase: passphrase}, &archive, nil)
				if err != nil {
					t.Fatal(err)
				}
				if (manifest.Secrets != nil) != encrypted {
					t.Fatal("wrong export mode")
				}
				if len(manifest.Groups) != len(models.AllOrgDataGroups) {
					t.Fatal("not a whole archive")
				}
				zr, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
				if err != nil {
					t.Fatal(err)
				}
				entries := indexEntries(zr)
				for _, entry := range zr.File {
					reader, err := entry.Open()
					if err != nil {
						t.Fatal(err)
					}
					data, err := io.ReadAll(reader)
					reader.Close()
					if err != nil {
						t.Fatal(err)
					}
					if bytes.Contains(data, []byte(source.String())) || bytes.Contains(data, []byte("transport_generation")) {
						t.Errorf("generation leaked into whole-archive entry %s", entry.Name)
					}
				}
				manifestRaw, err := json.Marshal(manifest)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(manifestRaw, []byte(source.String())) || bytes.Contains(manifestRaw, []byte("transport_generation")) {
					t.Fatal("returned manifest leaked generation")
				}
				if entries[dataPath("email_sync_state")] != nil || entries[dataPath("email_account_transport_recovery")] != nil {
					t.Fatal("instance-local sync/recovery checkpoint exported")
				}
				stored, err := readManifest(entries)
				if err != nil {
					t.Fatal(err)
				}
				for _, m := range []*Manifest{manifest, stored} {
					for table, want := range map[string]int64{"tasks": 2, "warmup_tasks": 1, "send_recovery_resolutions": 1, "email_account_errors": 1} {
						mt, ok := manifestTables(m)[table]
						if !ok || mt.Rows != want {
							t.Fatal("whole export lost evidence rows", table, mt.Rows)
						}
					}
				}
				for _, item := range []struct {
					table, column string
					result        []byte
				}{{"tasks", "send_executor_result", raw}, {"warmup_tasks", "dispatch_result", warmRaw}, {"send_recovery_resolutions", "confirmed_result", raw}} {
					reader, err := entries[dataPath(item.table)].Open()
					if err != nil {
						t.Fatal(err)
					}
					decoder := json.NewDecoder(reader)
					found := false
					for {
						var row map[string]json.RawMessage
						err := decoder.Decode(&row)
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Fatal(err)
						}
						value := row[item.column]
						if len(value) == 0 || bytes.Equal(value, []byte("null")) {
							continue
						}
						var expected map[string]json.RawMessage
						if err := json.Unmarshal(item.result, &expected); err != nil {
							t.Fatal(err)
						}
						delete(expected, "transport_generation")
						portable, err := json.Marshal(expected)
						if err != nil {
							t.Fatal(err)
						}
						assertExportJSONEqual(t, value, portable)
						found = true
					}
					reader.Close()
					if !found {
						t.Fatal("result envelope was dropped", item.table)
					}
				}
				for _, item := range []struct {
					query    string
					id       uuid.UUID
					expected []byte
				}{
					{`SELECT send_executor_result FROM tasks WHERE id=$1`, task, raw},
					{`SELECT dispatch_result FROM warmup_tasks WHERE task_id=$1`, warmTask, warmRaw},
					{`SELECT confirmed_result FROM send_recovery_resolutions WHERE id=$1`, audit, raw},
				} {
					var current []byte
					if err := pool.QueryRow(t.Context(), item.query, item.id).Scan(&current); err != nil {
						t.Fatal(err)
					}
					assertExportJSONEqual(t, current, item.expected)
				}
				if importTransportGeneration(t, pool, mailbox) != source {
					t.Fatal("export changed source generation")
				}
				if _, err := svc.ImportFrom(t.Context(), f.org, bytes.NewReader(archive.Bytes()), ImportOptions{Passphrase: passphrase, Conflict: models.OrgImportConflictOverwrite, ActorUserID: f.owner}, nil); err != nil {
					t.Fatal(err)
				}
				var historical []byte
				var local bool
				if err := pool.QueryRow(t.Context(), `SELECT confirmed_result,recorded_locally_at IS NOT NULL FROM send_recovery_resolutions WHERE id=$1`, audit).Scan(&historical, &local); err != nil {
					t.Fatal(err)
				}
				var expected map[string]json.RawMessage
				if err := json.Unmarshal(raw, &expected); err != nil {
					t.Fatal(err)
				}
				delete(expected, "transport_generation")
				portable, err := json.Marshal(expected)
				if err != nil {
					t.Fatal(err)
				}
				assertExportJSONEqual(t, historical, portable)
				if local || importTransportGeneration(t, pool, mailbox) == source {
					t.Fatal("history gained authority or destination retained source proof")
				}
			})
		}
	}
}
