package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/models"
)

// ClayRepository holds the Clay integration's reads and its counters, which
// live on the connection row.
type ClayRepository interface {
	// IncrementStats adds one inbound request's counts to the connection.
	IncrementStats(ctx context.Context, connID uuid.UUID, delta models.ClayStats) error
	// OwnedCampaigns, OwnedCategories and OwnedSegments return which of the
	// ids belong to the organization.
	OwnedCampaigns(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error)
	OwnedCategories(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error)
	OwnedSegments(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error)
}

type clayRepository struct{ db *pgxpool.Pool }

// NewClayRepository builds the Clay repository.
func NewClayRepository(db *pgxpool.Pool) ClayRepository { return &clayRepository{db: db} }

// ClayStatsKey is the config_capabilities key a Clay connection counts under.
const ClayStatsKey = "clay_stats"

func (r *clayRepository) IncrementStats(ctx context.Context, connID uuid.UUID, d models.ClayStats) error {
	at := time.Now().UTC()
	if d.LastReceivedAt != nil {
		at = *d.LastReceivedAt
	}
	// One statement, so concurrent requests from Clay never lose a count.
	_, err := r.db.Exec(ctx, `
		UPDATE integration_connections SET config_capabilities =
			jsonb_set(COALESCE(config_capabilities, '{}'::jsonb), '{clay_stats}', (
				SELECT jsonb_build_object(
					'received', COALESCE((s->>'received')::int, 0) + $2,
					'created', COALESCE((s->>'created')::int, 0) + $3,
					'updated', COALESCE((s->>'updated')::int, 0) + $4,
					'skipped', COALESCE((s->>'skipped')::int, 0) + $5,
					'failed', COALESCE((s->>'failed')::int, 0) + $6,
					'last_received_at', to_jsonb($7::timestamptz),
					'last_error', CASE WHEN $8 = '' AND $6 > 0 THEN COALESCE(s->>'last_error', '') ELSE $8 END
				)
				FROM (SELECT COALESCE(config_capabilities->'clay_stats', '{}'::jsonb) AS s) x
			))
		WHERE id = $1 AND provider = 'clay'`,
		connID, d.Received, d.Created, d.Updated, d.Skipped, d.Failed, at, truncateRunes(d.LastError, 300))
	return err
}

func (r *clayRepository) owned(ctx context.Context, query string, orgID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, query, orgID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (r *clayRepository) OwnedCampaigns(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	return r.owned(ctx, `SELECT id FROM campaigns WHERE organization_id = $1 AND id = ANY($2)`, orgID, ids)
}

func (r *clayRepository) OwnedCategories(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	return r.owned(ctx, `SELECT id FROM categories WHERE organization_id = $1 AND id = ANY($2)`, orgID, ids)
}

func (r *clayRepository) OwnedSegments(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	return r.owned(ctx, `SELECT id FROM segments WHERE organization_id = $1 AND id = ANY($2)`, orgID, ids)
}
