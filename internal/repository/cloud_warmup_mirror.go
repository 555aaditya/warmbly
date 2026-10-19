package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/models"
)

// CloudWarmupMirrorRepository keeps an enrolled mailbox's warmup columns equal to what Warmbly Cloud reports.
type CloudWarmupMirrorRepository interface {
	// MirrorCloudWarmupSettings writes settings observed at observedAt and returns the mailboxes whose columns changed.
	MirrorCloudWarmupSettings(ctx context.Context, observedAt time.Time, settings map[uuid.UUID]models.PoolLinkWarmupSettings) ([]uuid.UUID, error)
}

var _ CloudWarmupMirrorRepository = (*cloudLinkRepository)(nil)

func mirrorable(w models.PoolLinkWarmupSettings) bool {
	for _, v := range []int{w.Base, w.Max, w.Increase, w.ReplyRate} {
		if v < 0 || v > 100 {
			return false
		}
	}
	return w.Days >= 0 && w.Days <= models.WarmupEveryDay && w.StartTime != "" && w.EndTime != ""
}

func (r *cloudLinkRepository) MirrorCloudWarmupSettings(ctx context.Context, observedAt time.Time, settings map[uuid.UUID]models.PoolLinkWarmupSettings) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	var bases, maxes, increases, replies, days []int32
	var starts, ends []string
	for id, w := range settings {
		w = w.Mirrored()
		if !mirrorable(w) {
			log.Warn().Str("account_id", id.String()).Msg("cloud link: reported warmup settings are out of range; local copy left as it is")
			continue
		}
		ids = append(ids, id)
		bases, maxes, increases, replies = append(bases, int32(w.Base)), append(maxes, int32(w.Max)), append(increases, int32(w.Increase)), append(replies, int32(w.ReplyRate))
		starts, ends, days = append(starts, w.StartTime), append(ends, w.EndTime), append(days, int32(w.Days))
	}
	if len(ids) == 0 {
		return nil, nil
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// Stamping first locks each observation row, so an older report racing a newer one is re-checked and dropped.
	stamp := `UPDATE cloud_link_mailboxes SET settings_observed_at = $1
		 WHERE email_account_id = ANY($2::uuid[]) AND enrollment_state <> 'pending_remove'
		   AND (settings_observed_at IS NULL OR settings_observed_at < $1)
		RETURNING email_account_id`
	accepted, err := scanIDs(tx.Query(ctx, stamp, observedAt, ids))
	if err != nil {
		db.CaptureError(err, stamp, nil, "query")
		return nil, err
	}
	if len(accepted) == 0 {
		return nil, tx.Commit(ctx)
	}
	query := `
		UPDATE email_accounts ea
		   SET warmup_base = s.base, warmup_max = s.max, warmup_increase = s.inc, warmup_reply_rate = s.reply,
		       warmup_start_time = s.start_at::time, warmup_end_time = s.end_at::time, warmup_days = s.days, updated_at = now()
		  FROM unnest($1::uuid[], $2::int[], $3::int[], $4::int[], $5::smallint[], $6::text[], $7::text[], $8::smallint[])
		       AS s(id, base, max, inc, reply, start_at, end_at, days)
		 WHERE ea.id = s.id AND s.id = ANY($9::uuid[])
		   AND (ea.warmup_base, ea.warmup_max, ea.warmup_increase, ea.warmup_reply_rate, ea.warmup_start_time, ea.warmup_end_time, ea.warmup_days)
		       IS DISTINCT FROM (s.base, s.max, s.inc, s.reply, s.start_at::time, s.end_at::time, s.days)
		RETURNING ea.id`
	changed, err := scanIDs(tx.Query(ctx, query, ids, bases, maxes, increases, replies, starts, ends, days, accepted))
	if err != nil {
		db.CaptureError(err, query, nil, "query")
		return nil, err
	}
	return changed, tx.Commit(ctx)
}

func scanIDs(rows pgx.Rows, err error) ([]uuid.UUID, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
