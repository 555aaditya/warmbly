package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

const MailTransportLockNamespace int64 = 285

func lockTransportMailbox(ctx context.Context, tx pgx.Tx, accountID uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,$2))`, accountID.String(), MailTransportLockNamespace)
	return err
}

func (r *emailAccountErrorRepository) RecordTransportSuccess(ctx context.Context, accountID uuid.UUID, operation models.MailTransportOperation, at time.Time, taskID *uuid.UUID, generation uuid.UUID) *errx.Error {
	if generation == uuid.Nil || !operation.Known() || !models.ValidMailTransportTime(at, time.Now()) {
		return nil
	}
	if operation == models.MailTransportSend && taskID == nil || operation == models.MailTransportSync && taskID != nil {
		return nil
	}
	tx, err := beginResultTx(ctx, r.DB)
	if err != nil {
		return errx.InternalError()
	}
	defer tx.Rollback(ctx)
	if err := lockTransportMailbox(ctx, tx, accountID); err != nil {
		return errx.InternalError()
	}
	var current uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT transport_generation FROM email_accounts WHERE id=$1 AND transport_generation=$2 FOR UPDATE`, accountID, generation).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return errx.InternalError()
	}
	_, err = tx.Exec(ctx, `INSERT INTO email_account_transport_recovery AS existing(email_account_id,operation,recovered_at,source_task_id)
	    SELECT id,$2,$3,$4 FROM email_accounts WHERE id=$1 AND transport_generation=$5 AND $3::timestamptz <= NOW()
	        AND ($2='sync' OR EXISTS(SELECT 1 FROM tasks WHERE id=$4 AND email_account_id=$1 AND send_result_state='sent'))
	    ON CONFLICT(email_account_id,operation) DO UPDATE
	        SET recovered_at=GREATEST(existing.recovered_at,EXCLUDED.recovered_at),
	        source_task_id=CASE WHEN EXCLUDED.recovered_at >= existing.recovered_at THEN EXCLUDED.source_task_id ELSE existing.source_task_id END`, accountID, operation, at.UTC(), taskID, generation)
	if err != nil {
		return errx.InternalError()
	}
	_, err = tx.Exec(ctx, `UPDATE email_account_errors report SET resolved_at=recovery.recovered_at,resolved_by=$2 || ' recovered'
	    FROM email_account_transport_recovery recovery, email_accounts account
	    WHERE report.email_account_id=$1 AND report.operation=$2 AND report.resolved_at IS NULL
	        AND report.observation_source IN ('worker','receipt')
	        AND account.id=report.email_account_id AND account.transport_generation=$3
	        AND recovery.email_account_id=report.email_account_id AND recovery.operation=report.operation
	        AND report.created_at < recovery.recovered_at - INTERVAL '1 second'
	        AND report.error_code IN ('SERVER_UNREACHABLE','CONNECTION_LOST','RESOURCE_NOT_FOUND','IMAP_UNKNOWN')`, accountID, operation, generation)
	if err != nil {
		return errx.InternalError()
	}
	if err := tx.Commit(ctx); err != nil {
		return errx.InternalError()
	}
	return nil
}
