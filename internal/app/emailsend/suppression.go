package emailsend

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/mailhdr"
)

// checkSuppression refuses the send when any recipient is on the workspace
// suppression list, naming the first one and the entry that matched it. A
// forward relays mail already received to people picked by hand, so the list
// does not apply to it; send admission makes the same call.
func (s *emailSendService) checkSuppression(ctx context.Context, orgID uuid.UUID, req *SendEmailRequest) *errx.Error {
	if s.suppression == nil || req.Forward != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, list := range [][]string{req.To, req.CC, req.BCC} {
		for _, raw := range list {
			addr := strings.ToLower(mailhdr.Bare(raw))
			if addr == "" || seen[addr] {
				continue
			}
			seen[addr] = true
			entry, err := s.suppression.IsRecipientSuppressed(ctx, orgID, addr)
			if err != nil {
				return errx.New(errx.Internal, "failed to read the suppression list")
			}
			if entry != nil {
				return suppressedRecipientError(addr, entry)
			}
		}
	}
	return nil
}

func suppressedRecipientError(addr string, entry *models.SuppressedRecipient) *errx.Error {
	msg := addr + " is on this workspace's suppression list"
	if entry.Kind == models.SuppressionKindDomain {
		msg += " through its domain " + entry.Email
	}
	if reason := strings.TrimSpace(entry.Reason); reason != "" {
		msg += fmt.Sprintf(" (%s)", reason)
	}
	msg += ". Remove the entry under Contacts > Suppression list to email it."
	return errx.NewWithIdentifier(errx.BadRequest, ErrCodeRecipientSuppressed, msg)
}
