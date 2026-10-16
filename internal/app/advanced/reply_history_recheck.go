package advanced

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// replyEffectWindow is how soon after a message is processed the intent and
// follow-up task it produced are written.
const replyEffectWindow = 2 * time.Minute

// WithdrawHistoricalReplyEffects reads one page of mail that predates its
// sender's contact and was still handled as a reply, deleting the intent each
// one recorded and cancelling the follow-up task it opened while nobody has
// touched it. Opt-outs are the reply opt-out recheck's.
func (s *service) WithdrawHistoricalReplyEffects(ctx context.Context, since time.Time, afterID uuid.UUID) (uuid.UUID, bool, error) {
	if s.uniboxRepo == nil {
		return afterID, true, nil
	}
	page, err := s.uniboxRepo.ListHistoricalReplies(ctx, since, afterID)
	if err != nil {
		return afterID, false, err
	}
	for i := range page.Replies {
		if err := s.withdrawHistoricalReply(ctx, &page.Replies[i]); err != nil {
			// Left for the next pass, which reads the same message again.
			log.Warn().Err(err).Str("message_id", page.Replies[i].ID.String()).Msg("historical reply cleanup failed")
		}
	}
	return page.NextCursor, page.Done, nil
}

func (s *service) withdrawHistoricalReply(ctx context.Context, h *repository.HistoricalReply) error {
	// The subject pins an intent to its message; the window to its processing.
	intents, err := s.repo.ListReplyIntentsFor(ctx, h.OrganizationID, h.Sender, h.Subject,
		h.ProcessedAt.Add(-time.Minute), h.ProcessedAt.Add(replyEffectWindow))
	if err != nil || len(intents) == 0 {
		return err
	}
	ids := make([]uuid.UUID, 0, len(intents))
	for _, in := range intents {
		if strings.Contains(in.ActionTaken, "created_crm_task") && s.crmRepo != nil {
			cancelled, err := s.crmRepo.CancelUntouchedCRMTasks(ctx, h.OrganizationID, h.ContactID, replyTaskTitle(in.Intent, in.ContactEmail),
				h.ProcessedAt.Add(-time.Minute), h.ProcessedAt.Add(replyEffectWindow))
			if err != nil {
				return err
			}
			for _, id := range cancelled {
				s.pushCRM(ctx, h.OrganizationID, models.CRMObjectTask, id)
				if s.audit != nil {
					s.audit.LogAction(ctx, h.OrganizationID, uuid.Nil, models.AuditActionUpdate, models.AuditEntityCRMTask, &id, "", "",
						map[string]string{"status": "cancelled"},
						map[string]string{"reason": "opened for mail that arrived before its sender was a contact, which is not a reply"})
				}
			}
		}
		ids = append(ids, in.ID)
	}
	if err := s.repo.DeleteReplyIntents(ctx, h.OrganizationID, ids); err != nil {
		return err
	}
	log.Info().
		Str("organization_id", h.OrganizationID.String()).
		Str("message_id", h.ID.String()).
		Int("intents", len(ids)).
		Msg("historical reply cleanup: withdrew what mail from before its contact produced")
	return nil
}
