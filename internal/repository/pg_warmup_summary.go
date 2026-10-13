package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type WarmupSummary struct {
	Sent, Replied, SpamPlaced int
}

func (r *warmupRepository) GetWarmupSummaries(ctx context.Context, accountIDs []uuid.UUID, from, to time.Time) (map[uuid.UUID]WarmupSummary, error) {
	out := make(map[uuid.UUID]WarmupSummary, len(accountIDs))
	if len(accountIDs) == 0 {
		return out, nil
	}
	rows, err := resultDB(ctx, r.db).Query(ctx, `
		WITH stats AS (
			SELECT email_account_id, SUM(emails_sent) AS sent,
			       SUM(emails_replied) FILTER (WHERE date <= DATE($3)) AS replied
			FROM warmup_statistics
			WHERE email_account_id = ANY($1::uuid[]) AND date >= DATE($2)
			GROUP BY email_account_id
		), spam AS (
			SELECT reported_account_id, COUNT(*) AS placed
			FROM warmup_spam_reports
			WHERE reported_account_id = ANY($1::uuid[])
			  AND created_at >= $2 AND report_type = 'spam_placement'
			GROUP BY reported_account_id
		)
		SELECT a.id, COALESCE(s.sent, 0), COALESCE(s.replied, 0), COALESCE(p.placed, 0)
		FROM unnest($1::uuid[]) a(id)
		LEFT JOIN stats s ON s.email_account_id = a.id
		LEFT JOIN spam p ON p.reported_account_id = a.id`, accountIDs, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var summary WarmupSummary
		if err := rows.Scan(&id, &summary.Sent, &summary.Replied, &summary.SpamPlaced); err != nil {
			return nil, err
		}
		out[id] = summary
	}
	return out, rows.Err()
}
