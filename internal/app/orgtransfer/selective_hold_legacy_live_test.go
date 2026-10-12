package orgtransfer

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func TestLiveImportLegacyUnknownPreservesOtherOutcome(t *testing.T) {
	for _, group := range []models.OrgDataGroup{models.OrgDataGroupCore, models.OrgDataGroupSending} {
		for _, mode := range []models.OrgImportConflict{models.OrgImportConflictSkip, models.OrgImportConflictOverwrite} {
			t.Run(string(group)+"/"+string(mode), func(t *testing.T) {
				f := newSelectiveHoldReviewFixture(t)
				other := uuid.New()
				f.exec(t, `INSERT INTO tasks(id,email_account_id,task_type,status,message_id) VALUES($1,$2,'email','active','')`, other, f.mailbox)
				f.exec(t, `INSERT INTO email_tasks(task_id,to_addrs,subject,body,body_plain,encrypted) VALUES($1,ARRAY['other@fixture.invalid'],'Other','Other','Other',false)`, other)
				groups := []models.OrgDataGroup{group}
				archive := f.archive(t, groups)
				recovery := f.repo.(repository.SendResultRecovery)
				calls, effects := 0, 0
				apply := func(inner context.Context) error {
					calls++
					repository.AfterSendResultCommit(inner, func(context.Context) { effects++ })
					return nil
				}
				for _, task := range []uuid.UUID{f.task, other} {
					if err := f.repo.UpdateTaskStatus(t.Context(), task, "completed"); err != nil {
						t.Fatal(err)
					}
					if err := recovery.ApplySendResult(t.Context(), models.SendEmailResult{TaskID: task}, apply); err != nil {
						t.Fatal(err)
					}
					assertLegacyArchiveTask(t, f, task)
				}
				if calls != 0 || effects != 0 {
					t.Fatalf("unknown outcomes invoked effects: %d %d", calls, effects)
				}
				otherFixture := *f
				otherFixture.task = other
				before, beforeOther := f.localRows(t), otherFixture.localRows(t)
				var heldTask uuid.UUID
				if err := f.pool.QueryRow(t.Context(), `SELECT send_recovery_task_id FROM email_accounts WHERE id=$1 AND send_recovery_hold`, f.mailbox).Scan(&heldTask); err != nil {
					t.Fatal(err)
				}
				assertLegacyArchiveUnknowns(t, f, 2)
				f.importArchive(t, archive, groups, mode)
				assertLegacyArchiveUnknowns(t, f, 2)
				if f.localRows(t) != before || otherFixture.localRows(t) != beforeOther {
					t.Error("import rewound one of two legacy unknown tasks or typed payloads")
				}
				result := models.SendEmailResult{TaskID: heldTask, Success: true, MessageID: "<fresh-legacy@fixture.invalid>", SentAt: time.Now()}
				if err := recovery.ApplySendResult(t.Context(), result, apply); err != nil {
					t.Fatal(err)
				}
				assertLegacyArchiveUnknowns(t, f, 1)
				assertHold := func() {
					t.Helper()
					var held bool
					if err := f.pool.QueryRow(t.Context(), `SELECT send_recovery_hold AND send_recovery_reason='unknown' AND send_recovery_task_id<>$2 FROM email_accounts WHERE id=$1`, f.mailbox, heldTask).Scan(&held); err != nil || !held {
						t.Fatalf("settling known result lost other unknown hold: %v %v", held, err)
					}
				}
				assertHold()
				f.importArchive(t, archive, groups, mode)
				for range 2 {
					if err := recovery.ApplySendResult(t.Context(), result, apply); err != nil {
						t.Fatal(err)
					}
				}
				if calls != 1 || effects != 1 {
					t.Fatalf("repeat archive/result repeated effects: %d %d", calls, effects)
				}
				assertLegacyArchiveUnknowns(t, f, 1)
				assertHold()
			})
		}
	}
}
func TestLiveImportLegacyAppliedResultRemainsIdempotent(t *testing.T) {
	for _, group := range []models.OrgDataGroup{models.OrgDataGroupCore, models.OrgDataGroupSending} {
		for _, success := range []bool{false, true} {
			t.Run(string(group)+"/"+map[bool]string{false: "failed", true: "sent"}[success], func(t *testing.T) {
				f := newSelectiveHoldReviewFixture(t)
				groups := []models.OrgDataGroup{group}
				archive := f.archive(t, groups)
				if err := f.repo.UpdateTaskStatus(t.Context(), f.task, "completed"); err != nil {
					t.Fatal(err)
				}
				result := models.SendEmailResult{TaskID: f.task, Success: success, MessageID: "<legacy-local@fixture.invalid>", SentAt: time.Now()}
				if !success {
					result.Error = &models.EmailSendError{Failure: &errx.SendFailure{Disposition: errx.SendPermanent, Scope: "recipient", ObservedAt: time.Now()}}
				}
				calls, effects := 0, 0
				apply := func(inner context.Context) error {
					calls++
					repository.AfterSendResultCommit(inner, func(context.Context) { effects++ })
					if success {
						return f.repo.UpdateTaskMessageID(inner, f.task, result.MessageID)
					}
					return f.repo.UpdateTaskStatus(inner, f.task, "failed")
				}
				recovery := f.repo.(repository.SendResultRecovery)
				if err := recovery.ApplySendResult(t.Context(), result, apply); err != nil {
					t.Fatal(err)
				}
				var noNonce, applied bool
				if err := f.pool.QueryRow(t.Context(), `SELECT send_executor_nonce IS NULL,send_result_applied_at IS NOT NULL FROM tasks WHERE id=$1`, f.task).Scan(&noNonce, &applied); err != nil || !noNonce || !applied || calls != 1 || effects != 1 {
					t.Fatalf("native nonce-less applied control invalid: nonceNull=%v applied=%v calls=%d effects=%d err=%v", noNonce, applied, calls, effects, err)
				}
				before := f.localRows(t)
				f.importArchive(t, archive, groups, models.OrgImportConflictOverwrite)
				if after := f.localRows(t); after != before {
					t.Error("archive rewound the applied legacy result, task identity, status, or typed payload")
				}
				if err := recovery.ApplySendResult(t.Context(), result, apply); err != nil || calls != 1 || effects != 1 {
					t.Errorf("applied result effects repeated: calls=%d effects=%d err=%v", calls, effects, err)
				}
				assertLegacyArchiveUnknowns(t, f, 0)
				assertLegacyArchiveNoHold(t, f)
			})
		}
	}
}

