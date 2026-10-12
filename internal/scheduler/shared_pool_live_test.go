package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// longOpenTimezone is a zone whose 8am-8pm mailbox band stays open for the
// next six hours, so a slot pushed past four hours is the conflict step's
// doing and not the band closing or the distribution curve.
func longOpenTimezone(t *testing.T) string {
	t.Helper()
	open := func(at time.Time) bool { return at.Hour() >= 8 && at.Hour() < 20 }
	for _, name := range []string{"UTC", "America/New_York", "America/Los_Angeles", "Asia/Tokyo",
		"Europe/Berlin", "Asia/Dubai", "Pacific/Auckland", "Pacific/Honolulu", "Asia/Kolkata", "America/Sao_Paulo"} {
		l, err := time.LoadLocation(name)
		if err != nil {
			continue
		}
		now := time.Now().In(l)
		if open(now) && open(now.Add(6*time.Hour)) {
			return name
		}
	}
	t.Skip("no timezone in the spread stays inside 8am-8pm for the next six hours")
	return ""
}

// longOpenFixture is the live fixture with the campaign on the mailbox's own
// clock, so the distribution curve moves a slot by two hours at most.
func longOpenFixture(t *testing.T) (*liveFixture, SchedulerService) {
	t.Helper()
	handle, pool := liveDB(t)
	tz := longOpenTimezone(t)
	f := newLiveFixture(t, pool, tz)
	if _, err := pool.Exec(context.Background(), `UPDATE campaigns SET timezone = $2 WHERE id = $1`, f.campaign, tz); err != nil {
		t.Fatalf("campaign timezone: %v", err)
	}
	return f, liveScheduler(t, handle, pool)
}

// fillMailboxDay books a task of taskType on the mailbox every five minutes
// for a day, closer together than its 600s gap, so any slot near one of them
// conflicts.
func fillMailboxDay(t *testing.T, f *liveFixture, taskType string) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO tasks (id, task_type, email_account_id, status, message_id, scheduled_at)
		SELECT gen_random_uuid(), $1::task_type, $2, 'pending', '', s
		FROM generate_series(NOW() - INTERVAL '1 hour', NOW() + INTERVAL '24 hours', INTERVAL '5 minutes') s`,
		taskType, f.mailbox); err != nil {
		t.Fatalf("book %s tasks: %v", taskType, err)
	}
}

// TestLiveParkedCampaignDoesNotThrottleItsPoolMate is issue #942: another
// campaign's deferral parks on the shared mailbox are wake-ups, not sends, and
// must not push the active campaign's slot behind them.
func TestLiveParkedCampaignDoesNotThrottleItsPoolMate(t *testing.T) {
	f, s := longOpenFixture(t)
	fillMailboxDay(t, f, "campaign")

	at, pair, _, err := s.CalculateNextCampaignTime(context.Background(), f.campaign)
	if err != nil || pair == nil {
		t.Fatalf("pair=%v err=%v, want a sendable lead", pair, err)
	}
	if limit := time.Now().Add(4 * time.Hour); at.After(limit) {
		t.Fatalf("slot %s was pushed behind another campaign's wake-ups (limit %s)", at, limit)
	}
}

// TestLiveBookedSendsStillSpaceTheCampaign is the control: a real send booked
// on the mailbox still moves the slot.
func TestLiveBookedSendsStillSpaceTheCampaign(t *testing.T) {
	f, s := longOpenFixture(t)
	fillMailboxDay(t, f, "warmup")

	at, pair, _, err := s.CalculateNextCampaignTime(context.Background(), f.campaign)
	if err != nil || pair == nil {
		t.Fatalf("pair=%v err=%v, want a sendable lead", pair, err)
	}
	if limit := time.Now().Add(4 * time.Hour); !at.After(limit) {
		t.Fatalf("slot %s ignored the warmup sends booked on the mailbox", at)
	}
}

// TestLiveClaimedMailboxIsPassedOver: a mailbox another campaign has just
// taken counts on the min-gap clock, so the pass sends from the other one.
func TestLiveClaimedMailboxIsPassedOver(t *testing.T) {
	handle, pool := liveDB(t)
	tz := openHoursTimezone(t)
	f := newLiveFixture(t, pool, tz)
	ctx := context.Background()

	second := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO email_accounts (id, user_id, organization_id, email, name,
	          signature_plain, signature_html, provider, status, campaign_limit, min_wait_time, timezone)
	      VALUES ($1, $2, $3, $4, 'Second', '', '', 'smtp_imap', 'active', 50, 600, $5)`,
		second, f.user, f.org, "second-"+second.String()[:8]+"@test.local", tz); err != nil {
		t.Fatalf("add mailbox: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM tasks WHERE email_account_id = $1`, second)
		_, _ = pool.Exec(c, `DELETE FROM email_accounts WHERE id = $1`, second)
	})
	// Least-recently-used would pick the mailbox that never sent, and that is
	// the one another campaign has just taken.
	if _, err := pool.Exec(ctx, `
		INSERT INTO tasks (id, task_type, email_account_id, status, message_id, scheduled_at, completed_at)
		VALUES ($1, 'campaign', $2, 'completed', '<earlier@test.local>', $3, $3)`,
		uuid.New(), second, earlierToday(2*time.Hour)); err != nil {
		t.Fatalf("earlier send: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE email_accounts SET send_claimed_at = NOW() WHERE id = $1`, f.mailbox); err != nil {
		t.Fatalf("claim: %v", err)
	}

	_, pair, accountID, err := liveScheduler(t, handle, pool).CalculateNextCampaignTime(ctx, f.campaign)
	if err != nil || pair == nil {
		t.Fatalf("pair=%v err=%v, want a send from the unclaimed mailbox", pair, err)
	}
	if accountID != second {
		t.Fatalf("picked %s, want the mailbox nobody had claimed (%s)", accountID, second)
	}
}
