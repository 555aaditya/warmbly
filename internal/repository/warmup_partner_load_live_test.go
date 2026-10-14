package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/models"
)

// The shared seven-day snapshot hands out the same candidates and counts as
// the exact read, and today's inbound cap still applies the moment it fills.
func TestLiveWarmupPartnerWeekCacheMatchesExactReadAndKeepsTheCapLive(t *testing.T) {
	f := newPartnerOrgFixture(t)
	ctx := context.Background()
	exact := &warmupRepository{db: f.pool}
	cached := &warmupRepository{db: f.pool, partnerWeeks7d: newPartnerWeekCache(time.Hour)}

	other := &partnerOrgFixture{pool: f.pool, sender: f.outside, exec: f.exec}
	for i := 0; i < 3; i++ {
		other.warmed(t, f.sibling)
	}
	f.received(t, f.outside, f.sibling, time.Now().Add(-2*time.Hour))
	t.Cleanup(func() {
		f.exec(`DELETE FROM warmup_tokens WHERE sender_account_id = $1`, f.outside)
		f.exec(`DELETE FROM tasks WHERE email_account_id = $1`, f.outside)
	})

	assertSame := func(stage string) []models.WarmupPartnerCandidate {
		t.Helper()
		want, err := exact.WarmupPartnerCandidates(ctx, "premium", f.sender)
		if err != nil {
			t.Fatal(err)
		}
		got, err := cached.WarmupPartnerCandidates(ctx, "premium", f.sender)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: cached %d candidates, exact %d", stage, len(got), len(want))
		}
		for _, w := range want {
			g, ok := candidateByID(got, w.ID)
			g.OrganizationID, w.OrganizationID = nil, nil
			if !ok || g != w {
				t.Fatalf("%s: cached %+v, exact %+v", stage, g, w)
			}
		}
		return got
	}
	got := assertSame("warm")
	if c, ok := candidateByID(got, f.outside); !ok || c.Sent7d != 3 || c.Received7d != 1 {
		t.Fatalf("outside partner = %+v, want sent/received 3/1", c)
	}

	// Filling the sibling's day removes it at once, snapshot or not.
	for i := 0; i < config.WarmupInboundDailyFloor; i++ {
		f.received(t, f.sibling, f.outside, time.Now().Add(-time.Minute))
	}
	got, err := cached.WarmupPartnerCandidates(ctx, "premium", f.sender)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := candidateByID(got, f.sibling); ok {
		t.Fatal("a mailbox at today's inbound cap is still offered from the cached path")
	}

	// A reserved send does not count against its own recipient.
	f.exec(`DELETE FROM warmup_received WHERE email_account_id = $1`, f.sibling)
	f.exec(`DELETE FROM warmup_tokens WHERE recipient_account_id = $1`, f.sibling)
	for i := 0; i < config.WarmupInboundDailyFloor-1; i++ {
		other.warmed(t, f.sibling)
	}
	reserved := uuid.New()
	f.exec(`INSERT INTO tasks (id, task_type, email_account_id, status, message_id) VALUES ($1, 'warmup', $2, 'active', '')`, reserved, f.sender)
	f.exec(`INSERT INTO warmup_tokens (token, task_id, sender_account_id, recipient_account_id, created_at)
	        VALUES (gen_random_uuid(), $1, $2, $3, NOW())`, reserved, f.sender, f.sibling)
	t.Cleanup(func() { f.exec(`DELETE FROM tasks WHERE id = $1`, reserved) })
	for _, repo := range []*warmupRepository{exact, cached} {
		planned, err := repo.WarmupPartnerCandidates(ctx, "premium", f.sender)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := candidateByID(planned, f.sibling); ok {
			t.Fatal("a full inbox is offered to a new send")
		}
		executing, err := repo.WarmupExecutionCandidates(ctx, "premium", f.sender, reserved)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := candidateByID(executing, f.sibling); !ok {
			t.Fatal("a reserved send was counted against its own recipient")
		}
	}
}

func TestPartnerInboundCapMatchesTheSQLRule(t *testing.T) {
	for _, tc := range []struct{ sent, share, want int }{
		{0, 100, config.WarmupInboundDailyFloor},
		{35, 100, max(config.WarmupInboundDailyFloor, 5*config.WarmupInboundDailyMultiple)},
		{36, 100, max(config.WarmupInboundDailyFloor, 6*config.WarmupInboundDailyMultiple)},
		{10_000, 100, config.WarmupInboundDailyCeiling},
		{0, 75, config.WarmupInboundDailyFloor * 75 / 100},
	} {
		if got := partnerInboundCap(tc.sent, tc.share); got != tc.want {
			t.Fatalf("cap(%d, %d%%) = %d, want %d", tc.sent, tc.share, got, tc.want)
		}
	}
}
