package advanced

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// WithdrawHistoricalReplyEffects removes only effects with exact saved provenance.
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
			log.Warn().Err(err).Str("message_id", page.Replies[i].ID.String()).Msg("historical reply cleanup failed")
			return afterID, false, err
		}
	}
	return page.NextCursor, page.Done, nil
}

func (s *service) withdrawHistoricalReply(ctx context.Context, h *repository.HistoricalReply) error {
	intents, err := s.repo.ListReplyIntentsForMessage(ctx, h.OrganizationID, h.ID)
	if err != nil || len(intents) == 0 {
		return err
	}
	ids := make([]uuid.UUID, 0, len(intents))
	for _, in := range intents {
		if strings.Contains(in.ActionTaken, "created_crm_task") {
			rawTaskID, _ := in.Metadata["crm_task_id"].(string)
			taskID, err := uuid.Parse(rawTaskID)
			if err != nil || taskID == uuid.Nil {
				continue // Legacy tasks without an exact link must not be guessed.
			}
			if s.crmRepo == nil {
				return fmt.Errorf("historical reply cleanup: CRM repository unavailable")
			}
			cancelled, err := s.crmRepo.CancelUntouchedCRMTask(ctx, h.OrganizationID, h.ContactID, taskID)
			if err != nil {
				return err
			}
			if cancelled {
				s.pushCRM(ctx, h.OrganizationID, models.CRMObjectTask, taskID)
				if s.audit != nil {
					s.audit.LogAction(ctx, h.OrganizationID, uuid.Nil, models.AuditActionUpdate, models.AuditEntityCRMTask, &taskID, "", "",
						map[string]string{"status": "cancelled"},
						map[string]string{"reason": "opened for mail that arrived before its sender was a contact, which is not a reply"})
				}
			}
		}
		ids = append(ids, in.ID)
	}
	if len(ids) == 0 {
		return nil
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
