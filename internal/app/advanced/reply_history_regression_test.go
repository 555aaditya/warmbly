package advanced

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func TestReview988PreservesFreshSameSubjectIntent(t *testing.T) {
	processed := time.Now()
	org, messageID := uuid.New(), uuid.New()
	stale := models.ReplyIntentRecord{ID: uuid.New(), OrganizationID: org, ContactEmail: "lead@example.org", CreatedAt: processed,
		Metadata: map[string]interface{}{"subject": "Hello", "unibox_message_id": messageID.String()}}
	fresh := stale
	fresh.ID = uuid.New()
	fresh.CreatedAt = processed.Add(30 * time.Second)
	fresh.Metadata = map[string]interface{}{"subject": "Hello", "unibox_message_id": uuid.NewString()}
	repo := &historyAdvancedRepo{incomingReplyAdvancedRepo: &incomingReplyAdvancedRepo{}, intents: []models.ReplyIntentRecord{stale, fresh}}
	svc := &service{repo: repo, uniboxRepo: historyUnibox{page: repository.HistoricalReplyPage{
		Replies: []repository.HistoricalReply{{ID: messageID, OrganizationID: org, ContactID: uuid.New(), Sender: stale.ContactEmail, Subject: "Hello", ProcessedAt: processed}}, Done: true,
	}}}
	if _, _, err := svc.WithdrawHistoricalReplyEffects(context.Background(), processed.Add(-time.Hour), uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != stale.ID {
		t.Fatalf("deleted %v, want only stale %v; fresh %v must remain", repo.deleted, stale.ID, fresh.ID)
	}
}

func TestWithdrawHistoricalReplyEffectsSkipsUnattributableLegacy(t *testing.T) {
	messageID, taskID := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name          string
		message, task any
	}{
		{"missing message", nil, taskID.String()},
		{"invalid message", "unknown", taskID.String()},
		{"zero message", uuid.Nil.String(), taskID.String()},
		{"different message", uuid.NewString(), taskID.String()},
		{"missing task", messageID.String(), nil},
		{"invalid task", messageID.String(), "unknown"},
		{"zero task", messageID.String(), uuid.Nil.String()},
		{"non-string task", messageID.String(), 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			org := uuid.New()
			repo := &historyAdvancedRepo{incomingReplyAdvancedRepo: &incomingReplyAdvancedRepo{}, intents: []models.ReplyIntentRecord{{
				ID: uuid.New(), OrganizationID: org, ContactEmail: "lead@example.org", ActionTaken: "created_crm_task",
				Metadata: map[string]interface{}{"subject": "Hello", "unibox_message_id": tc.message, "crm_task_id": tc.task},
			}}}
			crm := &historyCRMRepo{}
			svc := &service{repo: repo, crmRepo: crm, uniboxRepo: historyUnibox{page: repository.HistoricalReplyPage{
				Replies: []repository.HistoricalReply{{ID: messageID, OrganizationID: org, ContactID: uuid.New(), Sender: "lead@example.org", Subject: "Hello", ProcessedAt: time.Now()}}, Done: true,
			}}}
			if _, done, err := svc.WithdrawHistoricalReplyEffects(context.Background(), time.Now(), uuid.Nil); err != nil || !done {
				t.Fatalf("legacy skip: done=%v, err=%v", done, err)
			}
			if len(repo.deleted) != 0 || len(crm.cancelled) != 0 {
				t.Fatalf("unattributable effects changed: deleted=%v cancelled=%v", repo.deleted, crm.cancelled)
			}
		})
	}
}

func TestReview988DoesNotFinishAfterCleanupWriteFailure(t *testing.T) {
	for _, failTask := range []bool{false, true} {
		t.Run(map[bool]string{false: "delete", true: "cancel"}[failTask], func(t *testing.T) {
			org, messageID, taskID, after, next := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
			in := models.ReplyIntentRecord{ID: uuid.New(), OrganizationID: org, ActionTaken: "created_crm_task",
				Metadata: map[string]interface{}{"unibox_message_id": messageID.String(), "crm_task_id": taskID.String()}}
			repo := &historyAdvancedRepo{incomingReplyAdvancedRepo: &incomingReplyAdvancedRepo{}, intents: []models.ReplyIntentRecord{in}, failDelete: !failTask}
			crm, audit := &historyCRMRepo{failCancel: failTask}, &recordingAudit{}
			svc := &service{repo: repo, crmRepo: crm, audit: audit, uniboxRepo: historyUnibox{page: repository.HistoricalReplyPage{
				Replies: []repository.HistoricalReply{{ID: messageID, OrganizationID: org, ContactID: uuid.New()}}, NextCursor: next, Done: true,
			}}}
			cursor, done, err := svc.WithdrawHistoricalReplyEffects(context.Background(), time.Now(), after)
			if err == nil || done || cursor != after {
				t.Fatalf("failed page advanced: cursor=%v done=%v err=%v", cursor, done, err)
			}
			cursor, done, err = svc.WithdrawHistoricalReplyEffects(context.Background(), time.Now(), cursor)
			if err != nil || !done || cursor != next {
				t.Fatalf("retry did not finish: cursor=%v done=%v err=%v", cursor, done, err)
			}
			if len(repo.deleted) != 1 || len(crm.cancelled) != 1 || len(audit.lifted) != 1 {
				t.Fatalf("retry not idempotent: deleted=%v cancelled=%v audit=%v", repo.deleted, crm.cancelled, audit.lifted)
			}
		})
	}
}

