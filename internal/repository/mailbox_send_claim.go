package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MailboxSendClaimer takes a mailbox for one campaign send.
type MailboxSendClaimer interface {
	ClaimMailboxSend(ctx context.Context, mailboxID uuid.UUID, window time.Duration) (bool, error)
}

// ClaimMailboxSend takes the mailbox for a send now, refused while another
// claim or a reserved send of any lane sits inside window. The conditional
// update serialises concurrent passes on the row, so two campaigns sharing a
// mailbox cannot both win the same gap.
func (r *taskRepository) ClaimMailboxSend(ctx context.Context, mailboxID uuid.UUID, window time.Duration) (bool, error) {
	var claimed bool
	err := r.db.QueryRow(ctx, `
		UPDATE email_accounts ea SET send_claimed_at = NOW()
		WHERE ea.id = $1
		  AND (ea.send_claimed_at IS NULL OR ea.send_claimed_at <= NOW() - make_interval(secs => $2))
		  AND NOT EXISTS (
		    SELECT 1 FROM tasks t
		    WHERE t.email_account_id = $1
		      AND t.send_reserved_at > NOW() - make_interval(secs => $2)
		      AND t.send_released_at IS NULL)
		RETURNING true
	`, mailboxID, window.Seconds()).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return claimed, err
}