func TestLiveImportLegacyStampedTaskAcceptsLaterResult(t *testing.T) {
	t.Run("completion_during_source_hold_import", func(t *testing.T) { testLegacyCompletionDuringSourceHoldImport(t, false) })
	t.Run("completion_after_insert_snapshot", func(t *testing.T) { testLegacyCompletionDuringSourceHoldImport(t, true) })
	t.Run("completed_before_import", testLegacyStampedBeforeImport)
}

func testLegacyStampedBeforeImport(t *testing.T) {
	f := newSelectiveHoldReviewFixture(t)
	groups := []models.OrgDataGroup{models.OrgDataGroupSending}
	archive := f.archive(t, groups)
	if err := f.repo.UpdateTaskStatus(t.Context(), f.task, "completed"); err != nil {
		t.Fatal(err)
	}
	before := f.localRows(t)
	f.importArchive(t, archive, groups, models.OrgImportConflictOverwrite)
	if after := f.localRows(t); after != before {
		t.Error("import rewound a locally stamped legacy task before its result arrived")
	}
	result := models.SendEmailResult{TaskID: f.task, Success: true, MessageID: "<late-legacy@fixture.invalid>", SentAt: time.Now()}
	calls, effects := 0, 0
	apply := func(inner context.Context) error {
		calls++
		repository.AfterSendResultCommit(inner, func(context.Context) { effects++ })
		return f.repo.UpdateTaskMessageID(inner, f.task, result.MessageID)
	}
	for range 2 {
		if err := f.repo.(repository.SendResultRecovery).ApplySendResult(t.Context(), result, apply); err != nil {
			t.Fatal(err)
		}
	}
	var correctlySettled bool
	if err := f.pool.QueryRow(t.Context(), `SELECT status='completed' AND send_result_state='sent' AND send_result_applied_at IS NOT NULL AND send_executor_nonce IS NULL AND message_id=$2 FROM tasks WHERE id=$1`, f.task, result.MessageID).Scan(&correctlySettled); err != nil || !correctlySettled || calls != 1 || effects != 1 {
		t.Errorf("valid late legacy result not applied once: state=%v calls=%d effects=%d err=%v", correctlySettled, calls, effects, err)
	}
	assertLegacyArchiveUnknowns(t, f, 0)
	assertLegacyArchiveNoHold(t, f)
}

