package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

// The historical reply cleanup finds mail handled as a reply after its
// sender became a contact though it arrived before, outside every campaign
// thread, and withdraws only what that message left (#960).
//
//	WARMBLY_TEST_DB=postgres://warmbly:warmbly@localhost:15432/<db>?sslmode=disable \
//	  go test ./internal/repository/ -run LiveHistoricalReply -v
func TestLiveHistoricalReplyCleanupQueries(t *testing.T) {
	handle, pool := liveContactDB(t)
	f := newSharedOrgFixture(t, pool)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("fixture %q: %v", sql[:min(60, len(sql))], err)
		}
	}

	mailbox := uuid.New()
	exec(`INSERT INTO email_accounts (id, user_id, organization_id, email, name, signature_plain, signature_html, provider)
	      VALUES ($1, $2, $3, $4, 'History', '', '', 'smtp_imap')`, mailbox, f.owner, f.org, "history-"+mailbox.String()[:6]+"@test.local")
	lead := "lead-" + uuid.New().String()[:6] + "@example.org"
	imported := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	contact := uuid.New()
	exec(`INSERT INTO contacts (id, user_id, organization_id, email, first_name, last_name, company, phone, custom_fields, created_at)
	      VALUES ($1, $2, $3, $4, 'Lead', '', '', '', '{}', $5)`, contact, f.owner, f.org, lead, imported)
	sendID := "<send-" + uuid.New().String()[:8] + "@test.local>"
	exec(`INSERT INTO tasks (id, task_type, email_account_id, status, message_id) VALUES ($1, 'campaign', $2, 'completed', $3)`, uuid.New(), mailbox, sendID)

	processed := imported.Add(12 * time.Hour)
	message := func(from, subject string, received time.Time, inReplyTo []string, processedAt *time.Time) uuid.UUID {
		id := uuid.New()
		if inReplyTo == nil {
			inReplyTo = []string{}
		}
		exec(`INSERT INTO unibox_emails (id, user_id, email_id, folder, provider_folder, from_addr, subject, in_reply_to, internal_date, campaign_reply_processed_at)
		      VALUES ($1, $2, $3, 'inbox', 'inbox', ARRAY[$4], $5, $6, $7, $8)`, id, f.owner, mailbox, from, subject, inReplyTo, received, processedAt)
		return id
	}
	newsletter := message("Lead ("+lead+")", "Big sale", imported.AddDate(0, -5, 0), nil, &processed)
	message("Lead <"+lead+">", "Re: Quick question", imported.AddDate(0, -5, 0), []string{sendID}, &processed)
	message("Lead <"+lead+">", "Fresh", imported.Add(time.Hour), nil, &processed)
	message("Lead <"+lead+">", "Unclaimed", imported.AddDate(0, -5, 0), nil, nil)
	message("Lead <"+lead+">", "Undated", time.Time{}, nil, &processed)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM unibox_emails WHERE email_id = $1`, mailbox)
		_, _ = pool.Exec(context.Background(), `DELETE FROM tasks WHERE email_account_id = $1`, mailbox)
		_, _ = pool.Exec(context.Background(), `DELETE FROM email_accounts WHERE id = $1`, mailbox)
		_, _ = pool.Exec(context.Background(), `DELETE FROM reply_intents WHERE organization_id = $1`, f.org)
		_, _ = pool.Exec(context.Background(), `DELETE FROM crm_tasks WHERE contact_id = $1`, contact)
		_, _ = pool.Exec(context.Background(), `DELETE FROM contacts WHERE id = $1`, contact)
	})

	unibox := &uniboxRepository{db: handle}
	var found []HistoricalReply
	cursor := uuid.Nil
	for range 10_000 {
		page, err := unibox.ListHistoricalReplies(ctx, time.Now().AddDate(0, 0, -1), cursor)
		if err != nil {
			t.Fatalf("ListHistoricalReplies: %v", err)
		}
		for _, h := range page.Replies {
			if h.ContactID == contact {
				found = append(found, h)
			}
		}
		cursor = page.NextCursor
		if page.Done {
			break
		}
	}
	if len(found) != 1 || found[0].ID != newsletter {
		t.Fatalf("historical replies = %+v, want only the newsletter %v", found, newsletter)
	}
	if h := found[0]; h.OrganizationID != f.org || h.Sender != lead || h.Subject != "Big sale" || !h.ProcessedAt.Equal(processed) {
		t.Errorf("historical reply read as %+v", h)
	}

	adv := &advancedOutreachRepository{db: pool}
	for _, subject := range []string{"Big sale", "Fresh"} {
		if err := adv.CreateReplyIntent(ctx, &models.ReplyIntentRecord{OrganizationID: f.org, ContactEmail: lead,
			Intent: models.ReplyIntentNeutral, ActionTaken: "created_crm_task", Metadata: map[string]interface{}{"subject": subject}}); err != nil {
			t.Fatalf("CreateReplyIntent: %v", err)
		}
	}
	now := time.Now()
	intents, err := adv.ListReplyIntentsFor(ctx, f.org, lead, "Big sale", now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil || len(intents) != 1 || intents[0].ActionTaken != "created_crm_task" || intents[0].Intent != models.ReplyIntentNeutral {
		t.Fatalf("ListReplyIntentsFor = %+v, %v; want the newsletter's one intent", intents, err)
	}
	if err := adv.DeleteReplyIntents(ctx, f.org, []uuid.UUID{intents[0].ID}); err != nil {
		t.Fatalf("DeleteReplyIntents: %v", err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM reply_intents WHERE organization_id = $1`, f.org).Scan(&left); err != nil || left != 1 {
		t.Fatalf("intents left = %d, %v; want the fresh reply's", left, err)
	}

	crm := &crmRepository{db: pool}
	title := "Follow up: reply from " + lead
	task := func(status string) uuid.UUID {
		created, err := crm.CreateCRMTask(ctx, f.org, f.owner, &models.CreateCRMTask{ContactID: &contact, Title: title, Priority: "high"})
		if err != nil {
			t.Fatalf("CreateCRMTask: %v", err)
		}
		if status != "pending" {
			exec(`UPDATE crm_tasks SET status = $2::crm_task_status, updated_at = NOW() + interval '1 second' WHERE id = $1`, created.ID, status)
		}
		return created.ID
	}
	untouched, worked := task("pending"), task("in_progress")
	edited := task("pending")
	exec(`UPDATE crm_tasks SET description = 'called them', updated_at = NOW() + interval '1 second' WHERE id = $1`, edited)
	cancelled, err := crm.CancelUntouchedCRMTasks(ctx, f.org, contact, title, now.Add(-time.Minute), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("CancelUntouchedCRMTasks: %v", err)
	}
	if len(cancelled) != 1 || cancelled[0] != untouched {
		t.Fatalf("cancelled %v, want only the untouched task %v (kept %v and %v)", cancelled, untouched, worked, edited)
	}
	if again, err := crm.CancelUntouchedCRMTasks(ctx, f.org, contact, title, now.Add(-time.Minute), time.Now().Add(time.Minute)); err != nil || len(again) != 0 {
		t.Fatalf("second cancel = %v, %v; want nothing", again, err)
	}
}
