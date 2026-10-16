package warmup

import (
	"math"
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/models"
)

func TestEvaluateMetricsSpamThresholds(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		rate        float64
		wantState   models.WarmupHealthState
		wantBlocked time.Duration
	}{
		{name: "watch", rate: 10, wantState: models.WarmupHealthWatch},
		{name: "throttle", rate: 20, wantState: models.WarmupHealthThrottled, wantBlocked: warmupThrottleDuration},
		// Placement only ever slows a mailbox down; it never takes it out of the pool.
		{name: "half in spam still warms", rate: 50, wantState: models.WarmupHealthThrottled, wantBlocked: warmupThrottleDuration},
		{name: "all in spam still warms", rate: 100, wantState: models.WarmupHealthThrottled, wantBlocked: warmupThrottleDuration},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			decision := evaluateMetrics(&models.WarmupHealthMetrics{
				PlacementSample:   20,
				SpamPlacementRate: tc.rate,
			}, "", now)

			if decision.State != tc.wantState {
				t.Fatalf("expected %s, got %s", tc.wantState, decision.State)
			}
			if tc.wantBlocked == 0 {
				if decision.BlockedUntil != nil {
					t.Fatalf("expected no block, got %#v", decision.BlockedUntil)
				}
				return
			}
			if decision.BlockedUntil == nil || !decision.BlockedUntil.Equal(now.Add(tc.wantBlocked)) {
				t.Fatalf("expected blocked until %s, got %#v", now.Add(tc.wantBlocked), decision.BlockedUntil)
			}
		})
	}
}

func TestEvaluateMetricsThrottleBand(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)

	decision := evaluateMetrics(&models.WarmupHealthMetrics{
		PlacementSample:   25,
		SpamPlacementRate: 26.0,
	}, "", now)

	if decision.State != models.WarmupHealthThrottled {
		t.Fatalf("expected throttled, got %s", decision.State)
	}
	if decision.BlockedUntil == nil {
		t.Fatal("throttled should have blocked_until set")
	}
	if !decision.BlockedUntil.Equal(now.Add(warmupThrottleDuration)) {
		t.Fatalf("expected 3-day throttle, got %v", decision.BlockedUntil.Sub(now))
	}
}

func TestEvaluateMetricsComplaintRateWatch(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)

	decision := evaluateMetrics(&models.WarmupHealthMetrics{
		SentLast7d:        5,
		DeliveredLast30d:  200,
		ComplaintsLast30d: 1,
		ComplaintRate:     0.05, // between 0.03% (watch) and 0.10% (quarantine)
	}, "", now)

	if decision.State != models.WarmupHealthWatch {
		t.Fatalf("expected watch from complaint rate, got %s", decision.State)
	}
}

func TestEvaluateMetricsComplaintRateQuarantine(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)

	decision := evaluateMetrics(&models.WarmupHealthMetrics{
		SentLast7d:        5,
		DeliveredLast30d:  200,
		ComplaintsLast30d: 2,
		ComplaintRate:     0.15, // > 0.10% quarantine
	}, "", now)

	if decision.State != models.WarmupHealthQuarantined {
		t.Fatalf("expected quarantined from complaint rate, got %s", decision.State)
	}
}

func TestEvaluateMetricsComplaintRateBlock(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)

	decision := evaluateMetrics(&models.WarmupHealthMetrics{
		SentLast7d:        5,
		DeliveredLast30d:  200,
		ComplaintsLast30d: 10,
		ComplaintRate:     0.5, // > 0.30% block
	}, "", now)

	if decision.State != models.WarmupHealthBlocked {
		t.Fatalf("expected blocked from complaint rate, got %s", decision.State)
	}
}

func TestEvaluateMetricsBounceRateQuarantine(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)

	decision := evaluateMetrics(&models.WarmupHealthMetrics{
		SentLast7d:       5,
		DeliveredLast30d: 200,
		BouncesLast30d:   12,
		BounceRate:       6.0, // > 5% quarantine
	}, "", now)

	if decision.State != models.WarmupHealthQuarantined {
		t.Fatalf("expected quarantined from bounce rate, got %s", decision.State)
	}
}

