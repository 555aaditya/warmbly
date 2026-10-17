package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/infrastructure/pubsub"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type transportWarningPayload struct {
	UserID  string            `json:"user_id"`
	EmailID string            `json:"email_id"`
	Data    map[string]string `json:"data"`
}

type transportWarningBus struct {
	events []transportWarningPayload
}

func (b *transportWarningBus) Publish(_ context.Context, _ string, data interface{}, _ map[string]string) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var event transportWarningPayload
	if err := json.Unmarshal(encoded, &event); err != nil {
		return err
	}
	b.events = append(b.events, event)
	return nil
}

func TestLiveMailTransportConnectionReasonNotificationUsesRecordedEvidence(t *testing.T) {
	for _, op := range []models.MailTransportOperation{models.MailTransportUnknown, models.MailTransportSync} {
		for _, observation := range []string{"current", "missing_generation", "mismatched_generation", "nil_clock", "negative_clock", "future_clock"} {
			for _, userMessage := range []string{"mirrored", "missing", "custom", "private_nil"} {
				t.Run(string(op)+"/"+observation+"/"+userMessage, func(t *testing.T) {
					d := liveDB(t)
					f := newSendResultFixture(t, d)
					if _, err := d.Exec(t.Context(), `INSERT INTO organization_members (organization_id,user_id,role,accepted_at) VALUES ($1,$2,'owner',NOW())`, f.org, f.user); err != nil {
						t.Fatal(err)
					}
					var generation uuid.UUID
					if err := d.QueryRow(t.Context(), `SELECT transport_generation FROM email_accounts WHERE id=$1`, f.mailbox).Scan(&generation); err != nil {
						t.Fatal(err)
					}
					bus := &transportWarningBus{}
					repo := repository.NewEmailAccountErrorRepository(d)
					s := &JobsService{EmailAccountErrorRepository: repo, StreamingPublisher: pubsub.NewStreamingPublisher(bus, repository.NewEventAudienceRepository(d.Pool))}
					mailErr := errx.ErrMailServerUnreachableAt(errx.MailConnectionStageIMAPDial, errors.New(errx.MailConnectionReasonRefused))
					info := mailErr.GetUserErrorInfo()
					event := models.EmailErrorEvent{
						EmailAccountID: f.mailbox.String(), UserID: f.user.String(),
						ErrorCode: string(mailErr.Code), ErrorType: string(mailErr.Type), ResolveMethod: string(mailErr.ResolveMethod),
						Message: mailErr.Message, UserMessage: info.Message, UserTitle: info.Title, UserVisible: true,
						Operation: op, TransportGeneration: generation, Timestamp: time.Now().Add(-time.Minute).Unix(),
					}
					wantMessage, wantSource, wantOperation := mailErr.Message, models.MailTransportObservationSource("worker"), op
					if observation != "current" {
						wantMessage = errx.ErrMailServerUnreachable.Message
					}
					switch observation {
					case "missing_generation":
						event.TransportGeneration, wantOperation = uuid.Nil, models.MailTransportUnknown
					case "mismatched_generation":
						event.TransportGeneration, wantOperation = uuid.New(), models.MailTransportUnknown
					case "nil_clock", "negative_clock", "future_clock":
						wantSource = "receipt"
						event.Timestamp = 0
						if observation == "negative_clock" {
							event.Timestamp = -1
						} else if observation == "future_clock" {
							event.Timestamp = time.Now().Add(time.Hour).Unix()
						}
					}
					wantUserMessage, wantNotification := wantMessage, wantMessage
					switch userMessage {
					case "missing":
						event.UserMessage, wantUserMessage, wantNotification = "", "", ""
						if wantMessage == errx.ErrMailServerUnreachable.Message {
							wantNotification = wantMessage
						}
					case "custom":
						event.UserMessage = "Legacy account warning"
						wantUserMessage, wantNotification = event.UserMessage, event.UserMessage
					case "private_nil":
						event.Message = "Legacy private transport detail"
						wantMessage = event.Message
						event.UserMessage, wantUserMessage, wantNotification = "", "", ""
					}
					for range 2 {
						if err := s.HandleEmailServerError(t.Context(), event); err != nil {
							t.Fatal(err)
						}
					}
					rows, err := repo.GetByAccountID(t.Context(), f.mailbox, true)
					if err != nil || len(rows) != 1 || rows[0].Message != wantMessage || rows[0].Operation != wantOperation || rows[0].ObservationSource != wantSource {
						t.Fatal("stored first warning gained unverified precision or changed scope", err)
					}
					if wantUserMessage == "" && rows[0].UserMessage != nil || wantUserMessage != "" && (rows[0].UserMessage == nil || *rows[0].UserMessage != wantUserMessage) {
						t.Fatal("stored warning changed explicit legacy user text")
					}
					if len(bus.events) != 1 || bus.events[0].UserID != f.user.String() || bus.events[0].EmailID != f.mailbox.String() || bus.events[0].Data["message"] != wantNotification {
						t.Fatal("notification disagreed with stored warning or repeated on redelivery")
					}
					var currentGeneration uuid.UUID
					if err := d.QueryRow(t.Context(), `SELECT transport_generation FROM email_accounts WHERE id=$1`, f.mailbox).Scan(&currentGeneration); err != nil || currentGeneration != generation {
						t.Fatal("diagnostic changed mailbox generation", err)
					}
				})
			}
		}
	}
}

