package warmup

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// memberRepo holds one mailbox's rows by pool; the rest of the interface panics.
type memberRepo struct {
	repository.WarmupRepository

	rows     map[string]*models.WarmupParticipantHealth
	eligible bool
}

func (r memberRepo) IsPoolEligible(_ context.Context, _ uuid.UUID, pool string, _ bool) (bool, error) {
	return r.rows[pool] != nil && r.eligible, nil
}

func (r memberRepo) GetParticipantHealth(_ context.Context, _ uuid.UUID, pool string) (*models.WarmupParticipantHealth, error) {
	return r.rows[pool], nil
}

// The reply-back crosses tiers only on "not_in_pool"; reporting a non-member
// as ineligible dropped every reply to a partner from the other tier.
func TestCanParticipateNamesAMailboxOutsideThePool(t *testing.T) {
	free := map[string]*models.WarmupParticipantHealth{"free": {PoolType: "free", HealthState: models.WarmupHealthHealthy}}
	cases := []struct {
		name   string
		repo   memberRepo
		pool   string
		ok     bool
		reason string
	}{
		{"a member of the other tier is not in this pool", memberRepo{rows: free, eligible: true}, "premium", false, "not_in_pool"},
		{"a member that fails eligibility is refused as such", memberRepo{rows: free}, "free", false, "pool_authority_unavailable"},
		{"an eligible member participates", memberRepo{rows: free, eligible: true}, "free", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason, err := NewService(tc.repo).CanParticipate(context.Background(), uuid.New(), tc.pool)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ok != tc.ok || reason != tc.reason {
				t.Fatalf("CanParticipate = %v, %q; want %v, %q", ok, reason, tc.ok, tc.reason)
			}
		})
	}
}