func TestEvaluateMetricsBounceRateBlock(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)

	decision := evaluateMetrics(&models.WarmupHealthMetrics{
		SentLast7d:       5,
		DeliveredLast30d: 200,
		BouncesLast30d:   25,
		BounceRate:       12.5, // > 10% block
	}, "", now)

	if decision.State != models.WarmupHealthBlocked {
		t.Fatalf("expected blocked from bounce rate, got %s", decision.State)
	}
}

func TestEvaluateMetricsComplaintBelowSampleIgnored(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)

	decision := evaluateMetrics(&models.WarmupHealthMetrics{
		SentLast7d:        5,
		DeliveredLast30d:  50, // below 100 minimum
		ComplaintsLast30d: 5,
		ComplaintRate:     10.0,
	}, "", now)

	if decision.State == models.WarmupHealthQuarantined || decision.State == models.WarmupHealthBlocked {
		t.Fatalf("should not quarantine/block with insufficient sample, got %s", decision.State)
	}
}

func TestEvaluateMetricsIgnoresSmallSamples(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)

	decision := evaluateMetrics(&models.WarmupHealthMetrics{
		SentLast7d:        40,
		PlacementSample:   19,
		SpamPlacementRate: 100,
	}, "", now)

	if decision.State != models.WarmupHealthHealthy {
		t.Fatalf("expected healthy for undersampled account, got %s", decision.State)
	}
	if decision.BlockedUntil != nil {
		t.Fatalf("expected no block, got %#v", decision.BlockedUntil)
	}
}

// Harm to received warmup mail climbs a ladder rather than blocking on the
// first event: one deletion is housekeeping until proven otherwise (#635).
func TestEvaluateMetricsTamperingLadder(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		deletions   int
		spamFlags   int
		wantState   models.WarmupHealthState
		wantBlocked time.Duration
	}{
		{"nothing", 0, 0, models.WarmupHealthHealthy, 0},
		{"one deletion warns", 1, 0, models.WarmupHealthWatch, 0},
		{"two deletions pause", 2, 0, models.WarmupHealthQuarantined, warmupQuarantineDuration},
		{"one spam flag warns", 0, 1, models.WarmupHealthWatch, 0},
		{"two spam flags pause", 0, 2, models.WarmupHealthQuarantined, warmupQuarantineDuration},
		{"four deletions block", 4, 0, models.WarmupHealthBlocked, warmupBlockDuration},
		{"four spam flags block", 0, 4, models.WarmupHealthBlocked, warmupBlockDuration},
		{"a flag and three deletions block", 3, 1, models.WarmupHealthBlocked, warmupBlockDuration},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := evaluateMetrics(&models.WarmupHealthMetrics{DeletionsLast7d: tc.deletions, SpamFlagsLast7d: tc.spamFlags}, "", now)
			if decision.State != tc.wantState {
				t.Fatalf("state = %s, want %s (reason %q)", decision.State, tc.wantState, decision.Reason)
			}
			if tc.wantBlocked == 0 {
				if decision.BlockedUntil != nil {
					t.Fatalf("a %s carries a term: %v", tc.wantState, decision.BlockedUntil)
				}
				return
			}
			if decision.BlockedUntil == nil || !decision.BlockedUntil.Equal(now.Add(tc.wantBlocked)) {
				t.Fatalf("blocked until %v, want %s", decision.BlockedUntil, now.Add(tc.wantBlocked))
			}
			if decision.Reason == "" {
				t.Fatal("a tampering decision carries no reason for the owner")
			}
		})
	}
}

// A tampering block never requires review: it lapses like every other band,
// so an accidental run of deletions is not permanent.
func TestEvaluateMetricsTamperingBlockLapses(t *testing.T) {
	decision := evaluateMetrics(&models.WarmupHealthMetrics{DeletionsLast7d: 6}, "", time.Now())
	if decision.State != models.WarmupHealthBlocked || decision.BlockedUntil == nil {
		t.Fatalf("decision = %+v, want a block with a term", decision)
	}
}

