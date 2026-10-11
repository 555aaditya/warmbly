package tasks

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
	"github.com/warmbly/warmbly/internal/scheduler"
	"github.com/warmbly/warmbly/internal/tasks/proto"
)

// Live checks that campaigns sharing a mailbox cannot both send inside its
// gap once their wake-ups no longer space each other (issue #942).

// siblingCampaign adds a second campaign, step and lead to the fixture's
// workspace, sending from the same mailbox.
func (f *campaignSendFixture) siblingCampaign(t *testing.T) *campaignSendFixture {
	t.Helper()
	ctx := context.Background()
	b := *f
	b.campaign, b.step, b.contact = uuid.New(), uuid.New(), uuid.New()
	b.contactEmail = "lead-" + b.contact.String()[:8] + "@test.local"
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("fixture %q: %v", sql[:min(60, len(sql))], err)
		}
	}
	exec(`INSERT INTO campaigns (id, user_id, organization_id, name, description, status,
	          daily_limit, timezone, days, start_time, end_time, rotation_mode, updated_at, created_at)
	      VALUES ($1, $2, $3, 'Live Sibling', '', 'active', 50, 'UTC', 127, '00:00', '23:59',
	              'least_recently_used', NOW(), NOW())`, b.campaign, b.user, b.org)
	exec(`INSERT INTO sequences (id, campaign_id, organization_id, name, subject,
	          body_plain, body_html, wait_after, position, kind)
	      VALUES ($1, $2, $3, 'Step 1', 'Hi', 'Hello', '<p>Hello</p>', 0, 0, 'email')`,
		b.step, b.campaign, b.org)
	exec(`INSERT INTO contacts (id, user_id, organization_id, email, first_name, last_name, company, phone, custom_fields)
	      VALUES ($1, $2, $3, $4, 'Live', 'Sibling', '', '', '{}')`,
		b.contact, b.user, b.org, b.contactEmail)
	exec(`INSERT INTO campaign_leads (campaign_id, contact_id, position) VALUES ($1, $2, 0)`, b.campaign, b.contact)
	t.Cleanup(func() {
		c := context.Background()
		for _, step := range []struct {
			sql string
			arg any
		}{
			{`DELETE FROM task_execution_keys WHERE task_id IN (SELECT id FROM tasks WHERE email_account_id = $1)`, f.mailbox},
			{`DELETE FROM task_failures WHERE task_id IN (SELECT id FROM tasks WHERE email_account_id = $1)`, f.mailbox},
			{`DELETE FROM campaign_tasks WHERE task_id IN (SELECT id FROM tasks WHERE email_account_id = $1)`, f.mailbox},
			{`DELETE FROM tasks WHERE email_account_id = $1`, f.mailbox},
			{`DELETE FROM campaign_logs WHERE campaign_id = $1`, b.campaign},
			{`DELETE FROM campaign_daily_sends WHERE campaign_id = $1`, b.campaign},
			{`DELETE FROM campaign_contact_progress WHERE campaign_id = $1`, b.campaign},
			{`DELETE FROM campaign_leads WHERE campaign_id = $1`, b.campaign},
			{`DELETE FROM sequences WHERE campaign_id = $1`, b.campaign},
			{`DELETE FROM campaigns WHERE id = $1`, b.campaign},
		} {
			if _, err := f.pool.Exec(c, step.sql, step.arg); err != nil {
				t.Errorf("cleanup %q: %v", step.sql, err)
			}
		}
	})
	return &b
}

// pendingPasses returns when each of the campaign's pending passes is due.
func (f *campaignSendFixture) pendingPasses(t *testing.T) []time.Time {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), `
		SELECT t.scheduled_at FROM tasks t JOIN campaign_tasks ct ON ct.task_id = t.id
		WHERE ct.campaign_id = $1 AND t.status = 'pending'`, f.campaign)
	if err != nil {
		t.Fatalf("read passes: %v", err)
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var at time.Time
		if err := rows.Scan(&at); err != nil {
			t.Fatalf("scan pass: %v", err)
		}
		out = append(out, at)
	}
	return out
}

