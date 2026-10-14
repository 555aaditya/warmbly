package repository

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/models"
)

// partnerWeekTTL bounds how stale a candidate's seven-day counts may be. They
// weight the draw and size the inbound cap, and move by a handful of messages
// a minute across the whole pool; today's counts, which decide the cap, are
// always read live.
const partnerWeekTTL = time.Minute

// partnerWeek is what a mailbox sent, received and had junked over seven days.
type partnerWeek struct {
	sent, received, junked int
}

// partnerWeekCache holds the pool-wide seven-day counts for one process, so
// every sender shares one aggregate instead of rescanning the window per call.
type partnerWeekCache struct {
	ttl  time.Duration
	mu   sync.Mutex
	at   time.Time
	data map[uuid.UUID]partnerWeek
}

func newPartnerWeekCache(ttl time.Duration) *partnerWeekCache {
	return &partnerWeekCache{ttl: ttl}
}

const partnerWeekSQL = `
	WITH recv AS (
		SELECT wr.email_account_id AS id, COUNT(*) AS week, COUNT(sr.id) AS junk
		FROM warmup_received wr
		LEFT JOIN warmup_spam_reports sr
		  ON sr.reporter_account_id = wr.email_account_id AND sr.message_id = wr.message_id
		 AND sr.report_type = 'spam_placement' AND wr.message_id <> ''
		WHERE wr.created_at >= NOW() - interval '7 days' %[1]s
		GROUP BY 1
	),
	sent AS (
		SELECT wt.sender_account_id AS id, COUNT(*) AS week
		FROM warmup_tokens wt
		WHERE wt.created_at >= NOW() - interval '7 days' AND wt.sent_message_id <> '' %[2]s
		GROUP BY 1
	)
	SELECT COALESCE(recv.id, sent.id), COALESCE(sent.week, 0), COALESCE(recv.week, 0), COALESCE(recv.junk, 0)
	FROM recv FULL JOIN sent ON sent.id = recv.id`

var (
	partnerWeekAllSQL = fmt.Sprintf(partnerWeekSQL, "", "")
	partnerWeekIDsSQL = fmt.Sprintf(partnerWeekSQL,
		"AND wr.email_account_id = ANY($1::uuid[])", "AND wt.sender_account_id = ANY($1::uuid[])")
)

// partnerWeeks returns the seven-day counts for ids: from the shared cache
// when the repository has one, otherwise read exactly for those ids.
func (r *warmupRepository) partnerWeeks(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]partnerWeek, error) {
	if c := r.partnerWeeks7d; c != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.data != nil && time.Since(c.at) < c.ttl {
			return c.data, nil
		}
		// The pool, not a caller's transaction: the snapshot is shared.
		data, err := scanPartnerWeeks(ctx, r.db, partnerWeekAllSQL)
		if err != nil {
			return nil, err
		}
		c.data, c.at = data, time.Now()
		return data, nil
	}
	return scanPartnerWeeks(ctx, resultDB(ctx, r.db), partnerWeekIDsSQL, ids)
}

func scanPartnerWeeks(ctx context.Context, db sendResultDB, query string, args ...any) (map[uuid.UUID]partnerWeek, error) {
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[uuid.UUID]partnerWeek)
	for rows.Next() {
		var id uuid.UUID
		var w partnerWeek
		if err := rows.Scan(&id, &w.sent, &w.received, &w.junked); err != nil {
			return nil, err
		}
		out[id] = w
	}
	return out, rows.Err()
}

// partnerToday is what a mailbox received and was dispatched today, the two
// numbers its inbound cap is held against.
type partnerToday struct {
	received, inflight int
}

// partnerTodays reads today's arrivals and dispatches live. A reserved send
// being revalidated is left out of its own recipient's count.
func (r *warmupRepository) partnerTodays(ctx context.Context, ids []uuid.UUID, excludeTask uuid.UUID) (map[uuid.UUID]partnerToday, error) {
	rows, err := resultDB(ctx, r.db).Query(ctx, `
		SELECT wr.email_account_id, COUNT(*), 0
		FROM warmup_received wr
		WHERE wr.created_at >= date_trunc('day', NOW()) AND wr.email_account_id = ANY($1::uuid[])
		GROUP BY 1
		UNION ALL
		SELECT wt.recipient_account_id, 0, COUNT(*)
		FROM warmup_tokens wt
		WHERE wt.created_at >= date_trunc('day', NOW()) AND wt.recipient_account_id = ANY($1::uuid[])
		  AND wt.task_id <> $2
		GROUP BY 1`, ids, excludeTask)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[uuid.UUID]partnerToday, len(ids))
	for rows.Next() {
		var id uuid.UUID
		var received, inflight int
		if err := rows.Scan(&id, &received, &inflight); err != nil {
			return nil, err
		}
		t := out[id]
		t.received += received
		t.inflight += inflight
		out[id] = t
	}
	return out, rows.Err()
}

// partnerInboundCap is how much one inbox may receive from the pool in a day,
// scaled to the share the sender's tier may fill. send_admission.go holds the
// same rule in SQL.
func partnerInboundCap(sentWeek, sharePercent int) int {
	daily := ((sentWeek + 6) / 7) * config.WarmupInboundDailyMultiple
	daily = min(max(daily, config.WarmupInboundDailyFloor), config.WarmupInboundDailyCeiling)
	return daily * sharePercent / 100
}

// inboundSharePercent: a paying sender may fill all of an inbox's daily cap; a
// free sender stops short, so the rest of every inbox's day stays open to the
// premium tier.
func inboundSharePercent(poolType string) int {
	if poolType == string(models.WarmupPoolPremium) {
		return 100
	}
	return config.WarmupFreeInboundSharePercent
}

// capPartnerCandidates attaches each candidate's seven-day counts and drops
// every one already at its inbound cap for today, before any count or sample
// is taken from the set, so a thin tier is sized on who can still receive.
func (r *warmupRepository) capPartnerCandidates(ctx context.Context, poolType string, cands []models.WarmupPartnerCandidate) ([]models.WarmupPartnerCandidate, error) {
	if len(cands) == 0 {
		return cands, nil
	}
	ids := make([]uuid.UUID, len(cands))
	for i, c := range cands {
		ids[i] = c.ID
	}
	weeks, err := r.partnerWeeks(ctx, ids)
	if err != nil {
		return nil, err
	}
	excludeTask, _ := ctx.Value(warmupExecutionExclusion{}).(uuid.UUID)
	todays, err := r.partnerTodays(ctx, ids, excludeTask)
	if err != nil {
		return nil, err
	}
	share := inboundSharePercent(poolType)
	out := cands[:0]
	for _, c := range cands {
		w, t := weeks[c.ID], todays[c.ID]
		if max(t.received, t.inflight) >= partnerInboundCap(w.sent, share) {
			continue
		}
		c.Sent7d, c.Received7d, c.Junked7d = w.sent, w.received, w.junked
		out = append(out, c)
	}
	return out, nil
}