func TestWithdrawHistoricalReplyEffectsRetriesTheSamePage(t *testing.T) {
	org, after, next := uuid.New(), uuid.New(), uuid.New()
	repo := &historyAdvancedRepo{incomingReplyAdvancedRepo: &incomingReplyAdvancedRepo{}, failDelete: true}
	page := repository.HistoricalReplyPage{NextCursor: next, Done: true}
	for range 2 {
		messageID := uuid.New()
		page.Replies = append(page.Replies, repository.HistoricalReply{ID: messageID, OrganizationID: org, ContactID: uuid.New()})
		repo.intents = append(repo.intents, models.ReplyIntentRecord{ID: uuid.New(), OrganizationID: org, ActionTaken: "created_crm_task",
			Metadata: map[string]interface{}{"unibox_message_id": messageID.String(), "crm_task_id": uuid.NewString()}})
	}
	repo.failDeleteID = repo.intents[1].ID
	crm, audit := &historyCRMRepo{}, &recordingAudit{}
	svc := &service{repo: repo, crmRepo: crm, audit: audit, uniboxRepo: historyUnibox{page: page}}
	cursor, done, err := svc.WithdrawHistoricalReplyEffects(context.Background(), time.Now(), after)
	if err == nil || done || cursor != after || len(repo.deleted) != 1 {
		t.Fatalf("partially failed page advanced: cursor=%v done=%v deleted=%v err=%v", cursor, done, repo.deleted, err)
	}
	cursor, done, err = svc.WithdrawHistoricalReplyEffects(context.Background(), time.Now(), cursor)
	if err != nil || !done || cursor != next || len(repo.deleted) != 2 || len(crm.cancelled) != 2 || len(audit.lifted) != 2 {
		t.Fatalf("page retry lost or repeated effects: cursor=%v done=%v deleted=%v cancelled=%v audit=%v err=%v", cursor, done, repo.deleted, crm.cancelled, audit.lifted, err)
	}
}

type replyProvenanceRepo struct {
	*incomingReplyAdvancedRepo
	record *models.ReplyIntentRecord
}

func (r *replyProvenanceRepo) CreateReplyIntent(_ context.Context, record *models.ReplyIntentRecord) error {
	r.record = record
	return nil
}

type replyTaskProvenanceRepo struct {
	repository.CRMRepository
	taskID uuid.UUID
	fail   bool
}

func (r replyTaskProvenanceRepo) CreateCRMTask(context.Context, uuid.UUID, uuid.UUID, *models.CreateCRMTask) (*models.CRMTask, error) {
	if r.fail {
		return nil, errors.New("temporary task creation failure")
	}
	return &models.CRMTask{ID: r.taskID}, nil
}

func TestProcessIncomingReplyRecordsExactEffectProvenance(t *testing.T) {
	for _, failTask := range []bool{false, true} {
		t.Run(map[bool]string{false: "created", true: "failed"}[failTask], func(t *testing.T) {
			org, accountID, taskID := uuid.New(), uuid.New(), uuid.New()
			account := &models.Email{ID: accountID, OrganizationID: &org, UserID: uuid.NewString(), Email: "sender@example.test"}
			contact := &models.Contact{ID: uuid.New(), Email: "lead@example.org", CreatedAt: time.Now().Add(-time.Hour)}
			svc, progress := newIncomingReplyService(account, contact, contact.ID)
			repo := &replyProvenanceRepo{incomingReplyAdvancedRepo: progress.advanced}
			svc.repo = repo
			svc.crmRepo = replyTaskProvenanceRepo{taskID: taskID, fail: failTask}
			msg := inbound(accountID, contact.Email, "Re: Hello", "Thanks for writing.", nil)
			msg.InternalDate = time.Now()
			if err := svc.ProcessIncomingReply(context.Background(), accountID, msg); err != nil {
				t.Fatal(err)
			}
			if repo.record == nil || repo.record.Metadata["unibox_message_id"] != msg.ID.String() {
				t.Fatalf("missing message provenance: %+v", repo.record)
			}
			if failTask {
				if _, ok := repo.record.Metadata["crm_task_id"]; ok || repo.record.ActionTaken == "created_crm_task" {
					t.Fatalf("failed task was recorded as created: %+v", repo.record)
				}
			} else if repo.record.Metadata["crm_task_id"] != taskID.String() || repo.record.ActionTaken != "created_crm_task" {
				t.Fatalf("missing task provenance: %+v", repo.record)
			}
		})
	}
}
