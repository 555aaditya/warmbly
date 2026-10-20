package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/warmbly/warmbly/internal/models"
)

const retirableUnknownWarmupSend = `t.task_type='warmup' AND t.status IN ('completed','failed','dead_lettered')
	AND t.send_result_state='unknown' AND t.warmup_send_retired_at IS NULL
	AND t.send_result_applied_at IS NULL AND t.send_released_at IS NULL
	AND ea.send_recovery_hold AND ea.send_recovery_reason='unknown' AND ea.send_recovery_task_id=t.id
	AND (t.send_executor_nonce IS NOT NULL OR w.dispatch_nonce IS NOT NULL)
	AND (t.send_executor_started_at IS NOT NULL OR w.dispatch_started_at IS NOT NULL
	 OR EXISTS(SELECT 1 FROM outbound_attempts a WHERE a.task_id=t.id))
	AND COALESCE(t.send_reserved_at,t.completed_at,t.updated_at)<$1
	AND (t.send_executor_started_at IS NULL OR t.send_executor_started_at<$1)
	AND (w.dispatch_started_at IS NULL OR w.dispatch_started_at<$1)
	AND NOT EXISTS(SELECT 1 FROM warmup_tokens wt WHERE wt.task_id=t.id AND wt.sent_message_id<>'')
	AND NOT EXISTS(SELECT 1 FROM warmup_received wr WHERE wr.task_id=t.id)
	AND NOT EXISTS(SELECT 1 FROM send_recovery_resolutions sr WHERE sr.recovery_task_id=t.id
	 AND (sr.confirmed_result IS NOT NULL OR sr.conflict_detected_at IS NOT NULL))`

const retiredWarmupHold = `t.warmup_send_retired_at IS NOT NULL
	AND ea.send_recovery_hold AND ea.send_recovery_reason='unknown' AND ea.send_recovery_task_id=t.id`

func (r *taskRepository) RetireUnknownWarmupSends(ctx context.Context, before time.Time, limit int) (int, error) {
	limit = min(max(limit, 1), 500)
	var after *time.Time
	afterTask := uuid.Nil
	retired := 0
	for retired < limit {
		rows, err := r.db.Query(ctx, `SELECT t.id,t.email_account_id,COALESCE(t.send_reserved_at,t.completed_at,t.updated_at) FROM tasks t
			JOIN warmup_tasks w ON w.task_id=t.id JOIN email_accounts ea ON ea.id=t.email_account_id
			WHERE ((`+retirableUnknownWarmupSend+`) OR (`+retiredWarmupHold+`))
			AND ($3::timestamptz IS NULL OR (COALESCE(t.send_reserved_at,t.completed_at,t.updated_at),t.id)>($3,$4::uuid))
			ORDER BY COALESCE(t.send_reserved_at,t.completed_at,t.updated_at),t.id LIMIT $2`, before, limit-retired, after, afterTask)
		if err != nil {
			return retired, err
		}
		type candidate struct {
			task, mailbox uuid.UUID
			at            time.Time
		}
		var candidates []candidate
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.task, &c.mailbox, &c.at); err != nil {
				rows.Close()
				return retired, err
			}
			candidates = append(candidates, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return retired, err
		}
		if len(candidates) == 0 {
			break
		}
		for _, c := range candidates {
			ok, err := r.retireUnknownWarmupSend(ctx, c.task, c.mailbox, before)
			if err != nil {
				return retired, err
			}
			if ok {
				retired++
			}
		}
		// Protected historical payloads must not starve later recoverable mailboxes.
		last := candidates[len(candidates)-1]
		after, afterTask = &last.at, last.task
	}
	return retired, nil
}

func (r *taskRepository) retireUnknownWarmupSend(ctx context.Context, task, mailbox uuid.UUID, before time.Time) (bool, error) {
	tx, err := r.warmupSchedulingTx(ctx, task)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err = lockSend(ctx, tx, task, mailbox); err != nil {
		return false, err
	}
	var executorResult, dispatchResult []byte
	var alreadyRetired bool
	err = tx.QueryRow(ctx, `SELECT t.send_executor_result,w.dispatch_result,t.warmup_send_retired_at IS NOT NULL FROM tasks t
		JOIN warmup_tasks w ON w.task_id=t.id JOIN email_accounts ea ON ea.id=t.email_account_id
		WHERE t.id=$2 AND t.email_account_id=$3 AND ((`+retirableUnknownWarmupSend+`) OR (`+retiredWarmupHold+`))
		FOR UPDATE OF t,w,ea`, before, task, mailbox).Scan(&executorResult, &dispatchResult, &alreadyRetired)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if alreadyRetired {
		// Older replicas may reassert a historical unknown hold during a rolling upgrade.
		if err = refreshUnknownSendHold(ctx, tx, mailbox, task); err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	for _, raw := range [][]byte{executorResult, dispatchResult} {
		if len(raw) == 0 {
			continue
		}
		var result models.SendEmailResult
		if json.Unmarshal(raw, &result) != nil || result.TaskID != task || resultState(result) != "unknown" {
			return false, nil
		}
	}
	// Retirement fences replay, not provider outcome or accounting: retain every original send marker.
	if _, err = tx.Exec(ctx, `UPDATE tasks SET status='cancelled',warmup_send_retired_at=NOW(),updated_at=NOW() WHERE id=$1`, task); err != nil {
		return false, err
	}
	if err = resolveSendDeadLetter(ctx, tx, task); err != nil {
		return false, err
	}
	if err = refreshUnknownSendHold(ctx, tx, mailbox, task); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
