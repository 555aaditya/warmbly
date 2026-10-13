package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/config"
)

func TestLiveWarmupPartnerCountsMatchIndividualCandidates(t *testing.T) {
	f := newPartnerOrgFixture(t)
	r := &warmupRepository{db: f.pool}
	ids := []uuid.UUID{f.sender, f.sibling, f.outside, uuid.New()}
	assertParity := func(pool string) {
		t.Helper()
		counts, err := r.WarmupPartnerCounts(t.Context(), pool, ids)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			candidates, err := r.WarmupPartnerCandidates(t.Context(), pool, id)
			if err != nil || counts[id] != len(candidates) {
				t.Fatalf("%s/%s: count=%d individual=%d error=%v", pool, id, counts[id], len(candidates), err)
			}
		}
	}
	assertParity("premium")
	f.moveToFree(t, f.sibling, config.WarmupPoolFallbackMinAgeDays+1)
	f.moveToFree(t, f.outside, config.WarmupPoolFallbackMinAgeDays+1)
	assertParity("premium")
	assertParity("free")
	f.received(t, f.outside, f.sender, time.Now().Add(-time.Hour))
	f.received(t, f.outside, f.sender, time.Now().Add(-2*time.Hour))
	f.received(t, f.sibling, f.sender, time.Now().Add(-time.Duration(config.WarmupPoolReturnVisitDays+1)*24*time.Hour))
	assertParity("free")
	for _, change := range []string{
		`UPDATE warmup_pool_participants SET health_state='watch' WHERE email_account_id=$1`,
		`UPDATE warmup_pool_participants SET health_state='healthy', joined_at=NOW() WHERE email_account_id=$1`,
		`UPDATE warmup_pool_participants SET joined_at=NOW()-interval '30 days', health_state='quarantined', blocked_until=NULL WHERE email_account_id=$1`,
		`UPDATE warmup_pool_participants SET blocked_until=NOW()-interval '1 hour' WHERE email_account_id=$1`,
		`UPDATE warmup_pool_participants SET participant_role='recipient_only' WHERE email_account_id=$1`,
		`UPDATE warmup_pool_participants SET participant_role='sender_receiver', health_state='healthy', blocked_until=NULL WHERE email_account_id=$1`,
		`UPDATE email_accounts SET test_mode='diagnostic', test_send_enabled=false WHERE id=$1`,
		`UPDATE email_accounts SET test_mode=NULL WHERE id=$1`,
	} {
		f.exec(change, f.outside)
		assertParity("free")
		assertParity("premium")
	}
	f.exec(`UPDATE organizations SET risk_state='restricted' WHERE id=$1`, f.other)
	assertParity("premium")
	assertParity("free")
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.WarmupPartnerCounts(cancelled, "premium", ids); err == nil {
		t.Fatal("cancelled query reported counts")
	}
}

func TestLiveWarmupSummariesMatchIndividualReads(t *testing.T) {
	f := newPartnerOrgFixture(t)
	r := &warmupRepository{db: f.pool}
	now := time.Now().UTC()
	since := now.Add(-7 * 24 * time.Hour)
	for _, row := range []struct {
		date time.Time
		sent int
	}{
		{since.Add(-24 * time.Hour), 100},
		{since, 3},
		{now, 7},
		{now.Add(24 * time.Hour), 2},
	} {
		f.exec(`INSERT INTO warmup_statistics (email_account_id, date, emails_sent, emails_replied, target_volume)
		        VALUES ($1, DATE($2), $3, 2, 10)`, f.sender, row.date, row.sent)
	}
	for _, row := range []struct {
		at   time.Time
		kind string
	}{
		{since.Add(-time.Second), "spam_placement"},
		{since, "spam_placement"},
		{now, "spam_placement"},
		{now, "user_complaint"},
	} {
		f.exec(`INSERT INTO warmup_spam_reports (reporter_account_id, reported_account_id, message_id, report_type, created_at)
		        VALUES ($1, $2, $3, $4, $5)`, f.sibling, f.sender, uuid.NewString(), row.kind, row.at)
	}
	ids := []uuid.UUID{f.sender, f.sibling}
	summaries, err := r.GetWarmupSummaries(t.Context(), ids, since, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		sent, err := r.SumWarmupSentSince(t.Context(), id, since)
		if err != nil {
			t.Fatal(err)
		}
		placed, _, err := r.CountWarmupSpamReportsSince(t.Context(), id, since)
		if err != nil {
			t.Fatal(err)
		}
		stats, err := r.GetWarmupStatistics(t.Context(), id, since, now)
		if err != nil {
			t.Fatal(err)
		}
		replied := 0
		for _, stat := range stats {
			replied += stat.EmailsReplied
		}
		want := WarmupSummary{Sent: sent, Replied: replied, SpamPlaced: placed}
		if summaries[id] != want {
			t.Fatalf("%s: got %+v want %+v", id, summaries[id], want)
		}
	}
	if summaries[f.sender] != (WarmupSummary{Sent: 12, Replied: 4, SpamPlaced: 2}) || summaries[f.sibling] != (WarmupSummary{}) {
		t.Fatalf("window/isolation mismatch: %+v", summaries)
	}
	if empty, err := r.GetWarmupSummaries(t.Context(), nil, since, now); err != nil || len(empty) != 0 {
		t.Fatalf("empty request: %+v %v", empty, err)
	}
}
