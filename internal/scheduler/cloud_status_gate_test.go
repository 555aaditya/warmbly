package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

// A hold that is only a missing Warmbly Cloud report stays a health gate, so
// nothing is sent, but carries its own mark for the sending plan.
func TestHealthGateMarksAMissingCloudReport(t *testing.T) {
	cloudOnly, held := uuid.New(), uuid.New()
	pass := &campaignPass{health: map[uuid.UUID]healthRead{
		cloudOnly: {state: models.WarmupHealthBlocked, known: true, cloudStatus: true},
		held:      {state: models.WarmupHealthBlocked, known: true},
	}}
	s := &schedulerService{}
	if gate, remaining := s.gateFor(context.Background(), pass, models.Email{ID: cloudOnly}, 10); gate.reason != gateHealth || !gate.cloudStatus || remaining != 0 {
		t.Fatalf("missing Cloud report gave %+v with %d left, want a health hold marked as a Cloud status", gate, remaining)
	}
	if gate, _ := s.gateFor(context.Background(), pass, models.Email{ID: held}, 10); gate.reason != gateHealth || gate.cloudStatus {
		t.Fatalf("real block gave %+v, want an unmarked health hold", gate)
	}
}

// The plan's mailbox row names a missing Cloud report as its own state with no
// "blocked" band, and keeps the band for a real hold.
func TestPlanMailboxRowNamesAMissingCloudReport(t *testing.T) {
	now := time.Now()
	cloudOnly, held := models.Email{ID: uuid.New(), CampaignLimit: 50}, models.Email{ID: uuid.New(), CampaignLimit: 50}
	pass := &campaignPass{campaign: &models.Campaign{DailyLimit: 50}, risk: models.OrgRiskTrusted, health: map[uuid.UUID]healthRead{
		cloudOnly.ID: {state: models.WarmupHealthBlocked, known: true, cloudStatus: true},
		held.ID:      {state: models.WarmupHealthQuarantined, known: true},
	}}
	s := &schedulerService{}
	d := s.planMailbox(t.Context(), pass, cloudOnly, 0, 0, nil, now, 10*3600, now.Add(10*time.Hour))
	if d.state != models.MailboxPlanCloudStatus || d.healthBand() != "" {
		t.Fatalf("missing Cloud report row = %q band %q, want cloud_status_hold with no band", d.state, d.healthBand())
	}
	d = s.planMailbox(t.Context(), pass, held, 0, 0, nil, now, 10*3600, now.Add(10*time.Hour))
	if d.state != models.MailboxPlanHealthHold || d.healthBand() != string(models.WarmupHealthQuarantined) {
		t.Fatalf("real hold row = %q band %q, want health_hold quarantined", d.state, d.healthBand())
	}
}

// A hold beside a missing Cloud report never lapses on the clock: its end can pass
// between the read and the check while the report is still missing.
func TestHealthGateHoldsOpenEndedWhileTheCloudReportIsMissing(t *testing.T) {
	ended := time.Now().Add(-time.Minute)
	missing, known := uuid.New(), uuid.New()
	pass := &campaignPass{health: map[uuid.UUID]healthRead{
		missing: {state: models.WarmupHealthQuarantined, blockedUntil: &ended, known: true, cloudMissing: true},
		known:   {state: models.WarmupHealthQuarantined, blockedUntil: &ended, known: true},
	}}
	s := &schedulerService{}
	if gate, _ := s.gateFor(context.Background(), pass, models.Email{ID: missing}, 10); gate.reason != gateHealth {
		t.Fatalf("an ended hold beside a missing Cloud report gave %+v, want it still held", gate)
	}
	if gate, _ := s.gateFor(context.Background(), pass, models.Email{ID: known}, 10); gate.reason == gateHealth {
		t.Fatalf("an ended hold with a current report gave %+v, want the health gate open", gate)
	}
}
