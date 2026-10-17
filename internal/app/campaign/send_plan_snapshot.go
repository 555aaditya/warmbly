package campaign

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/warmbly/warmbly/internal/jobrun"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// The send plan is expensive on a large campaign: the planner walks lead supply
// and per-mailbox history, which on tens of thousands of leads took tens of
// seconds on the read and intermittently timed out (#797). The walk runs here,
// on a schedule, and the read endpoint serves the stored result.
const (
	// sendPlanSnapshotInterval is how often every active campaign is re-walked.
	// A send plan is "today's" figures: a minute is far finer than the day it
	// describes, and it keeps a snapshot's ComputedAt within a minute of now.
	sendPlanSnapshotInterval = time.Minute
	// sendPlanSnapshotMaxAge bounds a snapshot's age before it is treated as
	// stale, so a snapshotter that stalled or fell behind no longer serves an
	// aged snapshot as fresh. Read path and snapshotter share this one window.
	sendPlanSnapshotMaxAge = 3 * sendPlanSnapshotInterval
	// sendPlanSnapshotBatch is how many active campaigns one keyset page covers.
	sendPlanSnapshotBatch = 200
	// sendPlanSnapshotPerCompute bounds one campaign's walk so a wedged planner
	// never stalls the pass.
	sendPlanSnapshotPerCompute = 45 * time.Second
	// sendPlanSnapshotPause spaces the walks so a large fleet is not a
	// thundering herd on the database.
	sendPlanSnapshotPause = 50 * time.Millisecond
)

var errPlannerUnavailable = errors.New("send planning is not available")

// planVersionKey keys a plan on the campaign's own version, so an edit or a
// start/stop is answered fresh while two viewers of an unchanged campaign share
// one computation. Shared by the read path and the snapshotter.
func planVersionKey(campaign *models.Campaign) string {
	return campaign.ID.String() + "|" + campaign.Status + "|" + campaign.UpdatedAt.UTC().Format(time.RFC3339Nano)
}

// planBudgetDay is the UTC day a plan counts; daily counters reset at UTC
// midnight whatever the campaign's timezone, so a snapshot from an earlier day
// is stale. Matches PlanCampaignDay's own Day stamp.
func planBudgetDay(now time.Time) string {
	return now.UTC().Format("2006-01-02")
}

// computeAndStore walks the campaign's send plan through the scheduler and
// writes it as the campaign's snapshot. Persistence is best effort: the plan is
// returned whatever the write does, so a read's cold fallback still answers and
// the next pass retries the write.
func (s *campaignService) computeAndStore(ctx context.Context, campaign *models.Campaign, orgID uuid.UUID, key string) (*models.CampaignSendPlan, error) {
	planner, ok := s.planner()
	if !ok {
		return nil, errPlannerUnavailable
	}
	plan, err := planner.PlanCampaignDay(ctx, campaign.ID, s.orgDailyLimit(ctx, orgID))
	if err != nil {
		return nil, err
	}
	if s.planSnapshotRepo != nil {
		// A planner walk for an older campaign version can finish after a newer
		// walk has already stored its snapshot. Re-read the campaign's current
		// version and skip the write when the plan computed here is already
		// superseded, so a late older walk cannot overwrite a newer snapshot.
		// The next pass recomputes for the current version; the returned plan is
		// still the one this caller asked for.
		if current, cerr := s.campaignRepository.GetByID(ctx, campaign.ID); cerr == nil && current != nil && planVersionKey(current) != key {
			return plan, nil
		}
		snap := &repository.CampaignSendPlanSnapshot{
			CampaignID:     campaign.ID,
			OrganizationID: orgID,
			Day:            plan.Day,
			VersionKey:     key,
			Plan:           plan,
			ComputedAt:     plan.ComputedAt,
		}
		if err := s.planSnapshotRepo.Upsert(ctx, snap); err != nil {
			log.Warn().Err(err).Str("campaign_id", campaign.ID.String()).Msg("send plan snapshot: upsert failed")
		}
	}
	return plan, nil
}

