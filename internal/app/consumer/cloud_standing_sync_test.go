package jobs

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/app/warmup"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type partialStandingSyncer struct {
	changes []models.CloudLinkStandingChange
	xerr    *errx.Error
}

func (s partialStandingSyncer) SyncStanding(context.Context) ([]models.CloudLinkStandingChange, *errx.Error) {
	return s.changes, s.xerr
}

type partialStandingRiskRepo struct {
	repository.WorkerRepository
	bands map[uuid.UUID]models.EmailRiskBand
}

func (r partialStandingRiskRepo) SetEmailAccountRiskBand(_ context.Context, id uuid.UUID, band models.EmailRiskBand) error {
	r.bands[id] = band
	return nil
}

type partialStandingPublisher struct {
	warmup.Service
	changes []models.CloudLinkStandingChange
}

func (p *partialStandingPublisher) PublishHealthTransition(_ context.Context, id uuid.UUID, oldState, newState models.WarmupHealthState, reason string) {
	p.changes = append(p.changes, models.CloudLinkStandingChange{EmailAccountID: id, Previous: oldState, Current: newState, Reason: reason})
}

func TestCloudStandingSyncPublishesPersistedChangesWhileReportingIndependentFailures(t *testing.T) {
	for _, failure := range []*errx.Error{nil, errx.NewWithIdentifier(errx.Conflict, "pool_link_mailbox_conflict", "An unrelated enrollment needs review.")} {
		change := models.CloudLinkStandingChange{EmailAccountID: uuid.New(), Previous: models.WarmupHealthHealthy, Current: models.WarmupHealthQuarantined, Reason: "complaints"}
		risk := partialStandingRiskRepo{bands: map[uuid.UUID]models.EmailRiskBand{}}
		publisher := &partialStandingPublisher{}
		s := &JobsService{WorkerRepo: risk, WarmupService: publisher}
		got := s.syncCloudStanding(context.Background(), partialStandingSyncer{changes: []models.CloudLinkStandingChange{change}, xerr: failure})
		if (failure == nil && got != nil) || (failure != nil && got != failure) {
			t.Fatalf("sync lost its job outcome: %v", got)
		}
		if risk.bands[change.EmailAccountID] != models.RiskBandFromHealth(change.Current) || len(publisher.changes) != 1 || publisher.changes[0] != change {
			t.Fatal("an independent failure suppressed the confirmed standing transition")
		}
	}
}
