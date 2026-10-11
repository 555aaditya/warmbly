package orgtransfer

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func TestLiveArchiveSendRecoveryLocalOriginResets(t *testing.T) {
	svc, pool := liveImportService(t)
	f := newTenantFixture(t, pool)
	ctx := t.Context()
	mailbox := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO email_accounts(id,user_id,organization_id,email,name,signature_plain,signature_html,provider)
		VALUES($1,$2,$3,$4,'Archive','','','smtp_imap')`, mailbox, f.owner, f.org, mailbox.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM email_accounts WHERE id=$1`, mailbox) })
	var heldTask uuid.UUID
	for _, item := range []struct{ evidence, state, contrary string }{
		{"operator_confirmed_sent", "unknown", "failed"},
		{"operator_confirmed_not_sent", "unknown", "sent"},
		{"authoritative_result_conflict", "sent", "failed"},
		{"authoritative_result_conflict", "failed", "sent"},
	} {
		task := uuid.New()
		if heldTask == uuid.Nil {
			heldTask = task
		}
		if _, err := pool.Exec(ctx, `INSERT INTO tasks(id,email_account_id,task_type,status,message_id,send_result_state,send_result_applied_at)
			VALUES($1,$2,'email','completed','',$3,CASE WHEN $3<>'unknown' THEN NOW() END)`, task, mailbox, item.state); err != nil {
			t.Fatal(err)
		}
		reason, reference := "conflict", ""
		var confirmed any
		if item.state == "unknown" {
			reason, reference = "unknown", "Source confirmation fixture"
			result := models.SendEmailResult{TaskID: task, Success: item.evidence == "operator_confirmed_sent"}
			if !result.Success {
				result.Error = &models.EmailSendError{Code: "RECIPIENT_REJECTED"}
			}
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			confirmed = raw
		}
		if _, err := pool.Exec(ctx, `INSERT INTO send_recovery_resolutions(organization_id,email_account_id,recovery_task_id,previous_reason,evidence_type,confirmation_reference,confirmed_result,conflicting_result_state,recorded_locally_at,conflict_detected_at,conflict_resolved_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,'2099-01-01Z','2099-01-01Z','2099-01-01Z')`, f.org, mailbox, task, reason, item.evidence, reference, confirmed, item.contrary); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='unknown',send_recovery_task_id=$2 WHERE id=$1`, mailbox, heldTask); err != nil {
		t.Fatal(err)
	}
	groups := []models.OrgDataGroup{models.OrgDataGroupCore, models.OrgDataGroupSending}
	var archive bytes.Buffer
	if _, err := svc.ExportTo(ctx, f.org, ExportOptions{Groups: groups}, &archive, nil); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatal(err)
	}
	exported := 0
	for _, entry := range zr.File {
		if entry.Name != dataPath("send_recovery_resolutions") {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		decoder := json.NewDecoder(reader)
		for {
			var row map[string]json.RawMessage
			if err := decoder.Decode(&row); err == io.EOF {
				break
			} else if err != nil {
				t.Fatal(err)
			}
			if string(row["recorded_locally_at"]) == "null" || string(row["conflict_resolved_at"]) == "null" {
				t.Fatal("export destroyed source evidence rather than import resetting local authority")
			}
			exported++
		}
	}
	if exported != 4 {
		t.Fatalf("native export lost recovery audit: rows=%d", exported)
	}
	if _, err := svc.ImportFrom(ctx, f.org, bytes.NewReader(archive.Bytes()), ImportOptions{Groups: groups, Conflict: models.OrgImportConflictOverwrite, ActorUserID: f.owner}, nil); err != nil {
		t.Fatal(err)
	}
	var count, reset, confirmations, automatic int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE recorded_locally_at IS NULL AND conflict_resolved_at IS NULL AND conflict_detected_at='2099-01-01Z'),
		count(*) FILTER(WHERE confirmed_result IS NOT NULL AND confirmation_reference='Source confirmation fixture'),
		count(*) FILTER(WHERE evidence_type='authoritative_result_conflict' AND conflicting_result_state IN('sent','failed') AND confirmed_result IS NULL AND evidence_task_id IS NULL AND confirmation_reference='')
		FROM send_recovery_resolutions WHERE organization_id=$1`, f.org).Scan(&count, &reset, &confirmations, &automatic); err != nil || count != 4 || reset != 4 || confirmations != 2 || automatic != 2 {
		t.Errorf("archive lost history or retained source authority: count=%d reset=%d confirmed=%d automatic=%d error=%v", count, reset, confirmations, automatic, err)
	}
	results, err := repository.NewTaskRepository(pool).(interface {
		ListUnappliedSendResults(context.Context, int) ([]models.SendEmailResult, error)
	}).ListUnappliedSendResults(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		var ours bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1 AND email_account_id=$2)`, result.TaskID, mailbox).Scan(&ours); err != nil || ours {
			t.Fatalf("archive confirmation became executable: ours=%v error=%v", ours, err)
		}
	}
	var held bool
	var heldAfter uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT send_recovery_hold,send_recovery_task_id FROM email_accounts WHERE id=$1`, mailbox).Scan(&held, &heldAfter); err != nil || !held || heldAfter != heldTask {
		t.Fatalf("archive roundtrip released restrictive hold: held=%v error=%v", held, err)
	}
	var audit uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM send_recovery_resolutions WHERE recovery_task_id=$1`, heldTask).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE send_recovery_resolutions SET recorded_locally_at=NOW(),conflict_resolved_at=NOW() WHERE id=$1`, audit); err != nil {
		t.Fatal(err)
	}
	for _, conflict := range []models.OrgImportConflict{models.OrgImportConflictSkip, models.OrgImportConflictOverwrite} {
		t.Run("old_archive_"+string(conflict), func(t *testing.T) {
			oldArchive := tenantArchive(t, map[string][]map[string]any{"send_recovery_resolutions": {{
				"id": audit, "organization_id": f.org, "email_account_id": mailbox, "recovery_task_id": heldTask,
				"previous_reason": "unknown", "evidence_type": "operator_confirmed_not_sent", "confirmation_reference": "Older archive fixture",
				"confirmed_result": map[string]any{"task_id": heldTask, "success": false, "error": map[string]any{"code": "RECIPIENT_REJECTED"}},
			}}})
			if _, err := svc.ImportFrom(ctx, f.org, oldArchive, ImportOptions{Conflict: conflict, ActorUserID: f.owner}, nil); err != nil {
				t.Fatal(err)
			}
			var local, resolved, success bool
			if err := pool.QueryRow(ctx, `SELECT recorded_locally_at IS NOT NULL,conflict_resolved_at IS NOT NULL,(confirmed_result->>'success')::boolean FROM send_recovery_resolutions WHERE id=$1`, audit).Scan(&local, &resolved, &success); err != nil {
				t.Fatal(err)
			}
			kept := conflict == models.OrgImportConflictSkip
			if local != kept || resolved != kept || success != kept {
				t.Fatalf("old archive inherited authority or skip changed local evidence: local=%v resolved=%v success=%v kept=%v", local, resolved, success, kept)
			}
		})
	}
}