func requireGap(t *testing.T, pool *pgxpool.Pool, mailbox uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE email_accounts SET min_wait_time = 600 WHERE id = $1`, mailbox); err != nil {
		t.Fatalf("gap: %v", err)
	}
}

func TestLiveCampaignsSharingAMailboxSendOncePerGap(t *testing.T) {
	handle := liveCampaignDB(t)
	sender := &recordingSender{}
	svc := liveCampaignService(t, handle, sender)
	a := newCampaignSendFixture(t, handle.Pool)
	requireGap(t, handle.Pool, a.mailbox)
	b := a.siblingCampaign(t)

	ticks := []uuid.UUID{a.queueTick(t, svc.taskRepo), b.queueTick(t, svc.taskRepo)}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, id := range ticks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if xerr := svc.HandleCampaignTask(&proto.ProcessTask{TaskId: id.String()}); xerr != nil {
				t.Errorf("campaign tick returned an error: %v", xerr)
			}
		}()
	}
	close(start)
	wg.Wait()

	if n := sender.count(); n != 1 {
		t.Fatalf("the shared mailbox sent %d emails inside its 600s gap, want 1", n)
	}
	// Whichever lost keeps its chain and comes back for its lead.
	if len(a.pendingPasses(t)) != 1 || len(b.pendingPasses(t)) != 1 {
		t.Fatalf("pending passes: a=%d b=%d, want one each", len(a.pendingPasses(t)), len(b.pendingPasses(t)))
	}
}

// racingScheduler lets another campaign take the mailbox between this pass's
// read of the pool and its claim.
type racingScheduler struct {
	scheduler.SchedulerService
	pool    *pgxpool.Pool
	mailbox uuid.UUID
}

func (r racingScheduler) CalculateNextCampaignTime(ctx context.Context, id uuid.UUID) (time.Time, *repository.ContactSequencePair, uuid.UUID, error) {
	at, pair, account, err := r.SchedulerService.CalculateNextCampaignTime(ctx, id)
	if _, xerr := r.pool.Exec(ctx, `UPDATE email_accounts SET send_claimed_at = NOW() WHERE id = $1`, r.mailbox); xerr != nil {
		return at, pair, account, xerr
	}
	return at, pair, account, err
}

func (r racingScheduler) ClaimSend(ctx context.Context, account *models.Email) (bool, error) {
	return r.SchedulerService.(scheduler.SendClaimer).ClaimSend(ctx, account)
}

func TestLiveLostMailboxClaimReschedulesWithoutSending(t *testing.T) {
	handle := liveCampaignDB(t)
	sender := &recordingSender{}
	svc := liveCampaignService(t, handle, sender)
	f := newCampaignSendFixture(t, handle.Pool)
	requireGap(t, handle.Pool, f.mailbox)
	svc.scheduler = racingScheduler{SchedulerService: svc.scheduler, pool: handle.Pool, mailbox: f.mailbox}

	taskID := f.queueTick(t, svc.taskRepo)
	if xerr := svc.HandleCampaignTask(&proto.ProcessTask{TaskId: taskID.String()}); xerr != nil {
		t.Fatalf("campaign tick returned an error: %v", xerr)
	}

	if n := sender.count(); n != 0 {
		t.Fatalf("sent %d emails from a mailbox another campaign had just taken", n)
	}
	var dispatched, failures int
	var status string
	if err := handle.Pool.QueryRow(context.Background(), `
		SELECT (SELECT COUNT(*) FROM campaign_contact_progress WHERE campaign_id = $1 AND dispatched_at IS NOT NULL),
		       (SELECT COUNT(*) FROM task_failures WHERE task_id = $2),
		       (SELECT status::text FROM tasks WHERE id = $2)`, f.campaign, taskID).Scan(&dispatched, &failures, &status); err != nil {
		t.Fatalf("read outcome: %v", err)
	}
	if dispatched != 0 || failures != 0 || status != "completed" {
		t.Fatalf("dispatched=%d failures=%d status=%s, want a quiet pass that reserved nothing", dispatched, failures, status)
	}
	for _, e := range f.logEvents(t) {
		if e == "email_failed" {
			t.Fatal("a lost claim was reported to the campaign feed as a failed send")
		}
	}
	passes := f.pendingPasses(t)
	if len(passes) != 1 {
		t.Fatalf("%d pending passes, want the one that looks again", len(passes))
	}
	if wait := time.Until(passes[0]); wait < 5*time.Second || wait > 30*time.Second {
		t.Fatalf("next pass in %s, want it back within seconds", wait)
	}
}