// The rate bands and the tampering band are judged apart and the more severe
// finding is kept, whichever side it comes from.
func TestEvaluateMetricsTamperingCombinesWithRates(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	block := now.Add(warmupBlockDuration)
	quarantine := now.Add(warmupQuarantineDuration)
	cases := []struct {
		name    string
		metrics models.WarmupHealthMetrics
		want    models.WarmupHealthState
		until   *time.Time
	}{
		{"one deletion does not mask a placement throttle", models.WarmupHealthMetrics{DeletionsLast7d: 1, PlacementSample: 20, SpamPlacementRate: 50}, models.WarmupHealthThrottled, func() *time.Time { u := now.Add(warmupThrottleDuration); return &u }()},
		{"a placement watch does not mask a tampering quarantine", models.WarmupHealthMetrics{DeletionsLast7d: 2, PlacementSample: 20, SpamPlacementRate: 10}, models.WarmupHealthQuarantined, &quarantine},
		{"a complaint-rate quarantine does not mask a tampering block", models.WarmupHealthMetrics{DeletionsLast7d: 4, DeliveredLast30d: 100, ComplaintRate: complaintRateQuarantinePct}, models.WarmupHealthBlocked, &block},
		{"a bounce-rate quarantine does not mask a tampering block", models.WarmupHealthMetrics{SpamFlagsLast7d: 4, DeliveredLast30d: 100, BounceRate: bounceRateQuarantinePct}, models.WarmupHealthBlocked, &block},
		{"a warmup-complaint quarantine does not mask a tampering block", models.WarmupHealthMetrics{DeletionsLast7d: 4, SentLast7d: 20, WarmupComplaintRate: warmupComplaintQuarantinePct}, models.WarmupHealthBlocked, &block},
		{"a placement throttle does not mask a tampering quarantine", models.WarmupHealthMetrics{DeletionsLast7d: 2, PlacementSample: 20, SpamPlacementRate: spamPlacementThrottlePct}, models.WarmupHealthQuarantined, &quarantine},
		{"heavy placement does not soften a tampering block", models.WarmupHealthMetrics{DeletionsLast7d: 4, PlacementSample: 20, SpamPlacementRate: 90}, models.WarmupHealthBlocked, &block},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.metrics
			decision := evaluateMetrics(&m, "", now)
			if decision.State != tc.want {
				t.Fatalf("state = %s, want %s (reason %q)", decision.State, tc.want, decision.Reason)
			}
			if (decision.BlockedUntil == nil) != (tc.until == nil) {
				t.Fatalf("blocked until %v, want %v", decision.BlockedUntil, tc.until)
			}
			if tc.until != nil && !decision.BlockedUntil.Equal(*tc.until) {
				t.Fatalf("blocked until %v, want %s", decision.BlockedUntil, tc.until)
			}
		})
	}
}

// Only Google, Microsoft and Yahoo judge a sender; another host's spam folder
// is never held against it.
func TestJudgedPlacementRate(t *testing.T) {
	cases := []struct {
		name       string
		evidence   models.WarmupPlacementEvidence
		wantRate   float64
		wantSample int
	}{
		{"majors only", models.WarmupPlacementEvidence{MajorDelivered: 40, MajorSpam: 4}, 10, 40},
		{"small-host spam is not counted", models.WarmupPlacementEvidence{MajorDelivered: 30, OtherDelivered: 20, OtherSpam: 20}, 0, 30},
		{"only small hosts is not judged", models.WarmupPlacementEvidence{OtherDelivered: 40, OtherSpam: 40}, 0, 0},
		{"nothing delivered", models.WarmupPlacementEvidence{}, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rate, sample := tc.evidence.Judged()
			if math.Abs(rate-tc.wantRate) > 1e-9 || sample != tc.wantSample {
				t.Fatalf("rate, sample = %v, %d; want %v, %d", rate, sample, tc.wantRate, tc.wantSample)
			}
		})
	}
}