func TestLiveMailTransportConfirmedSendReconcilesOnlySend(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed", true: "rollback"}[rollback], func(t *testing.T) {
			d := liveDB(t)
			s := liveJobsService(d)
			s.EmailAccountErrorRepository = repository.NewEmailAccountErrorRepository(d)
			f := newSendResultFixture(t, d)
			task := f.stampSend(t, s)
			var generation uuid.UUID
			if err := d.QueryRow(t.Context(), `SELECT transport_generation FROM email_accounts WHERE id=$1`, f.mailbox).Scan(&generation); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-time.Hour)
			for _, op := range []models.MailTransportOperation{models.MailTransportUnknown, models.MailTransportSync, models.MailTransportSend} {
				data := &repository.CreateEmailAccountError{TransportGeneration: generation, EmailAccountID: f.mailbox, UserID: f.user, Operation: op, ObservedAt: &old, ErrorCode: "SERVER_UNREACHABLE", Severity: "WARNING", ResolveMethod: "RETRY", Title: "Transport", Message: "Transport report"}
				if op == models.MailTransportSend {
					data.TaskID = &task
				}
				if _, err := s.EmailAccountErrorRepository.CreateOnce(t.Context(), data); err != nil {
					t.Fatal(err)
				}
			}
			result := models.SendEmailResult{TransportGeneration: generation, TaskID: task, SentAt: old.Add(time.Minute), MessageID: "<confirmed@test.local>"}
			if rollback {
				// A real write after recovery fails, forcing the parent reconciliation to roll back.
				result.MessageID = string(make([]byte, 256))
				if err := s.HandleEmailSent(t.Context(), result); err == nil {
					t.Fatal("invalid database text unexpectedly reconciled")
				}
			} else if err := s.HandleEmailSent(t.Context(), result); err != nil {
				t.Fatal(err)
			}
			rows, err := s.EmailAccountErrorRepository.GetByAccountID(t.Context(), f.mailbox, true)
			if err != nil {
				t.Fatal(err)
			}
			want := 2
			if rollback {
				want = 3
			}
			if len(rows) != want {
				t.Fatalf("unresolved=%d want=%d", len(rows), want)
			}
			var n int
			if err := d.QueryRow(t.Context(), `SELECT count(*) FROM email_account_transport_recovery WHERE email_account_id=$1`, f.mailbox).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if rollback && n != 0 || !rollback && n != 1 {
				t.Fatalf("recovery transaction did not settle atomically: %d", n)
			}
			if !rollback {
				var source uuid.UUID
				if err := d.QueryRow(t.Context(), `SELECT source_task_id FROM email_account_transport_recovery WHERE email_account_id=$1 AND operation='send'`, f.mailbox).Scan(&source); err != nil || source != task {
					t.Fatal("confirmed task provenance missing", err)
				}
				for _, row := range rows {
					if row.Operation == models.MailTransportSend {
						t.Fatal("send transport did not recover")
					}
				}
			}
		})
	}
}

