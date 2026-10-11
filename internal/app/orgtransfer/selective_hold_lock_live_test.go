package orgtransfer

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func TestLiveImportLocalResultPreservationLockOrder(t *testing.T) {
	for _, order := range []string{"import_first", "import_first_external_row", "result_first"} {
		t.Run(order, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
			defer cancel()
			f := newSelectiveHoldReviewFixture(t)
			groups := []models.OrgDataGroup{models.OrgDataGroupSending}
			archive := f.archive(t, groups)
			f.reserve(t)
			f.begin(t)
			f.finish(t)
			result := f.result()
			var generation string
			if err := f.pool.QueryRow(ctx, `SELECT transport_generation::text FROM email_accounts WHERE id=$1`, f.mailbox).Scan(&generation); err != nil {
				t.Fatal(err)
			}
			cfg, err := pgxpool.ParseConfig(os.Getenv("WARMBLY_TEST_DB"))
			if err != nil {
				t.Fatal(err)
			}
			cfg.MaxConns, cfg.MinConns = 1, 0
			resultPool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer resultPool.Close()
			conn, err := resultPool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			rpid := conn.Conn().PgConn().PID()
			conn.Release()
			rrepo := repository.NewTaskRepository(resultPool)
			recovery := rrepo.(repository.SendResultRecovery)
			reports := repository.NewEmailAccountErrorRepository(&db.DB{Pool: resultPool})
			importPID := make(chan uint32, 1)
			service := f.svc.(*service)
			service.repo = &observedWholeImportRepository{OrgTransferRepository: service.repo, pid: importPID}
			atTasks, atResult := make(chan struct{}, 1), make(chan struct{}, 1)
			resumeImport, resumeResult := make(chan struct{}), make(chan struct{})
			importDone, resultDone := make(chan error, 1), make(chan error, 1)
			calls := 0
			apply := func(inner context.Context) error {
				calls++
				if err := rrepo.UpdateTaskMessageID(inner, f.task, result.MessageID); err != nil {
					return err
				}
				if err := rrepo.UpdateTaskStatus(inner, f.task, "completed"); err != nil {
					return err
				}
				gen, err := uuid.Parse(generation)
				if err != nil {
					return err
				}
				if xerr := reports.RecordTransportSuccess(inner, f.mailbox, models.MailTransportSend, time.Now().Add(-time.Second), &f.task, gen); xerr != nil {
					return xerr
				}
				if order == "result_first" && calls == 1 {
					atResult <- struct{}{}
					select {
					case <-resumeResult:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return nil
			}
			startImport := func() {
				go func() {
					_, err := f.svc.ImportFrom(ctx, f.tenant.org, bytes.NewReader(archive), ImportOptions{Groups: groups, Passphrase: "synthetic-review-passphrase", Conflict: models.OrgImportConflictOverwrite, ActorUserID: f.tenant.owner}, func(_ int, stage string) {
						if stage == "importing tasks" && order != "result_first" {
							atTasks <- struct{}{}
							select {
							case <-resumeImport:
							case <-ctx.Done():
							}
						}
					})
					importDone <- err
				}()
			}
			startResult := func() { go func() { resultDone <- recovery.ApplySendResult(ctx, result, apply) }() }
			waitBlocked := func(waiter, holder uint32) {
				t.Helper()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					var yes bool
					if err := f.pool.QueryRow(ctx, `SELECT $2::int=ANY(pg_blocking_pids($1))`, waiter, holder).Scan(&yes); err != nil {
						t.Fatal(err)
					}
					if yes {
						return
					}
					select {
					case <-ticker.C:
					case <-ctx.Done():
						t.Fatal("expected lock blocker not reached", ctx.Err())
					}
				}
			}
			if order == "result_first" {
				startResult()
				select {
				case <-atResult:
				case err := <-resultDone:
					t.Fatalf("result did not reach callback: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				startImport()
				ip := <-importPID
				waitBlocked(ip, rpid)
				t.Log("actual result owns268/269/285 and task/mailbox rows; import waits before its row writes")
				close(resumeResult)
			} else {
				startImport()
				select {
				case <-atTasks:
				case err := <-importDone:
					t.Fatalf("import did not reach tasks: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				ip := <-importPID
				var gate pgx.Tx
				if order == "import_first_external_row" {
					gate, err = f.pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer gate.Rollback(context.Background())
					if _, err := gate.Exec(ctx, `SELECT id FROM tasks WHERE id=$1 FOR UPDATE`, f.task); err != nil {
						t.Fatal(err)
					}
				}
				startResult()
				waitBlocked(rpid, ip)
				var rowBeforeMailbox bool
				if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND (locktype='tuple' OR relation='tasks'::regclass AND mode IN('RowShareLock','RowExclusiveLock')))`, rpid).Scan(&rowBeforeMailbox); err != nil {
					t.Fatal(err)
				}
				if rowBeforeMailbox {
					t.Fatal("real ApplySendResult took a task row while waiting for mailbox269")
				}
				t.Log("actual import owns269/285; result owns268 but no task row and waits269")
				close(resumeImport)
				if gate != nil {
					waitBlocked(ip, gate.Conn().PgConn().PID())
					if err := gate.Rollback(ctx); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, done := range []<-chan error{importDone, resultDone} {
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("import/result did not settle", ctx.Err())
				}
			}
			var preserved bool
			if err := f.pool.QueryRow(ctx, `SELECT COALESCE(send_result_state='sent' AND send_result_applied_at IS NOT NULL AND status='completed' AND message_id=$2,false) FROM tasks WHERE id=$1`, f.task, result.MessageID).Scan(&preserved); err != nil {
				t.Fatal(err)
			}
			if !preserved {
				t.Error("older archive rewound real committed result state or result application message")
			}
			if err := recovery.ApplySendResult(ctx, result, apply); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Errorf("result application repeated after archive overwrite: %d", calls)
			}
		})
	}
}
