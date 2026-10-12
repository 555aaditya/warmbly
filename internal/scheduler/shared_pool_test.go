package scheduler

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// Campaigns sharing a pool used to break rotation ties by mailbox id, so every
// one of them reached for the same "next" mailbox (issue #942).
func TestRotationTiesSpreadAcrossCampaigns(t *testing.T) {
	pool := make([]AccountCandidate, 0, 12)
	for range 12 {
		pool = append(pool, candidateWithoutSenderRow(uuid.NewString(), 0, nil))
	}
	for _, mode := range []string{"round_robin", "least_recently_used"} {
		picked := map[uuid.UUID]int{}
		for range 60 {
			campaign := uuid.New()
			got := selectAccountByRotationMode(mode, pool, campaign)
			if got == nil {
				t.Fatalf("%s: expected a selection", mode)
			}
			if again := selectAccountByRotationMode(mode, pool, campaign); again.Account.ID != got.Account.ID {
				t.Fatalf("%s: one campaign broke the same tie two ways", mode)
			}
			picked[got.Account.ID]++
		}
		if len(picked) < 6 {
			t.Fatalf("%s: 60 campaigns shared only %d of 12 tied mailboxes: %v", mode, len(picked), picked)
		}
	}
}

func TestSendClaimWindowLeavesTheGraceAndCatchesCollisions(t *testing.T) {
	for _, c := range []struct {
		gap  int
		want time.Duration
	}{
		{600, 540 * time.Second},
		{90, 45 * time.Second},
		{60, 30 * time.Second},
		{0, 0},
	} {
		if got := sendClaimWindow(c.gap); got != c.want {
			t.Fatalf("sendClaimWindow(%d) = %s, want %s", c.gap, got, c.want)
		}
	}
}