func testLegacyCompletionDuringSourceHoldImport(t *testing.T, afterSnapshot bool) {
	f := newSelectiveHoldReviewFixture(t)
	groups := []models.OrgDataGroup{models.OrgDataGroupSending}
	older := f.archive(t, groups)
	if err := f.repo.UpdateTaskStatus(t.Context(), f.task, "completed"); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.(repository.SendResultRecovery).ApplySendResult(t.Context(), models.SendEmailResult{TaskID: f.task}, nil); err != nil {
		t.Fatal(err)
	}
	newer := f.archive(t, groups)
	f.exec(t, `DELETE FROM email_accounts WHERE id=$1`, f.mailbox)
	f.importArchive(t, older, groups, models.OrgImportConflictOverwrite)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	importPID := make(chan uint32, 1)
	if afterSnapshot {
		service := f.svc.(*service)
		service.repo = &observedWholeImportRepository{OrgTransferRepository: service.repo, pid: importPID}
		name := "fixture_completion_" + strings.ReplaceAll(f.task.String(), "-", "")
		f.exec(t, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN PERFORM pg_advisory_xact_lock(hashtextextended(NEW.id::text,9900001)); RETURN NEW; END $$`, name))
		f.exec(t, fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON tasks FOR EACH ROW WHEN (NEW.id='%s') EXECUTE FUNCTION %s()`, name, f.task, name))
		defer f.exec(t, fmt.Sprintf(`DROP TRIGGER %s ON tasks; DROP FUNCTION %s()`, name, name))
	}
	atTasks, resume := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := f.svc.ImportFrom(ctx, f.tenant.org, bytes.NewReader(newer), ImportOptions{
			Groups: groups, Passphrase: "synthetic-review-passphrase", Conflict: models.OrgImportConflictOverwrite, ActorUserID: f.tenant.owner,
		}, func(_ int, stage string) {
			if stage == "importing tasks" {
				close(atTasks)
				select {
				case <-resume:
				case <-ctx.Done():
				}
			}
		})
		done <- err
	}()
	select {
	case <-atTasks:
	case err := <-done:
		t.Fatalf("import failed before barrier: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var release func()
	if afterSnapshot {
		gate, err := f.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer gate.Rollback(context.Background())
		if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text,9900001))`, f.task); err != nil {
			t.Fatal(err)
		}
		close(resume)
		pid := <-importPID
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			var blocked bool
			if err := f.pool.QueryRow(ctx, `SELECT $2::int=ANY(pg_blocking_pids($1))`, pid, gate.Conn().PgConn().PID()).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				break
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				t.Fatal("import did not reach the before-insert gate", ctx.Err())
			}
		}
		release = func() {
			if err := gate.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		release = func() { close(resume) }
	}
	if err := f.repo.UpdateTaskStatus(ctx, f.task, "completed"); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := f.pool.QueryRow(ctx, `SELECT completed_at::text FROM tasks WHERE id=$1`, f.task).Scan(&before); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var retained bool
	if err := f.pool.QueryRow(ctx, `SELECT status='completed' AND completed_at::text=$2 AND send_result_state IS NULL AND send_executor_nonce IS NULL
		FROM tasks WHERE id=$1`, f.task, before).Scan(&retained); err != nil || !retained {
		t.Errorf("import ignored genuinely new local completion after source-hold classification: retained=%v err=%v", retained, err)
	}
	result := models.SendEmailResult{TaskID: f.task, Success: true, MessageID: "<late-during-import@fixture.invalid>", SentAt: time.Now()}
	calls, effects := 0, 0
	for range 2 {
		if err := f.repo.(repository.SendResultRecovery).ApplySendResult(ctx, result, func(inner context.Context) error {
			calls++
			repository.AfterSendResultCommit(inner, func(context.Context) { effects++ })
			return f.repo.UpdateTaskMessageID(inner, f.task, result.MessageID)
		}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 || effects != 1 {
		t.Fatalf("late local result effects repeated: %d %d", calls, effects)
	}
	assertLegacyArchiveUnknowns(t, f, 0)
	assertLegacyArchiveNoHold(t, f)
}

func TestLiveImportRefundedWarmupChargeWithoutNonces(t *testing.T) {
	f, _ := nativeWarmupArchiveFixture(t, false)
	groups := []models.OrgDataGroup{models.OrgDataGroupSending, models.OrgDataGroupWarmup}
	archive := f.archive(t, groups)
	lineage := f.repo.(repository.WarmupLineageRepository)
	if claimed, err := lineage.ClaimWarmupTask(t.Context(), f.task, time.Now()); err != nil || !claimed {
		t.Fatalf("real warmup claim failed: %v %v", claimed, err)
	}
	dispatch := f.repo.(repository.WarmupDispatchRepository)
	nonce, err := dispatch.AuthorizeWarmupDispatch(t.Context(), f.task, f.mailbox, f.worker)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch.DeferWarmupDispatch(t.Context(), f.task, f.mailbox, f.worker, nonce, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var noNonces, charged, refunded bool
	if err := f.pool.QueryRow(t.Context(), `SELECT t.send_executor_nonce IS NULL AND w.dispatch_nonce IS NULL,
		w.warmup_charged_date IS NOT NULL,w.warmup_refunded_at IS NOT NULL FROM tasks t JOIN warmup_tasks w ON w.task_id=t.id WHERE t.id=$1`, f.task).Scan(&noNonces, &charged, &refunded); err != nil || !noNonces || !charged || !refunded {
		t.Fatalf("native refunded-charge control invalid: noNonces=%v charged=%v refunded=%v err=%v", noNonces, charged, refunded, err)
	}
	before := f.localRows(t)
	booked, err := f.repo.GetBookedSendsForAccount(t.Context(), f.mailbox, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f.importArchive(t, archive, groups, models.OrgImportConflictOverwrite)
	if after := f.localRows(t); after != before {
		t.Error("import discarded the native refunded charge or rescheduled task while both nonces were absent")
	}
	bookedAfter, err := f.repo.GetBookedSendsForAccount(t.Context(), f.mailbox, time.Now())
	if err != nil || len(booked) != len(bookedAfter) {
		t.Errorf("import changed booked sends: %d -> %d %v", len(booked), len(bookedAfter), err)
	}
}

func TestLiveImportLegacyWarmupAppliedAccountingWithoutNonces(t *testing.T) {
	for _, reply := range []bool{false, true} {
		for _, success := range []bool{false, true} {
			t.Run(map[bool]string{false: "opening", true: "reply"}[reply]+"/"+map[bool]string{false: "failed", true: "sent"}[success], func(t *testing.T) {
				f, _ := nativeWarmupArchiveFixture(t, reply)
				groups := []models.OrgDataGroup{models.OrgDataGroupSending, models.OrgDataGroupWarmup}
				archive := f.archive(t, groups)
				warmup := repository.NewWarmupRepository(f.pool)
				if err := f.repo.UpdateTaskStatus(t.Context(), f.task, "completed"); err != nil {
					t.Fatal(err)
				}
				if err := warmup.IncrementDailyCount(t.Context(), f.mailbox, time.Now()); err != nil {
					t.Fatal(err)
				}
				if reply {
					if err := warmup.IncrementReplyCount(t.Context(), f.mailbox, time.Now()); err != nil {
						t.Fatal(err)
					}
				}
				result := models.SendEmailResult{TaskID: f.task, Success: success, MessageID: "<legacy-warmup@fixture.invalid>", SentAt: time.Now()}
				if !success {
					result.Error = &models.EmailSendError{Failure: &errx.SendFailure{Disposition: errx.SendPermanent, Scope: "recipient", ObservedAt: time.Now()}}
				}
				calls, effects := 0, 0
				apply := func(inner context.Context) error {
					calls++
					repository.AfterSendResultCommit(inner, func(context.Context) { effects++ })
					if !success {
						return warmup.FailWarmupSend(inner, f.mailbox, f.task, time.Now(), "Legacy native refusal", "Synthetic local refusal")
					}
					return warmup.RecordWarmupTokenDelivery(inner, f.task, result.MessageID)
				}
				recovery := f.repo.(repository.SendResultRecovery)
				if err := recovery.ApplySendResult(t.Context(), result, apply); err != nil {
					t.Fatal(err)
				}
				var legacy bool
				if err := f.pool.QueryRow(t.Context(), `SELECT t.send_executor_nonce IS NULL AND w.dispatch_nonce IS NULL AND w.warmup_charged_date IS NULL AND t.send_result_applied_at IS NOT NULL FROM tasks t JOIN warmup_tasks w ON w.task_id=t.id WHERE t.id=$1`, f.task).Scan(&legacy); err != nil || !legacy {
					t.Fatalf("legacy native warmup control invalid: %v %v", legacy, err)
				}
				checkCount := func() {
					t.Helper()
					var count, replies int
					if err := f.pool.QueryRow(t.Context(), `SELECT emails_sent,emails_replied FROM warmup_statistics WHERE email_account_id=$1 AND date=(NOW() AT TIME ZONE 'UTC')::date`, f.mailbox).Scan(&count, &replies); err != nil || count != 1 || replies != map[bool]int{false: 0, true: 1}[reply] {
						t.Errorf("import/result rewound or repeated legacy accounted send: count=%d replies=%d err=%v", count, replies, err)
					}
				}
				checkCount()
				before := f.localRows(t)
				f.importArchive(t, archive, groups, models.OrgImportConflictOverwrite)
				if after := f.localRows(t); after != before {
					t.Error("import rewound the nonce-less applied warmup outcome, token or failed-send record")
				}
				checkCount()
				if err := recovery.ApplySendResult(t.Context(), result, apply); err != nil || calls != 1 || effects != 1 {
					t.Errorf("legacy accounting or postcommit effects repeated: calls=%d effects=%d err=%v", calls, effects, err)
				}
				checkCount()
				assertLegacyArchiveUnknowns(t, f, 0)
				assertLegacyArchiveNoHold(t, f)
			})
		}
	}
}
func TestLiveImportLegacyWarmupAppliedMarkers(t *testing.T) {
	for _, kind := range []string{"sent", "refunded", "reply_refunded"} {
		for _, mode := range []models.OrgImportConflict{models.OrgImportConflictSkip, models.OrgImportConflictOverwrite} {
			t.Run(kind+"/"+string(mode), func(t *testing.T) {
				f, _ := nativeWarmupArchiveFixture(t, kind == "reply_refunded")
				ctx := t.Context()
				if err := f.repo.UpdateTaskStatus(ctx, f.task, "completed"); err != nil {
					t.Fatal("native stamp failed")
				}
				// Historical accepted-data fixture, not a native nonce-free reservation creation.
				f.exec(t, `UPDATE warmup_tasks SET warmup_charged_date=(NOW() AT TIME ZONE 'UTC')::date,warmup_reply_charged=$2 WHERE task_id=$1`, f.task, kind == "reply_refunded")
				f.exec(t, `UPDATE warmup_statistics SET emails_sent=1,emails_replied=$2 WHERE email_account_id=$1 AND date=(NOW() AT TIME ZONE 'UTC')::date`, f.mailbox, map[bool]int{false: 0, true: 1}[kind == "reply_refunded"])
				groups := []models.OrgDataGroup{models.OrgDataGroupSending, models.OrgDataGroupWarmup}
				archive := f.archive(t, groups)
				warmup := repository.NewWarmupRepository(f.pool)
				recovery := f.repo.(repository.SendResultRecovery)
				result := models.SendEmailResult{TaskID: f.task, Success: kind == "sent", MessageID: "<legacy-result@fixture.invalid>", SentAt: time.Now()}
				if !result.Success {
					result.Error = &models.EmailSendError{Failure: &errx.SendFailure{Disposition: errx.SendPermanent, Scope: "recipient", ObservedAt: time.Now()}}
				}
				calls, effects := 0, 0
				apply := func(inner context.Context) error {
					calls++
					if result.Success {
						if err := warmup.RecordWarmupTokenDelivery(inner, f.task, result.MessageID); err != nil {
							return err
						}
						if err := f.repo.UpdateTaskMessageID(inner, f.task, result.MessageID); err != nil {
							return err
						}
					} else if err := warmup.FailWarmupSend(inner, f.mailbox, f.task, time.Now(), "Synthetic refusal", "Synthetic fixture"); err != nil {
						return err
					}
					repository.AfterSendResultCommit(inner, func(context.Context) { effects++ })
					return nil
				}
				if err := recovery.ApplySendResult(ctx, result, apply); err != nil {
					t.Fatal("native application failed")
				}
				assertLegacyArchiveTask(t, f, f.task)
				before := f.localRows(t)
				var sent, replied int
				if err := f.pool.QueryRow(ctx, `SELECT emails_sent,emails_replied FROM warmup_statistics WHERE email_account_id=$1 AND date=(NOW() AT TIME ZONE 'UTC')::date`, f.mailbox).Scan(&sent, &replied); err != nil {
					t.Fatal("counter read failed")
				}
				if sent != map[bool]int{false: 0, true: 1}[result.Success] || replied != 0 {
					t.Fatal("native accounting precondition failed")
				}
				f.importArchive(t, archive, groups, mode)
				if f.localRows(t) != before {
					t.Error("import rewound legacy warmup application markers or payload")
				}
				for range 2 {
					if err := recovery.ApplySendResult(ctx, result, apply); err != nil {
						t.Fatal("native redelivery failed")
					}
				}
				f.importArchive(t, archive, groups, mode)
				if err := recovery.ApplySendResult(ctx, result, apply); err != nil {
					t.Fatal("native post-import redelivery failed")
				}
				if calls != 1 || effects != 1 {
					t.Error("import repeated legacy warmup application or postcommit effects")
				}
				var chargeRetained bool
				if err := f.pool.QueryRow(ctx, `SELECT warmup_charged_date IS NOT NULL AND warmup_reply_charged=$2 AND ($3::bool OR warmup_refunded_at IS NOT NULL) FROM warmup_tasks WHERE task_id=$1`, f.task, kind == "reply_refunded", result.Success).Scan(&chargeRetained); err != nil || !chargeRetained {
					t.Error("import changed historical warmup charge/refund proof")
				}
				var afterSent, afterReply int
				if err := f.pool.QueryRow(ctx, `SELECT emails_sent,emails_replied FROM warmup_statistics WHERE email_account_id=$1 AND date=(NOW() AT TIME ZONE 'UTC')::date`, f.mailbox).Scan(&afterSent, &afterReply); err != nil || afterSent != sent || afterReply != replied {
					t.Error("import changed already settled warmup counters")
				}
				assertLegacyArchiveUnknowns(t, f, 0)
				var held bool
				if err := f.pool.QueryRow(ctx, "SELECT send_recovery_hold FROM email_accounts WHERE id=$1", f.mailbox).Scan(&held); err != nil || held {
					t.Fatalf("settled native legacy warmup gained unknown hold: %v %v", held, err)
				}
			})
		}
	}
}

func TestLiveImportLegacyUnknownLockOrder(t *testing.T) {
	for _, order := range []string{"import_first", "result_first"} {
		t.Run(order, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			f := newSelectiveHoldReviewFixture(t)
			groups := []models.OrgDataGroup{models.OrgDataGroupSending}
			archive := f.archive(t, groups)
			cfg, err := pgxpool.ParseConfig(os.Getenv("WARMBLY_TEST_DB"))
			if err != nil {
				t.Fatal("fixture config unavailable")
			}
			cfg.MaxConns, cfg.MinConns = 1, 0
			rpool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal("result pool unavailable")
			}
			defer rpool.Close()
			conn, err := rpool.Acquire(ctx)
			if err != nil {
				t.Fatal("result connection unavailable")
			}
			rpid := conn.Conn().PgConn().PID()
			conn.Release()
			recovery := repository.NewTaskRepository(rpool).(repository.SendResultRecovery)
			importPID := make(chan uint32, 1)
			svc := f.svc.(*service)
			svc.repo = &observedWholeImportRepository{OrgTransferRepository: svc.repo, pid: importPID}
			importDone, resultDone := make(chan error, 1), make(chan error, 1)
			atTasks, resume := make(chan struct{}, 1), make(chan struct{})
			startImport := func() {
				go func() {
					_, err := f.svc.ImportFrom(ctx, f.tenant.org, bytes.NewReader(archive), ImportOptions{Groups: groups, Passphrase: "synthetic-review-passphrase", Conflict: models.OrgImportConflictOverwrite, ActorUserID: f.tenant.owner}, func(_ int, stage string) {
						if stage == "importing tasks" && order == "import_first" {
							atTasks <- struct{}{}
							select {
							case <-resume:
							case <-ctx.Done():
							}
						}
					})
					importDone <- err
				}()
			}
			calls, effects := 0, 0
			startResult := func() {
				go func() {
					resultDone <- recovery.ApplySendResult(ctx, models.SendEmailResult{TaskID: f.task}, func(inner context.Context) error {
						calls++
						repository.AfterSendResultCommit(inner, func(context.Context) { effects++ })
						return nil
					})
				}()
			}
			waitBlocked := func(waiter, holder uint32) {
				t.Helper()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					var yes bool
					if err := f.pool.QueryRow(ctx, `SELECT $2::int=ANY(pg_blocking_pids($1))`, waiter, holder).Scan(&yes); err != nil {
						t.Fatal("lock inspection failed")
					}
					if yes {
						return
					}
					select {
					case <-ticker.C:
					case <-ctx.Done():
						t.Fatal("native blocker not reached")
					}
				}
			}
			if order == "result_first" {
				gate, err := f.pool.Begin(ctx)
				if err != nil {
					t.Fatal("gate unavailable")
				}
				defer gate.Rollback(context.Background())
				if _, err := gate.Exec(ctx, `SELECT id FROM email_accounts WHERE id=$1 FOR UPDATE`, f.mailbox); err != nil {
					t.Fatal("gate failed")
				}
				startResult()
				waitBlocked(rpid, gate.Conn().PgConn().PID())
				startImport()
				waitBlocked(<-importPID, rpid)
				if err := gate.Rollback(ctx); err != nil {
					t.Fatal("gate release failed")
				}
			} else {
				startImport()
				select {
				case <-atTasks:
				case <-ctx.Done():
					t.Fatal("import barrier unavailable")
				}
				startResult()
				waitBlocked(rpid, <-importPID)
				close(resume)
			}
			if err := <-resultDone; err != nil {
				t.Fatal("native unknown result failed")
			}
			if err := <-importDone; err != nil {
				t.Fatal("native concurrent import failed")
			}
			assertLegacyArchiveTask(t, f, f.task)
			var retained bool
			if err := f.pool.QueryRow(ctx, `SELECT t.send_result_state='unknown' AND ea.send_recovery_hold AND ea.send_recovery_task_id=t.id FROM tasks t JOIN email_accounts ea ON ea.id=t.email_account_id WHERE t.id=$1`, f.task).Scan(&retained); err != nil || !retained {
				t.Error("serialized import rewound a native legacy unknown result or hold")
			}
			if calls != 0 || effects != 0 {
				t.Fatalf("unknown outcome invoked effects: %d %d", calls, effects)
			}
			assertLegacyArchiveUnknowns(t, f, 1)
		})
	}
}

func assertLegacyArchiveTask(t *testing.T, f *selectiveHoldReviewFixture, task uuid.UUID) {
	t.Helper()
	var absent bool
	err := f.pool.QueryRow(t.Context(), `SELECT send_executor_nonce IS NULL
		AND NOT EXISTS(SELECT 1 FROM warmup_tasks WHERE task_id=t.id AND dispatch_nonce IS NOT NULL)
		AND NOT EXISTS(SELECT 1 FROM send_recovery_resolutions WHERE recovery_task_id=t.id AND recorded_locally_at IS NOT NULL)
		FROM tasks t WHERE id=$1`, task).Scan(&absent)
	if err != nil || !absent {
		t.Fatal("fixture did not produce accepted nonce-free local state")
	}
}

func TestLiveImportLegacyRestrictivePreservation(t *testing.T) {
	for _, mode := range []models.OrgImportConflict{models.OrgImportConflictSkip, models.OrgImportConflictOverwrite} {
		t.Run(string(mode), func(t *testing.T) {
			f := newSelectiveHoldReviewFixture(t)
			groups := []models.OrgDataGroup{models.OrgDataGroupSending}
			archive := f.archive(t, groups)
			if err := f.repo.UpdateTaskStatus(t.Context(), f.task, "completed"); err != nil {
				t.Fatal("native task stamp failed")
			}
			result := models.SendEmailResult{TaskID: f.task, Error: &models.EmailSendError{Failure: &errx.SendFailure{Disposition: errx.SendAuth, Scope: "mailbox", ObservedAt: time.Now()}}}
			calls, effects := 0, 0
			apply := func(inner context.Context) error {
				calls++
				repository.AfterSendResultCommit(inner, func(context.Context) { effects++ })
				return nil
			}
			recovery := f.repo.(repository.SendResultRecovery)
			if err := recovery.ApplySendResult(t.Context(), result, apply); err != nil {
				t.Fatal("native restrictive callback failed")
			}
			assertLegacyArchiveTask(t, f, f.task)
			before := f.localRows(t)
			f.importArchive(t, archive, groups, mode)
			if f.localRows(t) != before {
				t.Error("import rewound native legacy authentication markers")
			}
			var held bool
			if err := f.pool.QueryRow(t.Context(), `SELECT send_recovery_hold AND send_recovery_reason='authentication' FROM email_accounts WHERE id=$1`, f.mailbox).Scan(&held); err != nil || !held {
				t.Fatal("restrictive mailbox hold lost")
			}
			for range 2 {
				if err := recovery.ApplySendResult(t.Context(), result, apply); err != nil {
					t.Fatal(err)
				}
			}
			if calls != 1 || effects != 1 {
				t.Fatalf("authentication callback/effects repeated: %d %d", calls, effects)
			}
			assertLegacyArchiveUnknowns(t, f, 0)
		})
	}
}

func TestLiveImportExistingReceiptExcluded(t *testing.T) {
	f := newSelectiveHoldReviewFixture(t)
	warmup := repository.NewWarmupRepository(f.pool)
	receipt := uuid.New()
	if err := warmup.RecordWarmupReceived(t.Context(), f.mailbox, receipt, "<native-receipt@fixture.invalid>", f.mailbox, false); err != nil {
		t.Fatal("native receipt creation failed")
	}
	groups := []models.OrgDataGroup{models.OrgDataGroupSending, models.OrgDataGroupWarmup}
	archive := f.archive(t, groups)
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal("native archive unreadable")
	}
	for _, entry := range zr.File {
		if entry.Name == dataPath("warmup_received") {
			t.Fatal("native archive exported forbidden source receipt")
		}
	}
	if ExcludedTables["warmup_received"] == "" {
		t.Fatal("receipt exclusion policy absent")
	}
	if err := warmup.RetireWarmupReceived(t.Context(), f.mailbox, receipt); err != nil {
		t.Fatal("native receipt update failed")
	}
	var before, after string
	q := `SELECT to_jsonb(r)::text FROM warmup_received r WHERE email_account_id=$1 AND internal_id=$2`
	if err := f.pool.QueryRow(t.Context(), q, f.mailbox, receipt).Scan(&before); err != nil {
		t.Fatal("local receipt unavailable")
	}
	f.importArchive(t, archive, groups, models.OrgImportConflictOverwrite)
	if err := f.pool.QueryRow(t.Context(), q, f.mailbox, receipt).Scan(&after); err != nil || after != before {
		t.Fatal("excluded local receipt changed")
	}
}

func assertLegacyArchiveUnknowns(t *testing.T, f *selectiveHoldReviewFixture, want int) {
	t.Helper()
	var got int
	if err := f.pool.QueryRow(t.Context(), "SELECT COUNT(*) FROM tasks WHERE email_account_id=$1 AND send_result_state='unknown'", f.mailbox).Scan(&got); err != nil || got != want {
		t.Fatalf("legacy unknown candidates changed: got=%d want=%d err=%v", got, want, err)
	}
}

func assertLegacyArchiveNoHold(t *testing.T, f *selectiveHoldReviewFixture) {
	t.Helper()
	var held bool
	if err := f.pool.QueryRow(t.Context(), `SELECT send_recovery_hold FROM email_accounts WHERE id=$1`, f.mailbox).Scan(&held); err != nil || held {
		t.Fatalf("settled legacy result gained a restrictive hold: %v %v", held, err)
	}
}

func TestLiveImportFreshLegacyWarmupHistoryRetainsPayload(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, sourceHold := range []bool{false, true} {
			t.Run(fmt.Sprintf("existing_%v/source_hold_%v", existing, sourceHold), func(t *testing.T) {
				f, _ := nativeWarmupArchiveFixture(t, false)
				warmup := repository.NewWarmupRepository(f.pool)
				groups := []models.OrgDataGroup{models.OrgDataGroupSending, models.OrgDataGroupWarmup}
				other := uuid.New()
				if sourceHold {
					f.exec(t, `INSERT INTO tasks(id,email_account_id,task_type,status,message_id) VALUES($1,$2,'email','active','')`, other, f.mailbox)
					f.exec(t, `INSERT INTO email_tasks(task_id,to_addrs,subject,body,body_plain,encrypted) VALUES($1,ARRAY['recipient@fixture.invalid'],'Portable fixture','Synthetic body','Synthetic body',false)`, other)
				}
				older := f.archive(t, groups)
				if err := f.repo.UpdateTaskStatus(t.Context(), f.task, "completed"); err != nil {
					t.Fatal(err)
				}
				if err := warmup.IncrementDailyCount(t.Context(), f.mailbox, time.Now()); err != nil {
					t.Fatal(err)
				}
				calls, effects := 0, 0
				result := models.SendEmailResult{TaskID: f.task, Success: true, MessageID: "<portable-legacy@fixture.invalid>", SentAt: time.Now()}
				if err := f.repo.(repository.SendResultRecovery).ApplySendResult(t.Context(), result, func(inner context.Context) error {
					calls++
					repository.AfterSendResultCommit(inner, func(context.Context) { effects++ })
					return warmup.RecordWarmupTokenDelivery(inner, f.task, result.MessageID)
				}); err != nil {
					t.Fatal(err)
				}
				if sourceHold {
					if err := f.repo.UpdateTaskStatus(t.Context(), other, "completed"); err != nil {
						t.Fatal(err)
					}
					if err := f.repo.(repository.SendResultRecovery).ApplySendResult(t.Context(), models.SendEmailResult{TaskID: other}, func(context.Context) error {
						t.Error("unknown source outcome invoked effects")
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}
				archive := f.archive(t, groups)
				f.exec(t, `DELETE FROM email_accounts WHERE id=$1`, f.mailbox)
				if existing {
					f.importArchive(t, older, groups, models.OrgImportConflictOverwrite)
					var unprotected bool
					if err := f.pool.QueryRow(t.Context(), `SELECT t.send_executor_nonce IS NULL AND t.send_result_state IS NULL AND t.send_result_applied_at IS NULL
						AND t.completed_at IS NULL AND t.send_reserved_at IS NULL AND t.send_executor_started_at IS NULL
						AND t.send_executor_result IS NULL AND t.send_result_evidence IS NULL AND t.send_released_at IS NULL
						AND NOT ea.send_recovery_hold AND NOT EXISTS(SELECT 1 FROM outbound_attempts WHERE task_id=t.id)
						AND NOT EXISTS(SELECT 1 FROM send_recovery_resolutions WHERE recovery_task_id=t.id AND recorded_locally_at IS NOT NULL)
						AND EXISTS(SELECT 1 FROM warmup_tasks w WHERE w.task_id=t.id AND w.dispatch_nonce IS NULL
						AND w.dispatch_started_at IS NULL AND w.dispatch_result IS NULL AND w.warmup_charged_date IS NULL
						AND w.warmup_refunded_at IS NULL) FROM tasks t JOIN email_accounts ea ON ea.id=t.email_account_id
						WHERE t.id=$1`, f.task).Scan(&unprotected); err != nil || !unprotected {
						t.Fatalf("older native import unexpectedly protected existing task: %v %v", unprotected, err)
					}
				}
				f.importArchive(t, archive, groups, models.OrgImportConflictOverwrite)
				var history, payload, token, authority bool
				if err := f.pool.QueryRow(t.Context(), `SELECT t.status='completed' AND t.send_result_state='sent',
					EXISTS(SELECT 1 FROM warmup_tasks w WHERE w.task_id=t.id AND w.subject IS NOT NULL),
					EXISTS(SELECT 1 FROM warmup_tokens k WHERE k.task_id=t.id AND k.sent_message_id=$2),
					t.send_executor_nonce IS NOT NULL OR EXISTS(SELECT 1 FROM warmup_tasks w WHERE w.task_id=t.id AND
					(w.dispatch_nonce IS NOT NULL OR w.warmup_charged_date IS NOT NULL OR w.lineage_version<>0))
					FROM tasks t WHERE t.id=$1`, f.task, result.MessageID).Scan(&history, &payload, &token, &authority); err != nil || !history || !payload || !token || authority {
					t.Errorf("native historical overwrite lost payload/token or gained authority: history=%v payload=%v token=%v authority=%v err=%v", history, payload, token, authority, err)
				}
				var count int
				if err := f.pool.QueryRow(t.Context(), `SELECT emails_sent FROM warmup_statistics WHERE email_account_id=$1 AND date=(NOW() AT TIME ZONE 'UTC')::date`, f.mailbox).Scan(&count); err != nil || count != 1 {
					t.Errorf("historical count not imported: %d %v", count, err)
				}
				if sourceHold {
					var historicalUnknown, held, noAuthority bool
					if err := f.pool.QueryRow(t.Context(), `SELECT COALESCE(t.send_result_state='unknown',false),ea.send_recovery_hold AND ea.send_recovery_task_id=t.id,
						t.send_executor_nonce IS NULL AND t.send_executor_started_at IS NULL
						FROM tasks t JOIN email_accounts ea ON ea.id=t.email_account_id WHERE t.id=$1`, other).Scan(&historicalUnknown, &held, &noAuthority); err != nil || !historicalUnknown || !held || !noAuthority {
						t.Errorf("source hold suppressed its own task history or gained authority: unknown=%v held=%v noAuthority=%v err=%v", historicalUnknown, held, noAuthority, err)
					}
					assertLegacyArchiveUnknowns(t, f, 1)
				} else {
					assertLegacyArchiveUnknowns(t, f, 0)
					assertLegacyArchiveNoHold(t, f)
				}
				if calls != 1 || effects != 1 {
					t.Fatalf("import invoked application/postcommit effects: %d %d", calls, effects)
				}
				results, err := f.repo.(interface {
					ListUnappliedSendResults(context.Context, int) ([]models.SendEmailResult, error)
				}).ListUnappliedSendResults(t.Context(), 200)
				if err != nil {
					t.Fatal(err)
				}
				for _, pending := range results {
					if pending.TaskID == f.task || pending.TaskID == other {
						t.Error("imported historical outcome became pending reconciliation")
					}
				}
			})
		}
	}
}
