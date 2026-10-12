package orgtransfer

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/encrypt"
	"github.com/warmbly/warmbly/internal/repository"
	"github.com/warmbly/warmbly/internal/tasks"
)

type selectiveHoldReviewFixture struct {
	pool                         *pgxpool.Pool
	svc                          Service
	tenant                       tenantFixture
	mailbox, task, worker, nonce uuid.UUID
	repo                         repository.TaskRepository
	admission                    repository.OutboundAdmissionRepository
	validator                    interface {
		ValidateOutboundExecution(context.Context, uuid.UUID) error
	}
}

func newSelectiveHoldReviewFixture(t *testing.T) *selectiveHoldReviewFixture {
	t.Helper()
	_, pool := liveImportService(t)
	f := &selectiveHoldReviewFixture{pool: pool, tenant: newTenantFixture(t, pool), mailbox: uuid.New(), task: uuid.New(), worker: uuid.New()}
	f.exec(t, `INSERT INTO fleet_nodes(id,role,active,last_seen_at,warmup_send_protocol) VALUES($1,'worker',true,NOW(),2)`, f.worker)
	f.exec(t, `INSERT INTO workers(id) VALUES($1)`, f.worker)
	f.exec(t, `INSERT INTO email_accounts(id,user_id,organization_id,email,name,signature_plain,signature_html,provider,status,worker_id,timezone,campaign_limit,warmup_max,shared_daily_limit,rolling_recipient_limit)
	 VALUES($1,$2,$3,$4,'Local archive fixture','','','smtp_imap','active',$5,'UTC',50,50,100,100)`, f.mailbox, f.tenant.owner, f.tenant.org, f.mailbox.String()+"@example.test", f.worker)
	f.exec(t, `INSERT INTO tasks(id,email_account_id,task_type,status,message_id) VALUES($1,$2,'email','active','')`, f.task, f.mailbox)
	f.exec(t, `INSERT INTO email_tasks(task_id,to_addrs,subject,body,body_plain,encrypted) VALUES($1,ARRAY['recipient@fixture.invalid'],'Local fixture','Synthetic local body','Synthetic local body',false)`, f.task)
	enc, err := encrypt.NewEncrypter([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	password, err := enc.Encrypt("unused-local-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO email_accounts_smtp_imap(email_account_id,smtp_host,smtp_port,smtp_user,smtp_password,imap_host,imap_port,imap_user,imap_password)
	 VALUES($1,'smtp.fixture.invalid',587,'synthetic',$2,'imap.fixture.invalid',993,'synthetic',$2)`, f.mailbox, password)
	f.svc = NewService(repository.NewOrgTransferRepository(&db.DB{Pool: pool}), nil, enc, nil, InstanceInfo{})
	f.repo = repository.NewTaskRepository(pool)
	f.admission = f.repo.(repository.OutboundAdmissionRepository)
	emailRepo := repository.NewEmailRepostory(&db.DB{Pool: pool}, enc)
	f.validator = tasks.NewService(nil, nil, nil, nil, nil, nil, nil, nil, nil, f.repo, nil, nil, nil, nil, emailRepo, nil, nil, nil, nil, nil, nil, nil).(interface {
		ValidateOutboundExecution(context.Context, uuid.UUID) error
	})
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM email_accounts WHERE organization_id=$1`, f.tenant.org); err != nil {
			t.Error(err)
		}
		if _, err := pool.Exec(context.Background(), `DELETE FROM fleet_nodes WHERE id=$1`, f.worker); err != nil {
			t.Error(err)
		}
	})
	return f
}

func (f *selectiveHoldReviewFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), q, args...); err != nil {
		t.Fatal(err)
	}
}

