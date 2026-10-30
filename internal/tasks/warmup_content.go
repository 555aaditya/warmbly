package tasks

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/generation"
	"github.com/warmbly/warmbly/internal/repository"
)

// warmupContent is the rendered content for one warmup send plus the cohort
// metadata recorded on the verification token for the A/B harness.
type warmupContent struct {
	subject          string
	body             string
	theme            string
	contentSource    string
	conversationID   *uuid.UUID
	scenarioVersion  string
	renderingVersion string
	maxTurns         int
}

// getWarmupSettings caches selection settings briefly between warmup sends.
func (s *tasksService) getWarmupSettings(ctx context.Context) models.WarmupGenerationSettings {
	if s.warmupContentRepo == nil {
		return models.DefaultWarmupGenerationSettings()
	}
	if c := s.warmupSettings; c != nil {
		c.mu.RLock()
		fresh := !c.fetched.IsZero() && time.Since(c.fetched) < 60*time.Second
		val := c.val
		c.mu.RUnlock()
		if fresh {
			return val
		}
	}
	set, err := s.warmupContentRepo.GetGenerationSettings(ctx)
	if err != nil || set == nil {
		return models.DefaultWarmupGenerationSettings()
	}
	if c := s.warmupSettings; c != nil {
		c.mu.Lock()
		c.val = *set
		c.fetched = time.Now()
		c.mu.Unlock()
	}
	return *set
}

var errWarmupContentUnavailable = errors.New("fresh approved warmup content is unavailable")

// Reserve an approved pooled plan unseen by both participants; keep replies pinned.
func (s *tasksService) pickNewWarmupContent(ctx context.Context, account Email, recipientID, taskID uuid.UUID) (warmupContent, error) {
	settings := s.getWarmupSettings(ctx)

	if settings.Enabled && s.warmupContentRepo != nil {
		// Content is drawn from the shared library by segment; tier doesn't
		// affect content (only mailbox reputation isolation uses the pool).
		segment := strings.TrimSpace(account.WarmupTag)
		conv, err := s.warmupContentRepo.PickConversation(ctx, repository.WarmupContentPick{Segment: segment, TaskID: taskID, SenderID: account.ID, RecipientID: recipientID})
		if err == nil && conv != nil && conv.ScenarioVersion != nil && conv.RenderingVersion != nil && conv.Status == models.WarmupConversationActive && conv.ReplyEligible && conv.LintPassed && conv.SemanticReview == "passed" {
			if err := generation.ValidateFreshWarmup(generation.Conversation{Subject: conv.Subject, Description: conv.Description, Messages: conv.Messages}); err != nil {
				return warmupContent{}, errWarmupContentUnavailable
			}
			c := Conversation{ID: conv.ID, Subject: conv.Subject, Theme: conv.Theme, Description: conv.Description, Messages: conv.Messages}
			body := GenerateConversationOpeningEmail(c, account)
			subject := strings.TrimSpace(conv.Subject)
			if body != "" && subject != "" && !strings.ContainsAny(subject, "<>{}\x00\r\n") {
				id := conv.ID
				return warmupContent{
					subject:          subject,
					body:             body,
					theme:            conv.Theme,
					contentSource:    models.WarmupContentSourceAI,
					conversationID:   &id,
					scenarioVersion:  *conv.ScenarioVersion,
					renderingVersion: *conv.RenderingVersion,
					maxTurns:         len(conv.Messages) + 1,
				}, nil
			}
		}
	}

	return warmupContent{}, errWarmupContentUnavailable
}
