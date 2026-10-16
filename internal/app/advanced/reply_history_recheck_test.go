package advanced

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type historyUnibox struct {
	repository.UniboxRepository
	page repository.HistoricalReplyPage
}

func (u historyUnibox) ListHistoricalReplies(context.Context, time.Time, uuid.UUID) (repository.HistoricalReplyPage, error) {
	return u.page, nil
}

type historyAdvancedRepo struct {
	*incomingReplyAdvancedRepo
	intents      []models.ReplyIntentRecord
	deleted      []uuid.UUID
	failDelete   bool
	failDeleteID uuid.UUID
}

func (r *historyAdvancedRepo) ListReplyIntentsForMessage(_ context.Context, orgID, messageID uuid.UUID) ([]models.ReplyIntentRecord, error) {
	var out []models.ReplyIntentRecord
	for _, in := range r.intents {
		if in.OrganizationID == orgID && in.Metadata["unibox_message_id"] == messageID.String() {
			out = append(out, in)
		}
	}
	return out, nil
}

func (r *historyAdvancedRepo) DeleteReplyIntents(_ context.Context, _ uuid.UUID, ids []uuid.UUID) error {
	if r.failDelete && (r.failDeleteID == uuid.Nil || slices.Contains(ids, r.failDeleteID)) {
		r.failDelete = false
		return errors.New("temporary database failure")
	}
	r.deleted = append(r.deleted, ids...)
	r.intents = slices.DeleteFunc(r.intents, func(in models.ReplyIntentRecord) bool { return slices.Contains(ids, in.ID) })
	return nil
}

type historyCRMRepo struct {
	repository.CRMRepository
	cancelled  []uuid.UUID
	failCancel bool
}

func (r *historyCRMRepo) CancelUntouchedCRMTask(_ context.Context, _, _, taskID uuid.UUID) (bool, error) {
	if r.failCancel {
		r.failCancel = false
		return false, errors.New("temporary task cancellation failure")
	}
	if slices.Contains(r.cancelled, taskID) {
		return false, nil
	}
	r.cancelled = append(r.cancelled, taskID)
	return true, nil
}

// A stored newsletter handled as a reply once its sender was imported left an
// intent and a follow-up task (#960). The cleanup withdraws exactly those: a
// fresh reply from the same sender, or another message's intent, stays.
func TestWithdrawHistoricalReplyEffects(t *testing.T) {
	processed := time.Date(2026, 10, 12, 23, 36, 0, 0, time.UTC)
	orgID, contactID := uuid.New(), uuid.New()
	messageID, taskID := uuid.New(), uuid.New()
	intent := func(subject, action string, at time.Time) models.ReplyIntentRecord {
		return models.ReplyIntentRecord{ID: uuid.New(), OrganizationID: orgID, ContactEmail: "lead@example.org",
			Intent: models.ReplyIntentNeutral, ActionTaken: action, CreatedAt: at,
			Metadata: map[string]interface{}{"subject": subject}}
	}
	fromNewsletter := intent("Big sale", "created_crm_task", processed.Add(time.Second))
	fromNewsletter.Metadata["unibox_message_id"] = messageID.String()
	fromNewsletter.Metadata["crm_task_id"] = taskID.String()
	fromFreshReply := intent("Re: Quick question", "created_crm_task", processed.Add(time.Second))
	sameSubjectLater := intent("Big sale", "created_crm_task", processed.Add(time.Hour))

	repo := &historyAdvancedRepo{incomingReplyAdvancedRepo: &incomingReplyAdvancedRepo{},
		intents: []models.ReplyIntentRecord{fromNewsletter, fromFreshReply, sameSubjectLater}}
	crm := &historyCRMRepo{}
	audit := &recordingAudit{}
	next := uuid.New()
	svc := &service{repo: repo, crmRepo: crm, audit: audit, uniboxRepo: historyUnibox{page: repository.HistoricalReplyPage{
		Replies: []repository.HistoricalReply{{ID: messageID, OrganizationID: orgID, Sender: "lead@example.org",
			Subject: "Big sale", ContactID: contactID, ProcessedAt: processed}},
		NextCursor: next, Done: true,
	}}}

	cursor, done, err := svc.WithdrawHistoricalReplyEffects(context.Background(), processed.AddDate(0, -3, 0), uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if cursor != next || !done {
		t.Errorf("cursor = %v done = %v, want %v and true", cursor, done, next)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != fromNewsletter.ID {
		t.Fatalf("deleted intents %v, want only %v", repo.deleted, fromNewsletter.ID)
	}
	if len(crm.cancelled) != 1 || crm.cancelled[0] != taskID {
		t.Errorf("cancelled tasks %v, want [%v]", crm.cancelled, taskID)
	}
	if len(audit.lifted) != 1 {
		t.Errorf("audited %d cancellations, want 1", len(audit.lifted))
	}
	if _, _, err := svc.WithdrawHistoricalReplyEffects(context.Background(), processed, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if len(crm.cancelled) != 1 || len(audit.lifted) != 1 || len(repo.deleted) != 1 {
		t.Fatal("second cleanup repeated an effect")
	}
}

// An intent that opened no task cancels none.
func TestWithdrawHistoricalReplyEffectsWithoutATask(t *testing.T) {
	processed := time.Now()
	orgID := uuid.New()
	messageID := uuid.New()
	in := models.ReplyIntentRecord{ID: uuid.New(), OrganizationID: orgID, ContactEmail: "lead@example.org",
		Intent: models.ReplyIntentAutomated, CreatedAt: processed, Metadata: map[string]interface{}{"subject": "Thanks", "unibox_message_id": messageID.String()}}
	repo := &historyAdvancedRepo{incomingReplyAdvancedRepo: &incomingReplyAdvancedRepo{}, intents: []models.ReplyIntentRecord{in}}
	crm := &historyCRMRepo{}
	svc := &service{repo: repo, crmRepo: crm, uniboxRepo: historyUnibox{page: repository.HistoricalReplyPage{
		Replies: []repository.HistoricalReply{{ID: messageID, OrganizationID: orgID, Sender: "lead@example.org",
			Subject: "Thanks", ContactID: uuid.New(), ProcessedAt: processed}},
		Done: true,
	}}}
	if _, _, err := svc.WithdrawHistoricalReplyEffects(context.Background(), processed.AddDate(0, -3, 0), uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if len(repo.deleted) != 1 || len(crm.cancelled) != 0 {
		t.Errorf("deleted %v and cancelled %v, want the intent only", repo.deleted, crm.cancelled)
	}
}

// Mail from a contact outside any thread is credited to the last send that
// went out before it arrived, never to a later one.
func TestUnthreadedReplyLooksUpTheSendBeforeIt(t *testing.T) {
	received := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	contact := &models.Contact{ID: uuid.New(), Email: "lead@example.org", CreatedAt: received.AddDate(0, -1, 0)}
	svc, _, _, accountID := newOptOutService(contact)
	progress := svc.campaignProgressRepo.(*incomingReplyProgressRepo)
	msg := inbound(accountID, "Lead <lead@example.org>", "Hello again", "Are you still hiring?", nil)
	msg.InternalDate = received
	if xerr := svc.ProcessIncomingReply(context.Background(), accountID, msg); xerr != nil {
		t.Fatal(xerr)
	}
	if !progress.latestBefore.Equal(received) {
		t.Errorf("latest send looked up before %v, want %v", progress.latestBefore, received)
	}
}