// Every small host junking everything leaves a mailbox that inboxes at the
// majors healthy; junk at the majors slows it down and never pauses it.
func TestPlacementAtSmallHostsDoesNotSlowAHealthyMailbox(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	metricsFor := func(e models.WarmupPlacementEvidence) *models.WarmupHealthMetrics {
		rate, sample := e.Judged()
		return &models.WarmupHealthMetrics{SpamPlacementRate: rate, PlacementSample: sample,
			OtherDelivered: e.OtherDelivered, OtherSpamRate: pct(e.OtherSpam, e.OtherDelivered)}
	}
	small := evaluateMetrics(metricsFor(models.WarmupPlacementEvidence{MajorDelivered: 40, OtherDelivered: 40, OtherSpam: 40}), "", now)
	if small.State != models.WarmupHealthHealthy {
		t.Fatalf("small-host spam alone set %s (%q)", small.State, small.Reason)
	}
	major := evaluateMetrics(metricsFor(models.WarmupPlacementEvidence{MajorDelivered: 40, MajorSpam: 36}), "", now)
	if major.State != models.WarmupHealthThrottled {
		t.Fatalf("90%% junk at the majors set %s, want throttled", major.State)
	}
}

// A watch or throttle holds until the rate is clearly below the line that set
// it, so a mailbox hovering at a line is not flipped (and announced) each pass.
func TestPlacementBandsHoldUntilClearlyRecovered(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		rate  float64
		prior models.WarmupHealthState
		want  models.WarmupHealthState
	}{
		{"a fresh 9% is healthy", 9, models.WarmupHealthHealthy, models.WarmupHealthHealthy},
		{"9% keeps a watch", 9, models.WarmupHealthWatch, models.WarmupHealthWatch},
		{"7% lifts a watch", 7, models.WarmupHealthWatch, models.WarmupHealthHealthy},
		{"a fresh 18% is only a watch", 18, models.WarmupHealthHealthy, models.WarmupHealthWatch},
		{"18% keeps a throttle", 18, models.WarmupHealthThrottled, models.WarmupHealthThrottled},
		{"14% steps a throttle down to watch", 14, models.WarmupHealthThrottled, models.WarmupHealthWatch},
		{"7% lifts a throttle", 7, models.WarmupHealthThrottled, models.WarmupHealthHealthy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := evaluateMetrics(&models.WarmupHealthMetrics{PlacementSample: 30, SpamPlacementRate: tc.rate}, tc.prior, now)
			if d.State != tc.want {
				t.Fatalf("state = %s, want %s (reason %q)", d.State, tc.want, d.Reason)
			}
		})
	}
}

// The reason a mailbox owner reads names what was judged, what was not, and
// that it recovers by itself.
func TestPlacementReasonExplainsTheReading(t *testing.T) {
	d := evaluateMetrics(&models.WarmupHealthMetrics{
		PlacementSample: 40, SpamPlacementRate: 25,
		OtherDelivered: 20, OtherSpamRate: 50,
	}, "", time.Now())
	want := "25% of warmup mail delivered at Google, Microsoft and Yahoo landed in spam over 7 days (40 delivered). " +
		"Other mail hosts (50% of 20) run their own filters and are not counted. " +
		"Warmup and cold sending run at half volume with wider spacing until it is below 15%, then step down to watch. " +
		"Watch lifts below 7.5% on its own. Separate ramp limits still apply."
	if d.Reason != want {
		t.Fatalf("reason =\n%q\nwant\n%q", d.Reason, want)
	}
}

// Only a watch or throttle the placement band set is held by its exit line; a
// re-entry probation or a complaint watch is not stretched by placement.
func TestPlacementPriorOnlyHoldsItsOwnBands(t *testing.T) {
	placed := evaluateMetrics(&models.WarmupHealthMetrics{PlacementSample: 30, SpamPlacementRate: 25}, "", time.Now())
	own := &models.WarmupParticipantHealth{HealthState: placed.State, LastHealthReason: &placed.Reason}
	if got := placementPrior(own); got != models.WarmupHealthThrottled {
		t.Fatalf("placement's own throttle read as prior %q", got)
	}
	probation := "re-entry probation after block expiry"
	other := &models.WarmupParticipantHealth{HealthState: models.WarmupHealthThrottled, LastHealthReason: &probation}
	if got := placementPrior(other); got != "" {
		t.Fatalf("a probation throttle read as placement prior %q", got)
	}
	d := evaluateMetrics(&models.WarmupHealthMetrics{PlacementSample: 30, SpamPlacementRate: 16}, placementPrior(other), time.Now())
	if d.State != models.WarmupHealthWatch {
		t.Fatalf("16%% after a probation throttle set %s, want watch", d.State)
	}
}
