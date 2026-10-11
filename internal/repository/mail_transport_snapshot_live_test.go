package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/pkg/encrypt"
)

func TestLiveMailTransportSnapshotKeepsConfigurationAndGenerationTogether(t *testing.T) {
	f := newTransportFixture(t)
	enc, err := encrypt.NewEncrypter(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo := NewEmailRepostory(f.db, enc)
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO email_accounts_oauth(email_account_id,access_token,refresh_token,expires_at) VALUES($1,'old-access','old-refresh',NOW()+INTERVAL '1 hour')`, f.account); err != nil {
		t.Fatal(err)
	}
	next := uuid.New()
	if err := repo.(EmailTransportSnapshot).WithTransportSnapshot(t.Context(), func(snapshot EmailRepository) error {
		account, xerr := snapshot.GetByID(t.Context(), f.account)
		if xerr != nil {
			return xerr
		}
		tx, err := f.pool.Begin(t.Context())
		if err != nil {
			return err
		}
		defer tx.Rollback(context.Background())
		if _, err := tx.Exec(t.Context(), `UPDATE email_accounts SET transport_generation=$2,name='Fresh' WHERE id=$1`, f.account, next); err != nil {
			return err
		}
		if _, err := tx.Exec(t.Context(), `UPDATE email_accounts_oauth SET access_token='fresh-access',refresh_token='fresh-refresh' WHERE email_account_id=$1`, f.account); err != nil {
			return err
		}
		if err := tx.Commit(t.Context()); err != nil {
			return err
		}
		credentials, xerr := snapshot.GetOAuthCredentials(t.Context(), f.account)
		if xerr != nil {
			return xerr
		}
		if account.TransportGeneration != f.generation || credentials.AccessToken != "old-access" || credentials.RefreshToken != "old-refresh" {
			t.Fatal("configuration crossed a generation boundary")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.(EmailTransportSnapshot).WithTransportSnapshot(t.Context(), func(snapshot EmailRepository) error {
		account, xerr := snapshot.GetByID(t.Context(), f.account)
		if xerr != nil {
			return xerr
		}
		credentials, xerr := snapshot.GetOAuthCredentials(t.Context(), f.account)
		if xerr != nil {
			return xerr
		}
		if account.TransportGeneration != next || account.Name != "Fresh" || credentials.AccessToken != "fresh-access" {
			t.Fatal("fresh snapshot did not load fresh configuration")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