// refreshPlanAsync recomputes a stale snapshot in the background so the read
// that saw it stale returns the last good figures immediately. It runs on a
// request-independent context, bounded inside getOrCompute, and coalesced by
// key so repeated stale polls trigger one walk rather than one per viewer.
func (s *campaignService) refreshPlanAsync(campaign *models.Campaign, orgID uuid.UUID, key string) {
	if s.planCache == nil {
		return
	}
	go func() {
		_, _ = s.planCache.getOrCompute(context.Background(), key, func(c context.Context) (*models.CampaignSendPlan, error) {
			return s.computeAndStore(c, campaign, orgID, key)
		})
	}()
}

// StartSendPlanSnapshotter re-walks every active campaign's send plan on an
// interval and stores it, so the read endpoint serves a stored snapshot instead
// of computing on the request. It runs through jobrun.Loop, whose per-pass
// claim means exactly one backend replica walks the fleet each tick rather than
// every replica repeating the work; runOnBoot seeds snapshots promptly after a
// restart. A no-op without the snapshot store or a planner.
func (s *campaignService) StartSendPlanSnapshotter(ctx context.Context, interval, maxAge time.Duration) {
	if maxAge > 0 {
		s.planMaxAge.Store(int64(maxAge))
	}
	if s.planSnapshotRepo == nil || s.campaignRepository == nil {
		return
	}
	if _, ok := s.planner(); !ok {
		return
	}
	if interval <= 0 {
		interval = sendPlanSnapshotInterval
	}
	jobrun.Loop(ctx, "send_plan_snapshot", interval, true, func(ctx context.Context) error {
		s.snapshotActiveCampaignsOnce(ctx)
		return nil
	})
}

// sendPlanMaxAge is the configured staleness window, else sendPlanSnapshotMaxAge.
func (s *campaignService) sendPlanMaxAge() time.Duration {
	if v := s.planMaxAge.Load(); v > 0 {
		return time.Duration(v)
	}
	return sendPlanSnapshotMaxAge
}

// snapshotActiveCampaignsOnce walks every active campaign once, skipping those
// whose stored snapshot is already current, and logs the pass's work.
func (s *campaignService) snapshotActiveCampaignsOnce(ctx context.Context) {
	start := time.Now()
	after := uuid.Nil
	computed, skipped := 0, 0
	for {
		ids, err := s.campaignRepository.ListActiveCampaignIDs(ctx, after, sendPlanSnapshotBatch)
		if err != nil {
			if ctx.Err() == nil {
				log.Warn().Err(err).Msg("send plan snapshot: could not list active campaigns")
			}
			return
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if ctx.Err() != nil {
				return
			}
			switch s.snapshotCampaign(ctx, id) {
			case "computed":
				computed++
			case "skipped":
				skipped++
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(sendPlanSnapshotPause):
			}
		}
		after = ids[len(ids)-1]
		if len(ids) < sendPlanSnapshotBatch {
			break
		}
	}
	if computed > 0 {
		log.Debug().
			Int("computed", computed).
			Int("skipped", skipped).
			Dur("elapsed", time.Since(start)).
			Msg("send plan snapshot: refreshed active campaigns")
	}
}

// snapshotCampaign walks and stores one campaign's plan, bounded by its own
// timeout. A snapshot already current (same version, same budget day, young
// enough) is left alone so the expensive planner walk is not repeated. Returns
// "computed", "skipped", or "" when nothing applied.
func (s *campaignService) snapshotCampaign(ctx context.Context, id uuid.UUID) string {
	campaign, err := s.campaignRepository.GetByID(ctx, id)
	if err != nil || campaign == nil || campaign.Status != "active" || campaign.OrganizationID == nil {
		return ""
	}
	orgID := *campaign.OrganizationID
	key := planVersionKey(campaign)
	// Skip the walk when the stored snapshot already matches this version and
	// budget day and is within the freshness window; only stale, mismatched or
	// missing snapshots are recomputed.
	if snap, err := s.planSnapshotRepo.Get(ctx, orgID, campaign.ID); err == nil && snap != nil &&
		snap.VersionKey == key && snap.Day == planBudgetDay(time.Now()) &&
		time.Since(snap.ComputedAt) <= s.sendPlanMaxAge() {
		return "skipped"
	}
	cctx, cancel := context.WithTimeout(ctx, sendPlanSnapshotPerCompute)
	defer cancel()
	if _, err := s.computeAndStore(cctx, campaign, orgID, key); err != nil {
		if ctx.Err() == nil {
			log.Warn().Err(err).Str("campaign_id", id.String()).Msg("send plan snapshot: compute failed")
		}
		return ""
	}
	return "computed"
}