func (f *selectiveHoldReviewFixture) archive(t *testing.T, groups []models.OrgDataGroup) []byte {
	t.Helper()
	var buf bytes.Buffer
	manifest, err := f.svc.ExportTo(t.Context(), f.tenant.org, ExportOptions{Groups: groups, Passphrase: "synthetic-review-passphrase"}, &buf, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Groups) == 0 || manifest.Groups[0] != models.OrgDataGroupCore {
		t.Fatal("actual export did not force core")
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	core := false
	for _, entry := range zr.File {
		if entry.Name == dataPath("email_accounts") {
			core = true
		}
	}
	if !core {
		t.Fatal("native selective archive omitted mailbox core entry")
	}
	return buf.Bytes()
}

func (f *selectiveHoldReviewFixture) importArchive(t *testing.T, raw []byte, groups []models.OrgDataGroup, conflict models.OrgImportConflict) {
	t.Helper()
	result, err := f.svc.ImportFrom(t.Context(), f.tenant.org, bytes.NewReader(raw), ImportOptions{Groups: groups, Passphrase: "synthetic-review-passphrase", Conflict: conflict, ActorUserID: f.tenant.owner}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.SecretsApplied {
		t.Fatal("native encrypted credentials were not applied")
	}
	t.Logf("actual ExportTo/ImportFrom groups=%v conflict=%s applied=%v warnings=%v", groups, conflict, result.RowCounts, result.Warnings)
}

func (f *selectiveHoldReviewFixture) reserve(t *testing.T) {
	t.Helper()
	var err error
	f.nonce, err = f.admission.ReserveOutbound(t.Context(), repository.OutboundReservation{TaskID: f.task, MailboxID: f.mailbox, OrganizationID: f.tenant.org, WorkerID: f.worker, Provider: models.InboxProviderSMTPIMAP, Recipients: []string{"recipient@fixture.invalid"}})
	if err != nil {
		t.Fatal(err)
	}
}

func (f *selectiveHoldReviewFixture) begin(t *testing.T) {
	t.Helper()
	if err := f.validator.ValidateOutboundExecution(t.Context(), f.task); err != nil {
		t.Fatalf("real generic execution validation refused fixture: %v", err)
	}
	state, err := f.admission.BeginOutbound(t.Context(), f.task, f.mailbox, f.worker, f.nonce)
	if err != nil || state.State != "execute" {
		t.Fatalf("native start=%+v error=%v", state, err)
	}
}

func (f *selectiveHoldReviewFixture) result() models.SendEmailResult {
	return models.SendEmailResult{TaskID: f.task, Success: true, MessageID: "<synthetic-result@fixture.invalid>"}
}

func (f *selectiveHoldReviewFixture) finish(t *testing.T) {
	t.Helper()
	if err := f.admission.FinishOutbound(t.Context(), f.task, f.mailbox, f.worker, f.result()); err != nil {
		t.Fatal(err)
	}
}

func (f *selectiveHoldReviewFixture) snapshot(t *testing.T) string {
	t.Helper()
	var raw string
	if err := f.pool.QueryRow(t.Context(), `SELECT jsonb_build_object('mailbox_held',ea.send_recovery_hold,'mailbox_reason',ea.send_recovery_reason,'held_task',ea.send_recovery_task_id,'mailbox_status',ea.status,'worker',ea.worker_id,'task_status',t.status,'task_state',t.send_result_state,'reserved',t.send_reserved_at,'released',t.send_released_at,'started',t.send_executor_started_at,'nonce',t.send_executor_nonce,'durable_result',t.send_executor_result IS NOT NULL,'applied',t.send_result_applied_at,'attempts',(SELECT count(*) FROM outbound_attempts WHERE task_id=t.id))::text FROM email_accounts ea LEFT JOIN tasks t ON t.id=$2 WHERE ea.id=$1`, f.mailbox, f.task).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestLiveReviewSelectiveExportMustPreserveLocalUnknownHold(t *testing.T) {
	for _, group := range []models.OrgDataGroup{models.OrgDataGroupCore, models.OrgDataGroupSending} {
		for _, conflict := range []models.OrgImportConflict{models.OrgImportConflictSkip, models.OrgImportConflictOverwrite} {
			for _, phase := range []string{"reserved", "started", "durable_result"} {
				t.Run(string(group)+"/"+string(conflict)+"/"+phase, func(t *testing.T) {
					f := newSelectiveHoldReviewFixture(t)
					groups := []models.OrgDataGroup{group}
					archive := f.archive(t, groups)
					f.reserve(t)
					if phase != "reserved" {
						f.begin(t)
					}
					if phase == "durable_result" {
						f.finish(t)
					}
					before := f.snapshot(t)
					beforeRows := f.localRows(t)
					f.importArchive(t, archive, groups, conflict)
					after := f.snapshot(t)
					if beforeRows != f.localRows(t) {
						t.Error("archive changed the complete local task, typed payload, reservation, durable outcome or evidence")
					}
					t.Logf("before=%s after=%s", before, after)
					var held, hasNonce, started bool
					var outcome *string
					if err := f.pool.QueryRow(t.Context(), `SELECT ea.send_recovery_hold,t.send_executor_nonce=$3,t.send_executor_started_at IS NOT NULL,t.send_result_state::text FROM email_accounts ea JOIN tasks t ON t.id=$2 WHERE ea.id=$1`, f.mailbox, f.task, f.nonce).Scan(&held, &hasNonce, &started, &outcome); err != nil {
						t.Fatal(err)
					}
					if !held {
						t.Error("actual native archive overwrite removed current locally reserved/started unknown hold without evidence")
					}
					if !hasNonce || started != (phase != "reserved") {
						t.Error("local executor authority unexpectedly lost")
					}
					if outcome == nil || *outcome != "unknown" {
						t.Error("actual native archive overwrote locally unresolved task outcome")
					}
					admit, err := f.repo.(repository.SendResultRecovery).GetSendAdmission(t.Context(), f.tenant.org, f.mailbox, models.InboxProviderSMTPIMAP, time.Now())
					if err != nil {
						t.Fatal(err)
					}
					if admit.Allowed {
						t.Error("mailbox send admission reopened while local unresolved executor authority remains")
					}
					if !held {
						newTask := uuid.New()
						f.exec(t, `INSERT INTO tasks(id,email_account_id,task_type,status,message_id) VALUES($1,$2,'email','active','')`, newTask, f.mailbox)
						f.exec(t, `INSERT INTO email_tasks(task_id,to_addrs,subject,body,body_plain,encrypted) VALUES($1,ARRAY['another@fixture.invalid'],'Another local fixture','Synthetic body','Synthetic body',false)`, newTask)
						_, err := f.admission.ReserveOutbound(t.Context(), repository.OutboundReservation{TaskID: newTask, MailboxID: f.mailbox, OrganizationID: f.tenant.org, WorkerID: f.worker, Provider: models.InboxProviderSMTPIMAP, Recipients: []string{"another@fixture.invalid"}})
						t.Logf("new outbound reservation after import err=%v", err)
					}
				})
			}
		}
	}
}

func TestLiveReviewSelectiveExportLateLocalResultRemainsAccepted(t *testing.T) {
	for _, group := range []models.OrgDataGroup{models.OrgDataGroupCore, models.OrgDataGroupSending} {
		t.Run(string(group), func(t *testing.T) {
			f := newSelectiveHoldReviewFixture(t)
			groups := []models.OrgDataGroup{group}
			archive := f.archive(t, groups)
			f.reserve(t)
			f.begin(t)
			f.importArchive(t, archive, groups, models.OrgImportConflictOverwrite)
			f.finish(t)
			recovery := f.repo.(repository.SendResultRecovery)
			calls := 0
			for range 2 {
				if err := recovery.ApplySendResult(t.Context(), f.result(), func(context.Context) error { calls++; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			var outcome string
			var applied bool
			if err := f.pool.QueryRow(t.Context(), `SELECT send_result_state::text,send_result_applied_at IS NOT NULL FROM tasks WHERE id=$1`, f.task).Scan(&outcome, &applied); err != nil {
				t.Fatal(err)
			}
			if outcome != "sent" || !applied || calls != 1 {
				t.Fatalf("valid local started callback lost: outcome=%s applied=%v calls=%d", outcome, applied, calls)
			}
			t.Logf("synthetic typed native callback accepted exactly once: %s", f.snapshot(t))
		})
	}
}

func TestLiveReviewSelectiveExportNewImportIsHistoryNotAuthority(t *testing.T) {
	for _, phase := range []string{"started_unknown", "applied_sent"} {
		for _, group := range []models.OrgDataGroup{models.OrgDataGroupCore, models.OrgDataGroupSending} {
			t.Run(phase+"/"+string(group), func(t *testing.T) {
				f := newSelectiveHoldReviewFixture(t)
				f.reserve(t)
				f.begin(t)
				if phase == "applied_sent" {
					f.finish(t)
					if err := f.repo.(repository.SendResultRecovery).ApplySendResult(t.Context(), f.result(), func(context.Context) error { return nil }); err != nil {
						t.Fatal(err)
					}
				}
				groups := []models.OrgDataGroup{group}
				archive := f.archive(t, groups)
				f.exec(t, `DELETE FROM email_accounts WHERE id=$1`, f.mailbox)
				f.importArchive(t, archive, groups, models.OrgImportConflictOverwrite)
				var held, exists, local bool
				if err := f.pool.QueryRow(t.Context(), `SELECT ea.send_recovery_hold,EXISTS(SELECT 1 FROM tasks WHERE id=$2),EXISTS(SELECT 1 FROM tasks WHERE id=$2 AND (send_executor_nonce IS NOT NULL OR send_executor_started_at IS NOT NULL OR send_executor_result IS NOT NULL)) FROM email_accounts ea WHERE ea.id=$1`, f.mailbox, f.task).Scan(&held, &exists, &local); err != nil {
					t.Fatal(err)
				}
				if held != (phase == "started_unknown") || exists != (group == models.OrgDataGroupSending) || local {
					t.Fatalf("new import confused history with local proof: held=%v task_exists=%v executor=%v", held, exists, local)
				}
				var payload bool
				if err := f.pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM email_tasks WHERE task_id=$1)`, f.task).Scan(&payload); err != nil || payload != exists {
					t.Fatalf("fresh historical task lost its portable typed payload: payload=%v task=%v err=%v", payload, exists, err)
				}
				results, err := f.repo.(interface {
					ListUnappliedSendResults(context.Context, int) ([]models.SendEmailResult, error)
				}).ListUnappliedSendResults(t.Context(), 200)
				if err != nil {
					t.Fatal(err)
				}
				for _, result := range results {
					if result.TaskID == f.task {
						t.Fatal("imported stored source result gained reconciliation authority")
					}
				}
				if phase == "started_unknown" && group == models.OrgDataGroupCore {
					var heldTask *uuid.UUID
					if err := f.pool.QueryRow(t.Context(), `SELECT send_recovery_task_id FROM email_accounts WHERE id=$1`, f.mailbox).Scan(&heldTask); err != nil {
						t.Fatal(err)
					}
					t.Logf("core-only unknown history retained hold but has no task row: held_task=%v", heldTask)
				}
				t.Logf("new imported snapshot=%s", f.snapshot(t))
			})
		}
	}
}
