package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AppAttestKey is a native-app key that passed Apple's attestation.
type AppAttestKey struct {
	KeyID       string
	PublicKey   []byte
	AppID       string
	Environment string
	Counter     uint32
	Receipt     []byte
	CreatedAt   time.Time
	LastUsedAt  *time.Time
}

// AppAttestRepository stores attested App Attest keys and their counters.
type AppAttestRepository interface {
	// Insert stores a newly attested key and reports false when the id is taken.
	Insert(ctx context.Context, key AppAttestKey) (bool, error)
	// Get returns the key, or nil when it was never registered.
	Get(ctx context.Context, keyID string) (*AppAttestKey, error)
	// AdvanceCounter stores counter only when it is above the stored one, so two
	// requests carrying the same assertion cannot both pass.
	AdvanceCounter(ctx context.Context, keyID string, counter uint32) (bool, error)
}

type appAttestRepository struct {
	db *pgxpool.Pool
}

func NewAppAttestRepository(db *pgxpool.Pool) AppAttestRepository {
	return &appAttestRepository{db: db}
}

func (r *appAttestRepository) Insert(ctx context.Context, key AppAttestKey) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		INSERT INTO app_attest_keys (key_id, public_key, app_id, environment, counter, receipt)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (key_id) DO NOTHING`,
		key.KeyID, key.PublicKey, key.AppID, key.Environment, int64(key.Counter), key.Receipt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *appAttestRepository) Get(ctx context.Context, keyID string) (*AppAttestKey, error) {
	var key AppAttestKey
	var counter int64
	err := r.db.QueryRow(ctx, `
		SELECT key_id, public_key, app_id, environment, counter, receipt, created_at, last_used_at
		FROM app_attest_keys
		WHERE key_id = $1`, keyID).
		Scan(&key.KeyID, &key.PublicKey, &key.AppID, &key.Environment, &counter, &key.Receipt, &key.CreatedAt, &key.LastUsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	key.Counter = uint32(counter)
	return &key, nil
}

func (r *appAttestRepository) AdvanceCounter(ctx context.Context, keyID string, counter uint32) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE app_attest_keys
		SET counter = $2, last_used_at = now()
		WHERE key_id = $1 AND counter < $2`,
		keyID, int64(counter))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
