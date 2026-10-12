package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/jobrun"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/observability/arrivaldiag"
)

// StartWarmupInboxCleanup repairs old leaks in bounded batches, retrying verification failures.
func (s *JobsService) StartWarmupInboxCleanup(ctx context.Context) {
	if s.UniboxRepository == nil {
		return
	}
	var afterID uuid.UUID
	var nextPass time.Time
	jobrun.Loop(ctx, "warmup_inbox_cleanup", time.Minute, true, func(ctx context.Context) error {
		if time.Now().Before(nextPass) {
			return nil
		}
		batchCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		next, done, err := s.cleanWarmupInboxBatch(batchCtx, afterID)
		afterID = next
		if err == nil && done {
			afterID = uuid.Nil
			nextPass = time.Now().Add(24 * time.Hour)
		}
		return err
	})
}

// fileWarmupOutOfMailbox persists filing separately from best-effort engagement.
func (s *JobsService) fileWarmupOutOfMailbox(ctx context.Context, e *models.JobEventNewEmail) error {
	defer arrivaldiag.Begin(ctx, arrivaldiag.WarmupFile)()
	if s.Publisher == nil || s.EmailRepository == nil || e.Message == nil {
		return nil
	}
	if models.NormalizeFolder(e.Message.Folder, e.Message.Flags) == models.FolderTrash || e.Message.ProviderFolder == models.FolderTrash {
		return nil
	}
	account, xerr := s.EmailRepository.GetByID(ctx, e.Message.EmailID)
	if xerr != nil {
		if xerr.Code == errx.NotFound {
			return nil
		}
		return fmt.Errorf("%w: warmup mailbox lookup: %w", errWarmupVerification, xerr)
	}
	if account == nil {
		return nil
	}
	if s.WarmupRecoveryRepo != nil {
		if err := s.WarmupRecoveryRepo.RememberMessage(ctx, e.Message.EmailID, e.Message.MessageID, e.Message.InternalDate); err != nil {
			return fmt.Errorf("%w: remember warmup message: %w", errWarmupVerification, err)
		}
	}
	placement, folder := account.WarmupFiling()
	if placement == models.WarmupPlacementInbox {
		return nil
	}
	action := models.WarmupEmailAction{
		UserID:             e.UserID,
		EmailID:            e.Message.EmailID,
		GmailID:            e.Message.GmailID,
		UID:                e.Message.UID,
		MailboxUIDValidity: e.Message.Mailbox,
		MailboxFolder:      e.Message.FolderPath,
		RFCMessageID:       e.Message.MessageID,
		InternalID:         e.Message.ID.String(),
		Actions:            []string{models.WarmupActionFile},
		Placement:          placement,
		TargetFolder:       folder,
	}
	if s.WarmupRecoveryRepo != nil {
		id, err := s.WarmupRecoveryRepo.EnqueueFiling(ctx, action)
		if err != nil {
			return fmt.Errorf("%w: persist warmup filing: %w", errWarmupVerification, err)
		}
		action.FilingID = id.String()
	}
	if account.WorkerID == nil {
		if s.WarmupRecoveryRepo != nil {
			return nil
		}
		return fmt.Errorf("warmup filing: mailbox has no assigned worker")
	}
	if s.WarmupRecoveryRepo == nil {
		s.markSelfMove(ctx, e.Message.EmailID, e.Message.MessageID)
	}
	// Durable filings use presence verification instead of assuming every attempt moves mail.
	err := s.Publisher.PublishWarmupAction(ctx, *account.WorkerID, &action)
	if s.WarmupRecoveryRepo != nil {
		return nil // The persisted filing survives a failed publish.
	}
	if err != nil {
		return fmt.Errorf("%w: warmup file: %w", errWarmupVerification, err)
	}
	return nil
}

// StartPendingWarmupVerification drains arrivals held during verification outages.
func (s *JobsService) StartPendingWarmupVerification(ctx context.Context) {
	jobrun.Loop(ctx, "pending_warmup_verification", time.Minute, true, func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		return s.retryPendingWarmupVerification(ctx)
	})
}

func (s *JobsService) retryPendingWarmupVerification(ctx context.Context) error {
	events, err := s.UniboxRepository.ClaimPendingWarmupVerification(ctx, 25)
	if err != nil {
		return err
	}
	var failures []error
	for _, e := range events {
		if err := s.UniboxRepository.ProcessPendingWarmupVerification(ctx, e.Message.ID, func(current *models.JobEventNewEmail) error {
			return s.ingestNewEmail(ctx, current)
		}); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (s *JobsService) cleanWarmupInboxBatch(ctx context.Context, afterID uuid.UUID) (uuid.UUID, bool, error) {
	const batchSize = 100
	events, err := s.UniboxRepository.ListWarmupReviewCandidates(ctx, afterID, batchSize)
	if err != nil {
		return afterID, false, err
	}
	for _, e := range events {
		// Historical cleanup requires an exact identifier, never a reused subject.
		candidate, message := e, *e.Message
		message.Subject = ""
		candidate.Message = &message
		warmup, err := s.isKnownWarmupEmail(ctx, &candidate)
		if err != nil {
			return afterID, false, err
		}
		if !warmup {
			// A reply in a warmup thread that got in before ancestry was
			// checked, or while the turn it answers was still unknown.
			if warmup, err = s.isWarmupThreadReply(ctx, &candidate); err != nil {
				return afterID, false, err
			}
		}
		if warmup {
			// Deleting the Unibox row only takes it out of OUR inbox. The copy
			// in the customer's own mailbox is what they are looking at, and
			// nothing else ever goes back for it, so file it here too (#583).
			// The Unibox row is the retry record: it stays until the filing
			// action is on the bus, so a publish or lookup failure is
			// re-offered next pass instead of hiding the leak for good.
			if err := s.fileWarmupOutOfMailbox(ctx, &candidate); err != nil {
				return afterID, false, err
			}
			if err := s.UniboxRepository.Delete(ctx, e.UserID, e.Message.ID); err != nil {
				return afterID, false, err
			}
			if s.StreamingPublisher != nil {
				s.StreamingPublisher.PublishEmailDeleted(ctx, s.emailInboxEvent(ctx, e.UserID, e.Message))
			}
		}
		afterID = e.Message.ID
	}
	return afterID, len(events) < batchSize, nil
}
