package jobs

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/jobrun"
	"github.com/warmbly/warmbly/internal/models"
)

// CloudStandingSyncer is the part of the cloud link that mirrors the cloud's
// warmup verdicts onto the mailboxes it warms.
type CloudStandingSyncer interface {
	SyncStanding(ctx context.Context) ([]models.CloudLinkStandingChange, *errx.Error)
}

// StartCloudStandingSync keeps the warmup standing Warmbly Cloud reports for
// each enrolled mailbox current, so campaign gates, pacing and the dashboard
// hold a cloud quarantine the same as a local one. A no-op when not linked.
func (s *JobsService) StartCloudStandingSync(ctx context.Context, interval time.Duration) {
	syncer, ok := s.CloudLink.(CloudStandingSyncer)
	if !ok || syncer == nil {
		return
	}
	jobrun.Loop(ctx, "cloud_standing_sync", interval, true, func(ctx context.Context) error {
		return s.syncCloudStanding(ctx, syncer)
	})
}

func (s *JobsService) syncCloudStanding(ctx context.Context, syncer CloudStandingSyncer) error {
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	changes, xerr := syncer.SyncStanding(runCtx)
	for _, c := range changes {
		s.markRiskBandFromWarmupHealth(runCtx, c.EmailAccountID, &models.WarmupParticipantHealth{HealthState: c.Current})
		if s.WarmupService != nil {
			s.WarmupService.PublishHealthTransition(runCtx, c.EmailAccountID, c.Previous, c.Current, c.Reason)
		}
	}
	if len(changes) > 0 {
		log.Info().Int("transitions", len(changes)).Msg("cloud standing sync: warmup standing changed")
	}
	if xerr != nil {
		return xerr
	}
	return nil
}
