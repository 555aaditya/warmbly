package orgtransfer

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type observedWholeImportRepository struct {
	repository.OrgTransferRepository
	pid chan uint32
}

func (r *observedWholeImportRepository) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := r.OrgTransferRepository.Begin(ctx)
	if err == nil {
		r.pid <- tx.Conn().PgConn().PID()
	}
	return tx, err
}

func TestLiveImportMailTransportWholeArchiveSendResultLockOrder(t *testing.T) {
	for _, lane := range []string{"import_first", "import_first_external_task_row", "result_first"} {
		t.Run(lane, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			svc, pool, f, mailbox, task, reports := overwriteTransportFixture(t)
			generation := importTransportGeneration(t, pool, mailbox)
			at := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
			result := models.SendEmailResult{TaskID: task, Success: true, SentAt: at, TransportGeneration: generation, MessageID: "native-lock-proof", ProviderMsgID: "provider-lock-proof"}
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE tasks SET send_result_state=NULL,send_executor_result=$2 WHERE id=$1`, task, raw); err != nil {
				t.Fatal(err)
			}
			warm := uuid.New()
			if _, err := pool.Exec(ctx, `INSERT INTO tasks(id,email_account_id,task_type,status,message_id,send_result_state,send_result_applied_at) VALUES($1,$2,'warmup','completed','warm-history','sent',NOW())`, warm, mailbox); err != nil {
				t.Fatal(err)
			}
			warmResult := result
			warmResult.TaskID = warm
			warmRaw, err := json.Marshal(warmResult)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO warmup_tasks(task_id,dispatch_result) VALUES($1,$2)`, warm, warmRaw); err != nil {
				t.Fatal(err)
			}
			audit := uuid.New()
			if _, err := pool.Exec(ctx, `INSERT INTO send_recovery_resolutions(id,organization_id,email_account_id,recovery_task_id,previous_reason,evidence_type,confirmation_reference,confirmed_result,recorded_locally_at) VALUES($1,$2,$3,$4,'unknown','operator_confirmed_sent','Preserve archive lock proof',$5,NOW())`, audit, f.org, mailbox, task, raw); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE email_accounts SET send_recovery_hold=true,send_recovery_reason='authentication',send_recovery_task_id=$2 WHERE id=$1`, mailbox, task); err != nil {
				t.Fatal(err)
			}
			var archive bytes.Buffer
			manifest, err := svc.ExportTo(ctx, f.org, ExportOptions{}, &archive, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(manifest.Groups) != len(models.AllOrgDataGroups) {
				t.Fatal("not whole archive")
			}
			mts := manifestTables(manifest)
			if mts["tasks"].Rows != 2 || mts["warmup_tasks"].Rows != 1 || mts["send_recovery_resolutions"].Rows != 1 {
				t.Fatal("missing populated send history")
			}
			importPID := make(chan uint32, 1)
			service := svc.(*service)
			service.repo = &observedWholeImportRepository{OrgTransferRepository: service.repo, pid: importPID}
			cfg, err := pgxpool.ParseConfig(os.Getenv("WARMBLY_TEST_DB"))
			if err != nil {
				t.Fatal(err)
			}
			cfg.MaxConns = 1
			cfg.MinConns = 0
			cfg.ConnConfig.RuntimeParams["application_name"] = "whole-archive-result-" + task.String()
			resultPool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer resultPool.Close()
			conn, err := resultPool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			resultPID := conn.Conn().PgConn().PID()
			conn.Release()
			recovery := repository.NewTaskRepository(resultPool).(repository.SendResultRecovery)
			importDone, resultDone := make(chan error, 1), make(chan error, 1)
			atTasks, resumeImport, atCallback, resumeResult := make(chan struct{}, 1), make(chan struct{}), make(chan struct{}, 1), make(chan struct{})
			startImport := func() {
				go func() {
					_, err := svc.ImportFrom(ctx, f.org, bytes.NewReader(archive.Bytes()), ImportOptions{Conflict: models.OrgImportConflictOverwrite, ActorUserID: f.owner}, func(_ int, stage string) {
						if stage == "importing tasks" && lane != "result_first" {
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
			startResult := func() {
				go func() {
					resultDone <- recovery.ApplySendResult(ctx, result, func(inner context.Context) error {
						if err := reports.RecordTransportSuccess(inner, mailbox, models.MailTransportSend, at, &task, generation); err != nil {
							return err
						}
						if lane == "result_first" {
							atCallback <- struct{}{}
							select {
							case <-resumeResult:
							case <-ctx.Done():
								return ctx.Err()
							}
						}
						return nil
					})
				}()
			}
			wait := func(q string, args ...any) {
				t.Helper()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					var yes bool
					if err := pool.QueryRow(ctx, q, args...).Scan(&yes); err != nil {
						t.Fatal(err)
					}
					if yes {
						return
					}
					select {
					case <-ticker.C:
					case <-ctx.Done():
						t.Fatal("lock state not reached", ctx.Err())
					}
				}
			}
			snapshot := func(label string, pid uint32) {
				t.Helper()
				var state []byte
				if err := pool.QueryRow(ctx, `SELECT jsonb_build_object('pid',$1::int,'blockers',pg_blocking_pids($1),'locks',(SELECT jsonb_agg(jsonb_build_object('type',locktype,'mode',mode,'granted',granted,'relation',relation::regclass::text,'classid',classid,'objid',objid,'transactionid',transactionid)) FROM pg_locks WHERE pid=$1))`, pid).Scan(&state); err != nil {
					t.Fatal(err)
				}
				t.Logf("%s %s", label, state)
			}
			var ip uint32
			if lane == "result_first" {
				startResult()
				select {
				case <-atCallback:
				case err := <-resultDone:
					t.Fatalf("result did not enter callback: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				startImport()
				select {
				case ip = <-importPID:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				wait(`SELECT $2::int=ANY(pg_blocking_pids($1)) AND EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND NOT granted)`, ip, resultPID)
				snapshot("result owns269/task row; import waits269", resultPID)
				snapshot("import has not reached tasks", ip)
				close(resumeResult)
			} else {
				startImport()
				select {
				case <-atTasks:
				case err := <-importDone:
					t.Fatalf("import did not reach task boundary: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				select {
				case ip = <-importPID:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				var rowGate pgx.Tx
				var gatePID uint32
				if lane == "import_first_external_task_row" {
					rowGate, err = pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer rowGate.Rollback(context.Background())
					gatePID = rowGate.Conn().PgConn().PID()
					var locked uuid.UUID
					if err := rowGate.QueryRow(ctx, `SELECT id FROM tasks WHERE id=$1 FOR UPDATE`, task).Scan(&locked); err != nil {
						t.Fatal(err)
					}
				}
				startResult()
				wait(`SELECT $2::int=ANY(pg_blocking_pids($1)) AND EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND NOT granted)`, resultPID, ip)
				var rowLockBeforeMailbox bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND (locktype='tuple' OR relation='tasks'::regclass AND mode IN('RowShareLock','RowExclusiveLock')))`, resultPID).Scan(&rowLockBeforeMailbox); err != nil {
					t.Fatal(err)
				}
				if rowLockBeforeMailbox {
					t.Fatal("actual ApplySendResult took task row before269")
				}
				snapshot("import owns269/285", ip)
				snapshot("Apply owns268 and tasks AccessShare only; waits269", resultPID)
				close(resumeImport)
				if rowGate != nil {
					wait(`SELECT $2::int=ANY(pg_blocking_pids($1))`, ip, gatePID)
					snapshot("import waits separate row-gate, not result", ip)
					snapshot("separate row-gate has no blockers", gatePID)
					if err := rowGate.Rollback(ctx); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, done := range []<-chan error{importDone, resultDone} {
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("native import/result failed: %T %v", err, err)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			var held, historical bool
			if err := pool.QueryRow(ctx, `SELECT send_recovery_hold AND send_recovery_reason='authentication' AND send_recovery_task_id=$2 FROM email_accounts WHERE id=$1`, mailbox, task).Scan(&held); err != nil || !held {
				t.Fatalf("restrictive hold changed %v %v", held, err)
			}
			if err := pool.QueryRow(ctx, `SELECT recorded_locally_at IS NULL AND confirmation_reference='Preserve archive lock proof' AND confirmed_result->>'message_id'='native-lock-proof' FROM send_recovery_resolutions WHERE id=$1`, audit).Scan(&historical); err != nil || !historical {
				t.Fatalf("historical evidence/local provenance changed %v %v", historical, err)
			}
			if lane != "result_first" {
				var confirmed bool
				if err := pool.QueryRow(ctx, `SELECT send_result_state='sent' AND send_result_applied_at IS NOT NULL FROM tasks WHERE id=$1`, task).Scan(&confirmed); err != nil || !confirmed {
					t.Fatal("late native result did not reconcile", err)
				}
			}
		})
	}
}
