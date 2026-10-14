package repository

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/warmbly/warmbly/internal/models"
)

// Participation locks precede task locks so a stop can safely retire both directions.
func lockWarmupParticipation(ctx context.Context, tx pgx.Tx, task, mailbox uuid.UUID) error {
	rows, err := tx.Query(ctx, `SELECT id FROM email_accounts WHERE id IN (
		SELECT t.email_account_id FROM tasks t WHERE t.id=$1 AND t.email_account_id=$2 AND t.task_type='warmup'
		UNION SELECT w.target_account_id FROM warmup_tasks w JOIN tasks t ON t.id=w.task_id
		WHERE t.id=$1 AND t.email_account_id=$2 AND t.task_type='warmup'
		UNION SELECT wt.recipient_account_id FROM warmup_tokens wt JOIN tasks t ON t.id=wt.task_id
		WHERE t.id=$1 AND t.email_account_id=$2 AND t.task_type='warmup') ORDER BY id`, task, mailbox)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended($1,270))`, id.String()); err != nil {
			return err
		}
	}
	return nil
}

const stoppableWarmupDispatch = `t.task_type='warmup' AND (
	(t.status IN ('pending','active') AND t.send_executor_started_at IS NULL AND t.send_executor_result IS NULL
	 AND w.dispatch_started_at IS NULL AND w.dispatch_result IS NULL AND t.send_result_applied_at IS NULL
	 AND NOT EXISTS(SELECT 1 FROM outbound_attempts a WHERE a.task_id=t.id)
	 AND NOT EXISTS(SELECT 1 FROM warmup_tokens wt WHERE wt.task_id=t.id AND wt.sent_message_id<>'')
	 AND NOT EXISTS(SELECT 1 FROM warmup_received wr WHERE wr.task_id=t.id))
	OR (` + unstartedWarmupDispatch + `))`

func retireStoppedWarmupDispatches(ctx context.Context, tx pgx.Tx, mailbox uuid.UUID, direction string) error {
	before := time.Now().UTC()
	rows, err := tx.Query(ctx, `WITH linked AS (
		SELECT id AS task_id FROM tasks WHERE $3 IN ('both','sender') AND email_account_id=$2
		UNION SELECT task_id FROM warmup_tasks WHERE $3 IN ('both','receiver') AND target_account_id=$2
		UNION SELECT task_id FROM warmup_tokens WHERE $3 IN ('both','receiver') AND recipient_account_id=$2)
		SELECT t.id,t.email_account_id,t.send_result_applied_at IS NOT NULL FROM tasks t LEFT JOIN warmup_tasks w ON w.task_id=t.id
		JOIN linked l ON l.task_id=t.id WHERE `+stoppableWarmupDispatch+` ORDER BY t.id`, before, mailbox, direction)
	if err != nil {
		return err
	}
	type candidate struct {
		task, sender     uuid.UUID
		stalePreparation bool
	}
	var candidates []candidate
	senders := map[string]uuid.UUID{}
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.task, &c.sender, &c.stalePreparation); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, c)
		senders[c.sender.String()] = c.sender
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range candidates {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,268))`, c.task.String()); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(senders))
	for key := range senders {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,269))`, key); err != nil {
			return err
		}
	}
	for _, c := range candidates {
		tag, err := tx.Exec(ctx, `WITH eligible AS (
			SELECT t.id FROM tasks t LEFT JOIN warmup_tasks w ON w.task_id=t.id WHERE t.id=$4
			AND ($3 IN ('both','sender') AND t.email_account_id=$2 OR $3 IN ('both','receiver') AND
			(w.target_account_id=$2 OR EXISTS(SELECT 1 FROM warmup_tokens wt WHERE wt.task_id=t.id AND wt.recipient_account_id=$2)))
			AND `+stoppableWarmupDispatch+`)
			UPDATE tasks t SET status='cancelled',updated_at=NOW(),send_result_state='failed',send_result_applied_at=NOW(),send_released_at=NOW()
			FROM eligible e WHERE e.id=t.id`, before, mailbox, direction, c.task)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := refundWarmupCharge(ctx, tx, c.task, c.sender); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM warmup_tokens WHERE task_id=$1`, c.task); err != nil {
			return err
		}
		if c.stalePreparation {
			if _, err := tx.Exec(ctx, `UPDATE email_accounts SET send_recovery_reason='unknown'
				WHERE id=$1 AND send_recovery_task_id=$2 AND send_recovery_reason='conflict'`, c.sender, c.task); err != nil {
				return err
			}
		}
		if err := persistSendHold(ctx, tx, c.sender, models.SendEmailResult{TaskID: c.task}, "failed"); err != nil {
			return err
		}
	}
	return nil
}