func TestLiveMailTransportConfirmedSendAndSyncClearLegacyReports(t *testing.T) {
	for _, syncFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "send_first", true: "sync_first"}[syncFirst], func(t *testing.T) {
			d := liveDB(t)
			s := liveJobsService(d)
			s.EmailAccountErrorRepository = repository.NewEmailAccountErrorRepository(d)
			s.EmailSyncStateRepository = repository.NewEmailSyncStateRepository(d)
			f := newSendResultFixture(t, d)
			task := f.stampSend(t, s)
			var generation uuid.UUID
			if err := d.QueryRow(t.Context(), `SELECT transport_generation FROM email_accounts WHERE id=$1`, f.mailbox).Scan(&generation); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-5 * 24 * time.Hour).UTC().Truncate(time.Second)
			if _, err := d.Exec(t.Context(), `INSERT INTO email_account_errors(id,email_account_id,user_id,error_code,severity,resolve_method,title,message,created_at,task_id)
			    VALUES($1,$2,$3,'SERVER_UNREACHABLE','WARNING','RETRY','Connection error','Temporary SMTP failure',$4,$5)`, uuid.New(), f.mailbox, f.user, old, task); err != nil {
				t.Fatal(err)
			}
			synced := old.Add(4 * 24 * time.Hour)
			sync := func() error {
				return s.HandleSyncState(t.Context(), &models.JobEventSyncState{UserID: f.user, EmailID: f.mailbox, State: models.SyncState{TransportGeneration: generation, LastSyncedAt: &synced}})
			}
			send := func() error {
				return s.HandleEmailSent(t.Context(), models.SendEmailResult{TransportGeneration: generation, TaskID: task, SentAt: synced.Add(-time.Hour), MessageID: "<legacy-recovered@test.local>"})
			}
			first, second := send, sync
			if syncFirst {
				first, second = sync, send
			}
			if err := first(); err != nil {
				t.Fatal(err)
			}
			rows, err := s.EmailAccountErrorRepository.GetByAccountID(t.Context(), f.mailbox, true)
			if err != nil || len(rows) != 1 {
				t.Fatal("one operation guessed the old failure's scope", rows, err)
			}
			if err := second(); err != nil {
				t.Fatal(err)
			}
			byAccount, err := s.EmailAccountErrorRepository.GetByAccountIDs(t.Context(), []uuid.UUID{f.mailbox}, true)
			if err != nil || len(byAccount[f.mailbox]) != 0 {
				t.Fatal("confirmed recovery left the legacy report visible to account health", byAccount, err)
			}
			rows, err = s.EmailAccountErrorRepository.GetByAccountID(t.Context(), f.mailbox, false)
			if err != nil || len(rows) != 1 || rows[0].ResolvedAt == nil || !rows[0].CreatedAt.Equal(old) || rows[0].Operation != models.MailTransportUnknown {
				t.Fatal("recovery lost or reclassified the historical failure", rows, err)
			}
		})
	}
}

func TestLiveMailTransportUnreadableSyncStateCannotAcquireRecovery(t *testing.T) {
	d := liveDB(t)
	f := newSendResultFixture(t, d)
	stateRepo := repository.NewEmailSyncStateRepository(d)
	errRepo := repository.NewEmailAccountErrorRepository(d)
	s := &JobsService{EmailSyncStateRepository: stateRepo, EmailAccountErrorRepository: errRepo}
	var generation uuid.UUID
	if err := d.QueryRow(t.Context(), `SELECT transport_generation FROM email_accounts WHERE id=$1`, f.mailbox).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)
	if err := stateRepo.Put(t.Context(), f.user, f.mailbox, &models.SyncState{LastSyncedAt: &at}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(t.Context(), `UPDATE email_sync_state SET backfill_cursor='{"google_recovery":"unreadable"}' WHERE email_id=$1`, f.mailbox); err != nil {
		t.Fatal(err)
	}
	if _, err := stateRepo.Get(t.Context(), f.mailbox); err == nil {
		t.Fatal("fixture did not fail the native previous-state read")
	}
	observed := at.Add(-time.Minute)
	if _, err := errRepo.CreateOnce(t.Context(), &repository.CreateEmailAccountError{EmailAccountID: f.mailbox, UserID: f.user, Operation: models.MailTransportSync, TransportGeneration: generation, ObservedAt: &observed, ErrorCode: "SERVER_UNREACHABLE", Severity: "WARNING", ResolveMethod: "RETRY", Title: "Transport", Message: "Transport report"}); err != nil {
		t.Fatal(err)
	}
	counts := func() (int, int) {
		t.Helper()
		var recovery, unresolved int
		if err := d.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM email_account_transport_recovery WHERE email_account_id=$1),(SELECT count(*) FROM email_account_errors WHERE email_account_id=$1 AND resolved_at IS NULL)`, f.mailbox).Scan(&recovery, &unresolved); err != nil {
			t.Fatal(err)
		}
		return recovery, unresolved
	}
	event := &models.JobEventSyncState{EmailID: f.mailbox, UserID: f.user, State: models.SyncState{TransportGeneration: generation, LastSyncedAt: &at}}
	err := s.HandleSyncState(t.Context(), event)
	if recovery, unresolved := counts(); err == nil || recovery != 0 || unresolved != 1 {
		t.Fatalf("unreadable equal-timestamp relay was accepted: err=%v recovery=%d unresolved=%d", err, recovery, unresolved)
	}
	if _, err := stateRepo.Get(t.Context(), f.mailbox); err == nil {
		t.Fatal("failed relay overwrote unreadable previous state")
	}
	if _, err := d.Exec(t.Context(), `UPDATE email_sync_state SET backfill_cursor='{}' WHERE email_id=$1`, f.mailbox); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleSyncState(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if recovery, unresolved := counts(); recovery != 0 || unresolved != 1 {
		t.Fatal("readable equal-timestamp retry acquired recovery")
	}
	fresh := at.Add(10 * time.Second)
	event.State.LastSyncedAt = &fresh
	if err := s.HandleSyncState(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if recovery, unresolved := counts(); recovery != 1 || unresolved != 0 {
		t.Fatal("fresh typed completed pass failed to recover")
	}
}