func TestLiveImportSendRecoveryHistoryCannotReconcileUnknownOutcome(t *testing.T) {
	svc, pool := liveImportService(t)
	dest := newTenantFixture(t, pool)
	ctx := t.Context()
	mailbox, task, audit, sourceRepair := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO email_accounts(id,user_id,organization_id,email,name,signature_plain,signature_html,provider)
		VALUES($1,$2,$3,$4,'Imported','','','smtp_imap')`, mailbox, dest.owner, dest.org, "import-"+mailbox.String()[:8]+"@example.test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM email_accounts WHERE id=$1`, mailbox) })
	archive := tenantArchive(t, map[string][]map[string]any{
		"tasks": {{"id": task, "email_account_id": mailbox, "task_type": "email", "status": "completed", "message_id": "", "completed_at": time.Now().UTC(), "send_result_state": "unknown"}},
		"send_recovery_resolutions": {{"id": audit, "organization_id": dest.org, "email_account_id": mailbox, "recovery_task_id": task,
			"previous_reason": "unknown", "evidence_type": "operator_confirmed_sent", "confirmation_reference": "Source-instance provider confirmation",
			"recorded_locally_at":  time.Now().UTC().Add(365 * 24 * time.Hour),
			"conflict_detected_at": time.Now().UTC().Add(365 * 24 * time.Hour), "conflicting_result_state": "failed",
			"conflict_resolved_at": time.Now().UTC().Add(365 * 24 * time.Hour),
			"confirmed_result":     map[string]any{"task_id": task, "success": true, "message_id": "source-result@example.test"}},
			{"id": sourceRepair, "organization_id": dest.org, "email_account_id": mailbox, "recovery_task_id": task,
				"previous_reason": "conflict", "evidence_type": "operator_provider_confirmation", "evidence_task_id": task,
				"confirmation_reference": "Source conflict repair", "recorded_locally_at": time.Now().UTC().Add(365 * 24 * time.Hour)}},
	})
	if _, err := svc.ImportFrom(ctx, dest.org, archive, ImportOptions{Conflict: models.OrgImportConflictOverwrite, ActorUserID: dest.owner}, nil); err != nil {
		t.Fatal(err)
	}
	repo := repository.NewTaskRepository(pool).(interface {
		ListUnappliedSendResults(context.Context, int) ([]models.SendEmailResult, error)
		ApplySendResult(context.Context, models.SendEmailResult, func(context.Context) error) error
	})
	results, err := repo.ListUnappliedSendResults(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.TaskID == task {
			t.Fatal("imported historical confirmation gained result reconciliation authority")
		}
	}
	var preserved bool
	if err := pool.QueryRow(ctx, `SELECT recorded_locally_at IS NULL AND conflict_resolved_at IS NULL AND confirmed_result->>'success'='true'
		AND confirmation_reference='Source-instance provider confirmation' FROM send_recovery_resolutions WHERE id=$1`, audit).Scan(&preserved); err != nil || !preserved {
		t.Fatalf("portable audit lost or imported proof gained local provenance: preserved=%v error=%v", preserved, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE email_accounts SET status='active',send_recovery_hold=true,send_recovery_reason='unknown',send_recovery_task_id=$2 WHERE id=$1`, mailbox, task); err != nil {
		t.Fatal(err)
	}
	failed := models.SendEmailResult{TaskID: task, Error: &models.EmailSendError{Code: "RECIPIENT_REJECTED"}}
	if err := repo.ApplySendResult(ctx, failed, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := repo.ApplySendResult(ctx, models.SendEmailResult{TaskID: task, Success: true}, func(context.Context) error {
		t.Error("conflict re-applied accounting")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var held bool
	var reason string
	if err := pool.QueryRow(ctx, `SELECT send_recovery_hold,COALESCE(send_recovery_reason,'') FROM email_accounts WHERE id=$1`, mailbox).Scan(&held, &reason); err != nil || !held || reason != "conflict" {
		t.Fatalf("imported operator repair erased a fresh local conflict: held=%v reason=%s error=%v", held, reason, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM send_recovery_resolutions WHERE recovery_task_id=$1 AND recorded_locally_at IS NOT NULL AND evidence_type='authoritative_result_conflict'`, task).Scan(&count); err != nil || count != 1 {
		t.Fatalf("fresh local conflict was merged into imported confirmation: count=%d error=%v", count, err)
	}
	emailRepo := repository.NewEmailRepostory(&db.DB{Pool: pool}, nil)
	resolution := &models.UpdateEmail{SendRecoveryResolution: &models.SendRecoveryResolution{
		HeldTaskID: task, HeldReason: "conflict", EvidenceType: "operator_provider_confirmation",
		EvidenceTaskID: &task, ConfirmationReference: "Fresh local provider confirmation",
	}}
	if _, xerr := emailRepo.Update(ctx, dest.org.String(), mailbox.String(), resolution); xerr != nil {
		t.Fatal(xerr)
	}
	if err := pool.QueryRow(ctx, `SELECT send_recovery_hold FROM email_accounts WHERE id=$1`, mailbox).Scan(&held); err != nil || held {
		t.Fatalf("fresh local confirmation could not resolve imported and native conflict regardless of clock order: held=%v error=%v", held, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM send_recovery_resolutions WHERE recovery_task_id=$1 AND conflict_detected_at IS NOT NULL AND conflict_resolved_at IS NULL`, task).Scan(&count); err != nil || count != 0 {
		t.Fatalf("explicit confirmation left known conflict rows unresolved: count=%d error=%v", count, err)
	}
}

func TestLiveImportedHistoryDoesNotRepeatAuthoritativeWorkerResult(t *testing.T) {
	for _, source := range []string{"executor", "warmup"} {
		for _, sent := range []bool{false, true} {
			for _, fresh := range []bool{false, true} {
				name := source + map[bool]string{false: "/failed", true: "/sent"}[sent] + map[bool]string{false: "/independent", true: "/after fresh confirmation"}[fresh]
				t.Run(name, func(t *testing.T) {
					svc, pool := liveImportService(t)
					dest := newTenantFixture(t, pool)
					ctx := t.Context()
					mailbox, task, audit := uuid.New(), uuid.New(), uuid.New()
					if _, err := pool.Exec(ctx, `INSERT INTO email_accounts(id,user_id,organization_id,email,name,signature_plain,signature_html,provider)
						VALUES($1,$2,$3,$4,'Imported','','','smtp_imap')`, mailbox, dest.owner, dest.org, mailbox.String()+"@example.test"); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM email_accounts WHERE id=$1`, mailbox) })
					kind := "email"
					if source == "warmup" {
						kind = "warmup"
					}
					failure := &models.EmailSendError{Code: "RECIPIENT_REJECTED"}
					history := models.SendEmailResult{TaskID: task, Success: !sent, MessageID: "history@example.test"}
					evidence := "operator_confirmed_sent"
					if sent {
						history.Error, evidence = failure, "operator_confirmed_not_sent"
					}
					archive := tenantArchive(t, map[string][]map[string]any{
						"tasks": {{"id": task, "email_account_id": mailbox, "task_type": kind, "status": "completed", "message_id": "", "completed_at": time.Now().UTC().Add(-5 * time.Minute), "send_result_state": "unknown"}},
						"send_recovery_resolutions": {{"id": audit, "organization_id": dest.org, "email_account_id": mailbox, "recovery_task_id": task,
							"previous_reason": "unknown", "evidence_type": evidence, "confirmation_reference": "Source confirmation history", "confirmed_result": history,
							"recorded_locally_at": time.Now().UTC().Add(365 * 24 * time.Hour)}},
					})
					if _, err := svc.ImportFrom(ctx, dest.org, archive, ImportOptions{Conflict: models.OrgImportConflictOverwrite, ActorUserID: dest.owner}, nil); err != nil {
						t.Fatal(err)
					}
					repo := repository.NewTaskRepository(pool).(interface {
						ListUnappliedSendResults(context.Context, int) ([]models.SendEmailResult, error)
						ApplySendResult(context.Context, models.SendEmailResult, func(context.Context) error) error
					})
					results, err := repo.ListUnappliedSendResults(ctx, 1)
					if err != nil || len(results) != 0 {
						t.Fatalf("imported history gained execution authority: results=%v error=%v", results, err)
					}
					if _, err := pool.Exec(ctx, `UPDATE email_accounts SET status='active',send_recovery_hold=true,send_recovery_reason='unknown',send_recovery_task_id=$2 WHERE id=$1`, mailbox, task); err != nil {
						t.Fatal(err)
					}
					var historyBefore string
					if err := pool.QueryRow(ctx, `SELECT to_jsonb(r)::text FROM send_recovery_resolutions r WHERE id=$1`, audit).Scan(&historyBefore); err != nil {
						t.Fatal(err)
					}
					calls := 0
					apply := func(context.Context) error { calls++; return nil }
					var appliedBefore time.Time
					if fresh {
						resolution := models.SendRecoveryResolution{HeldTaskID: task, HeldReason: "unknown", EvidenceType: evidence, ConfirmationReference: "Fresh local provider confirmation"}
						if !sent {
							resolution.MessageID = "fresh@example.test"
						}
						emailRepo := repository.NewEmailRepostory(&db.DB{Pool: pool}, nil)
						if _, xerr := emailRepo.Update(ctx, dest.org.String(), mailbox.String(), &models.UpdateEmail{SendRecoveryResolution: &resolution}); xerr != nil {
							t.Fatal(xerr)
						}
						results, err = repo.ListUnappliedSendResults(ctx, 1)
						if err != nil || len(results) != 1 || results[0].TaskID != task || results[0].Success != !sent {
							t.Fatalf("fresh local confirmation unavailable: results=%v error=%v", results, err)
						}
						if err := repo.ApplySendResult(ctx, results[0], apply); err != nil {
							t.Fatal(err)
						}
						if err := pool.QueryRow(ctx, `SELECT send_result_applied_at FROM tasks WHERE id=$1`, task).Scan(&appliedBefore); err != nil {
							t.Fatal(err)
						}
					}
					worker := models.SendEmailResult{TaskID: task, Success: sent, MessageID: "native@example.test"}
					if !sent {
						worker.Error = failure
					}
					raw, err := json.Marshal(worker)
					if err != nil {
						t.Fatal(err)
					}
					if source == "executor" {
						_, err = pool.Exec(ctx, `UPDATE tasks SET send_executor_result=$2 WHERE id=$1`, task, raw)
					} else {
						_, err = pool.Exec(ctx, `INSERT INTO warmup_tasks(task_id,dispatch_result)VALUES($1,$2)`, task, raw)
					}
					if err != nil {
						t.Fatal(err)
					}
					results, err = repo.ListUnappliedSendResults(ctx, 1)
					if err != nil || len(results) != 1 || results[0].TaskID != task || results[0].Success != sent {
						t.Fatalf("independent authoritative worker result unavailable: results=%v error=%v", results, err)
					}
					if err := repo.ApplySendResult(ctx, results[0], apply); err != nil {
						t.Fatal(err)
					}
					results, err = repo.ListUnappliedSendResults(ctx, 1)
					if err != nil || len(results) != 0 {
						t.Errorf("applied worker result repeats via imported history: results=%v error=%v", results, err)
					}
					var state, historyAfter string
					var appliedAt time.Time
					var held bool
					var conflicts int
					if err := pool.QueryRow(ctx, `SELECT t.send_result_state,t.send_result_applied_at,ea.send_recovery_hold,
						(SELECT count(*) FROM send_recovery_resolutions r WHERE r.recovery_task_id=t.id AND r.recorded_locally_at IS NOT NULL AND r.conflict_detected_at IS NOT NULL AND r.conflict_resolved_at IS NULL),
						(SELECT to_jsonb(r)::text FROM send_recovery_resolutions r WHERE r.id=$2)
						FROM tasks t JOIN email_accounts ea ON ea.id=t.email_account_id WHERE t.id=$1`, task, audit).Scan(&state, &appliedAt, &held, &conflicts, &historyAfter); err != nil {
						t.Fatal(err)
					}
					wantSent, wantConflicts := sent, 0
					if fresh {
						wantSent, wantConflicts = !sent, 1
					}
					wantState := "failed"
					if wantSent {
						wantState = "sent"
					}
					if state != wantState || calls != 1 || held != fresh || conflicts != wantConflicts || historyAfter != historyBefore || fresh && !appliedAt.Equal(appliedBefore) {
						t.Fatalf("state=%s callbacks=%d held=%v conflicts=%d history preserved=%v original application preserved=%v", state, calls, held, conflicts, historyAfter == historyBefore, appliedAt.Equal(appliedBefore))
					}
					newer := uuid.New()
					newRaw, err := json.Marshal(models.SendEmailResult{TaskID: newer, Success: true})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := pool.Exec(ctx, `INSERT INTO tasks(id,email_account_id,task_type,status,message_id,send_result_state,send_executor_result)
						VALUES($1,$2,'email','completed','','unknown',$3)`, newer, mailbox, newRaw); err != nil {
						t.Fatal(err)
					}
					results, err = repo.ListUnappliedSendResults(ctx, 1)
					if err != nil || len(results) != 1 || results[0].TaskID != newer {
						t.Fatalf("imported history starves newer result in batch of one: results=%v error=%v", results, err)
					}
					if err := repo.ApplySendResult(ctx, results[0], apply); err != nil {
						t.Fatal(err)
					}
					results, err = repo.ListUnappliedSendResults(ctx, 1)
					if err != nil || len(results) != 0 || calls != 2 {
						t.Fatalf("newer result did not settle idempotently: results=%v callbacks=%d error=%v", results, calls, err)
					}
				})
			}
		}
	}
}
