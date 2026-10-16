package advanced

import (
	"context"
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
	intents []models.ReplyIntentRecord
	deleted []uuid.UUID
}

func (r *historyAdvancedRepo) ListReplyIntentsFor(_ context.Context, _ uuid.UUID, email, subject string, from, to time.Time) ([]models.ReplyIntentRecord, error) {
	var out []models.ReplyIntentRecord
	for _, in := range r.intents {
		if in.ContactEmail == email && in.Metadata["subject"] == subject && !in.CreatedAt.Before(from) && !in.CreatedAt.After(to) {
			out = append(out, in)
		}
	}
	return out, nil
}

func (r *historyAdvancedRepo) DeleteReplyIntents(_ context.Context, _ uuid.UUID, ids []uuid.UUID) error {
	r.deleted = append(r.deleted, ids...)
	return nil
}

type historyCRMRepo struct {
	repository.CRMRepository
	titles []string
}

func (r *historyCRMRepo) CancelUntouchedCRMTasks(_ context.Context, _, _ uuid.UUID, title string, _, _ time.Time) ([]uuid.UUID, error) {
	r.titles = append(r.titles, title)
	return []uuid.UUID{uuid.New()}, nil
}

// A stored newsletter handled as a reply once its sender was imported left an
// intent and a follow-up task (#960). The cleanup withdraws exactly those: a
// fresh reply from the same sender, or another message's intent, stays.
func TestWithdrawHistoricalReplyEffects(t *testing.T) {
	processed := time.Date(2026, 10, 12, 23, 36, 0, 0, time.UTC)
	orgID, contactID := uuid.New(), uuid.New()
	intent := func(subject, action string, at time.Time) models.ReplyIntentRecord {
		return models.ReplyIntentRecord{ID: uuid.New(), OrganizationID: orgID, ContactEmail: "lead@example.org",
			Intent: models.ReplyIntentNeutral, ActionTaken: action, CreatedAt: at,
			Metadata: map[string]interface{}{"subject": subject}}
	}
	fromNewsletter := intent("Big sale", "created_crm_task", processed.Add(time.Second))
	fromFreshReply := intent("Re: Quick question", "created_crm_task", processed.Add(time.Second))
	sameSubjectLater := intent("Big sale", "created_crm_task", processed.Add(time.Hour))

	repo := &historyAdvancedRepo{incomingReplyAdvancedRepo: &incomingReplyAdvancedRepo{},
		intents: []models.ReplyIntentRecord{fromNewsletter, fromFreshReply, sameSubjectLater}}
	crm := &historyCRMRepo{}
	audit := &recordingAudit{}
	next := uuid.New()
	svc := &service{repo: repo, crmRepo: crm, audit: audit, uniboxRepo: historyUnibox{page: repository.HistoricalReplyPage{
		Replies: []repository.HistoricalReply{{ID: uuid.New(), OrganizationID: orgID, Sender: "lead@example.org",
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
	if want := "Follow up: reply from lead@example.org"; len(crm.titles) != 1 || crm.titles[0] != want {
		t.Errorf("cancelled tasks titled %v, want [%q]", crm.titles, want)
	}
	if len(audit.lifted) != 1 {
		t.Errorf("audited %d cancellations, want 1", len(audit.lifted))
	}
}

// An intent that opened no task cancels none.
func TestWithdrawHistoricalReplyEffectsWithoutATask(t *testing.T) {
	processed := time.Now()
	orgID := uuid.New()
	in := models.ReplyIntentRecord{ID: uuid.New(), OrganizationID: orgID, ContactEmail: "lead@example.org",
		Intent: models.ReplyIntentAutomated, CreatedAt: processed, Metadata: map[string]interface{}{"subject": "Thanks"}}
	repo := &historyAdvancedRepo{incomingReplyAdvancedRepo: &incomingReplyAdvancedRepo{}, intents: []models.ReplyIntentRecord{in}}
	crm := &historyCRMRepo{}
	svc := &service{repo: repo, crmRepo: crm, uniboxRepo: historyUnibox{page: repository.HistoricalReplyPage{
		Replies: []repository.HistoricalReply{{ID: uuid.New(), OrganizationID: orgID, Sender: "lead@example.org",
			Subject: "Thanks", ContactID: uuid.New(), ProcessedAt: processed}},
		Done: true,
	}}}
	if _, _, err := svc.WithdrawHistoricalReplyEffects(context.Background(), processed.AddDate(0, -3, 0), uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if len(repo.deleted) != 1 || len(crm.titles) != 0 {
		t.Errorf("deleted %v and cancelled %v, want the intent only", repo.deleted, crm.titles)
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
