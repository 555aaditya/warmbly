package scheduler

import (
	"context"
	"time"

	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// SendClaimer is the optional capability a campaign pass uses to take its
// mailbox before building the send.
type SendClaimer interface {
	ClaimSend(ctx context.Context, account *models.Email) (bool, error)
}

// ClaimSend takes the mailbox for a send now. False means another pass took
// it inside its gap since this pass read the pool, and this one should look
// again rather than send.
func (s *schedulerService) ClaimSend(ctx context.Context, account *models.Email) (bool, error) {
	claimer, ok := s.taskRepo.(repository.MailboxSendClaimer)
	if !ok {
		return true, nil
	}
	gap := s.behaviorGapFloor(s.behaviorFor(ctx, account), time.Now(), account.MinWaitTime)
	return claimer.ClaimMailboxSend(ctx, account.ID, sendClaimWindow(gap))
}

// sendClaimWindow is how long one claim holds a mailbox: its gap less the
// grace a due pass may send early by, never under half the gap.
func sendClaimWindow(gapSeconds int) time.Duration {
	window := max(gapSeconds-config.CampaignNotDueGraceSeconds, gapSeconds/2)
	return time.Duration(window) * time.Second
}
