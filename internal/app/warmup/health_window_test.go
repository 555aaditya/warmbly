package warmup

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/app/warmupramp"
	"github.com/warmbly/warmbly/internal/models"
)

type windowMetricsRepo struct {
	zeroMetricsRepo
	placement, week, month time.Time
}

func (r *windowMetricsRepo) HealthMetricCounts(_ context.Context, _ uuid.UUID, placement, week, month time.Time) (models.WarmupHealthCounts, error) {
	r.placement, r.week, r.month = placement, week, month
	return models.WarmupHealthCounts{}, nil
}

func TestHealthPlacementWindowMatchesReportingWithoutShorteningOtherSignals(t *testing.T) {
	now := time.Date(2026, 10, 16, 4, 30, 0, 0, time.UTC)
	for _, floor := range []time.Time{now.Add(-40 * 24 * time.Hour), now.Add(-time.Hour)} {
		r := &windowMetricsRepo{}
		svc := NewService(r).(*service)
		svc.now = func() time.Time { return now }
		_, err := svc.loadMetrics(t.Context(), uuid.New(), &models.WarmupParticipantHealth{HealthSignalsFrom: floor})
		if err != nil {
			t.Fatal(err)
		}
		want := []time.Time{models.WarmupPlacementWindowStart(now), now.Add(-7 * 24 * time.Hour), now.Add(-30 * 24 * time.Hour)}
		for i, got := range []time.Time{r.placement, r.week, r.month} {
			if floor.After(want[i]) {
				want[i] = floor
			}
			if !got.Equal(want[i]) {
				t.Fatalf("window %d starts %s, want %s", i, got, want[i])
			}
		}
	}
}

func TestPlacementRecoveryRestoresVolumeAndRetainsSafeguards(t *testing.T) {
	now := time.Date(2026, 10, 16, 4, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		name         string
		rate         float64
		prior, state models.WarmupHealthState
		volume       float64
	}{
		{"new throttle", 20, models.WarmupHealthHealthy, models.WarmupHealthThrottled, 0.5},
		{"hold at exit line", 15, models.WarmupHealthThrottled, models.WarmupHealthThrottled, 0.5},
		{"step down", 14.9, models.WarmupHealthThrottled, models.WarmupHealthWatch, 0.7},
		{"hold watch at exit line", 7.5, models.WarmupHealthWatch, models.WarmupHealthWatch, 0.7},
		{"fully recovered", 100.0 * 2 / 30, models.WarmupHealthThrottled, models.WarmupHealthHealthy, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := evaluateMetrics(&models.WarmupHealthMetrics{SpamPlacementRate: tc.rate, PlacementSample: 30}, tc.prior, now)
			if d.State != tc.state || warmupramp.HealthVolumeMultiplier(d.State) != tc.volume {
				t.Fatalf("decision = %+v, want %s at %g volume", d, tc.state, tc.volume)
			}
			if d.State == models.WarmupHealthThrottled && (!strings.Contains(d.Reason, "step down to watch") || !strings.Contains(d.Reason, "7.5%")) {
				t.Fatalf("reason overpromises recovery: %s", d.Reason)
			}
		})
	}
	for _, metrics := range []*models.WarmupHealthMetrics{
		{SpamPlacementRate: 6.67, PlacementSample: 30, ComplaintRate: complaintRateQuarantinePct, DeliveredLast30d: 1000},
		{SpamPlacementRate: 6.67, PlacementSample: 30, DeletionsLast7d: tamperingBlockStrikes},
	} {
		if d := evaluateMetrics(metrics, models.WarmupHealthThrottled, now); healthSeverity(d.State) < healthSeverity(models.WarmupHealthQuarantined) {
			t.Fatalf("placement recovery hid a severe issue: %+v", d)
		}
	}
}
