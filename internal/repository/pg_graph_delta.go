package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
)

// EmailGraphDeltaRepository persists the per-mailbox, per-folder Microsoft Graph
// delta cursor. Like EmailHistoryIDRepository (the Gmail equivalent) it lives in
// the consumer's Postgres; the disposable worker relays cursors over Kafka and
// never touches the DB. Get returns the folder->deltaLink map used to seed the
// worker when the mailbox is (re)assigned.
type EmailGraphDeltaRepository interface {
	Put(ctx context.Context, userID, emailID uuid.UUID, folder, deltaLink string) error
	Get(ctx context.Context, userID, emailID uuid.UUID) (map[string]string, error)
}

type pgEmailGraphDeltaRepository struct {
	db *db.DB
}

func NewEmailGraphDeltaRepository(d *db.DB) EmailGraphDeltaRepository {
	return &pgEmailGraphDeltaRepository{db: d}
}

func (r *pgEmailGraphDeltaRepository) Put(ctx context.Context, userID, emailID uuid.UUID, folder, deltaLink string) error {
	const q = `
		INSERT INTO email_delta_links (user_id, email_id, folder, delta_link, last_updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (user_id, email_id, folder)
		DO UPDATE SET delta_link = EXCLUDED.delta_link, last_updated_at = now()
		WHERE email_delta_links.delta_link IS DISTINCT FROM EXCLUDED.delta_link
	`
	if _, err := r.db.Exec(ctx, q, userID, emailID, folder, deltaLink); err != nil {
		return fmt.Errorf("email_delta_links: put: %w", err)
	}
	return nil
}

func (r *pgEmailGraphDeltaRepository) Get(ctx context.Context, userID, emailID uuid.UUID) (map[string]string, error) {
	const q = `
		SELECT folder, delta_link
		FROM email_delta_links
		WHERE user_id = $1 AND email_id = $2
	`
	rows, err := r.db.Query(ctx, q, userID, emailID)
	if err != nil {
		return nil, fmt.Errorf("email_delta_links: get: %w", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var folder, deltaLink string
		if err := rows.Scan(&folder, &deltaLink); err != nil {
			return nil, fmt.Errorf("email_delta_links: scan: %w", err)
		}
		out[folder] = deltaLink
	}
	return out, rows.Err()
}
