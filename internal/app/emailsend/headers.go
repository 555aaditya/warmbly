package emailsend

import (
	"strings"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/pkg/mailhdr"
)

// Response codes for a header-bound value that is refused.
const (
	ErrCodeInvalidRecipient = "invalid_recipient"
	ErrCodeInvalidMessageID = "invalid_message_id"
	// ErrCodeRecipientSuppressed refuses a send to an address on the
	// workspace suppression list, directly or through its domain.
	ErrCodeRecipientSuppressed = "recipient_suppressed"
)

// checkHeaderValues requires every to, cc and bcc entry to be exactly one
// address and every in_reply_to entry to be one Message-ID. Blank entries are
// dropped; at least one recipient must remain in to.
func checkHeaderValues(req *SendEmailRequest) *errx.Error {
	lists := []*[]string{&req.To, &req.CC, &req.BCC}
	for _, list := range lists {
		kept := (*list)[:0:0]
		for _, a := range *list {
			if strings.TrimSpace(a) == "" {
				continue
			}
			if !mailhdr.ValidAddress(a) {
				return errx.NewWithIdentifier(errx.BadRequest, ErrCodeInvalidRecipient,
					"Every to, cc and bcc entry must be exactly one email address, such as ana@example.com or Ana <ana@example.com>.")
			}
			kept = append(kept, a)
		}
		*list = kept
	}
	if len(req.To) == 0 {
		return errx.NewWithIdentifier(errx.BadRequest, ErrCodeInvalidRecipient, "At least one to address is required.")
	}
	ids := req.InReplyTo[:0:0]
	for _, id := range req.InReplyTo {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if !mailhdr.ValidMessageID(id) {
			return errx.NewWithIdentifier(errx.BadRequest, ErrCodeInvalidMessageID,
				"Every in_reply_to entry must be one Message-ID, such as <id@example.com>.")
		}
		ids = append(ids, strings.TrimSpace(id))
	}
	req.InReplyTo = ids
	return nil
}
