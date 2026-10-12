package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/pkg/encrypt"
)

func TestLiveMailTransportSnapshotKeepsConfigurationAndGenerationTogether(t *testing.T) {
	f := newTransportFixture(t)
	enc, err := encrypt.NewEncrypter(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	config := f.pool.Config().Copy()
	config.MaxConns = 1
	config.MinConns = 0
	limited, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer limited.Close()
	repo := NewEmailRepostory(&db.DB{Pool: limited}, enc)
	if _, err := f.pool.Exec(t.Context(), `UPDATE email_accounts SET sync_skip_folders=ARRAY['INBOX'] WHERE id=$1`, f.account); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO email_accounts_oauth(email_account_id,access_token,refresh_token,expires_at) VALUES($1,'old-access','old-refresh',NOW()+INTERVAL '1 hour')`, f.account); err != nil {
		t.Fatal(err)
	}
	next := uuid.New()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := repo.(EmailTransportSnapshot).WithTransportSnapshot(ctx, func(snapshot EmailRepository) error {
		account, xerr := snapshot.GetByID(ctx, f.account)
		if xerr != nil {
			return xerr
		}
		tx, err := f.pool.Begin(t.Context())
		if err != nil {
			return err
		}
		defer tx.Rollback(context.Background())
		if _, err := tx.Exec(ctx, `UPDATE email_accounts SET transport_generation=$2,name='Fresh',sync_skip_folders=ARRAY['Later'] WHERE id=$1`, f.account, next); err != nil {
			return err
		}
		if _, err := tx.Exec(t.Context(), `UPDATE email_accounts_oauth SET access_token='fresh-access',refresh_token='fresh-refresh' WHERE email_account_id=$1`, f.account); err != nil {
			return err
		}
		if err := tx.Commit(t.Context()); err != nil {
			return err
		}
		credentials, xerr := snapshot.GetOAuthCredentials(ctx, f.account)
		if xerr != nil {
			return xerr
		}
		folders, xerr := snapshot.GetSyncSkipFolders(ctx, f.account)
		if xerr != nil {
			return xerr
		}
		if account.TransportGeneration != f.generation || credentials.AccessToken != "old-access" || credentials.RefreshToken != "old-refresh" || len(folders) != 1 || folders[0] != "INBOX" {
			t.Fatal("configuration crossed a generation boundary")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.(EmailTransportSnapshot).WithTransportSnapshot(ctx, func(snapshot EmailRepository) error {
		account, xerr := snapshot.GetByID(ctx, f.account)
		if xerr != nil {
			return xerr
		}
		credentials, xerr := snapshot.GetOAuthCredentials(ctx, f.account)
		if xerr != nil {
			return xerr
		}
		folders, xerr := snapshot.GetSyncSkipFolders(ctx, f.account)
		if xerr != nil {
			return xerr
		}
		if account.TransportGeneration != next || account.Name != "Fresh" || credentials.AccessToken != "fresh-access" || len(folders) != 1 || folders[0] != "Later" {
			t.Fatal("fresh snapshot did not load fresh configuration")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
