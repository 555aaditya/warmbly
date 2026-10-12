package email

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/pkg/encrypt"
	"github.com/warmbly/warmbly/internal/repository"
)

func TestLiveWorkerMailboxLoadReadsOneGenerationWithSingleConnection(t *testing.T) {
	f := newRemovalLiveFixture(t)
	if _, err := f.pool.Exec(t.Context(), `UPDATE email_accounts SET provider='gmail',sync_skip_folders=ARRAY['Old'] WHERE id=$1`, f.mailbox); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO email_accounts_oauth(email_account_id,access_token,refresh_token,expires_at) VALUES($1,'old-access','old-refresh',NOW()+INTERVAL '1 hour')`, f.mailbox); err != nil {
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
	enc, err := encrypt.NewEncrypter(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	handle := &db.DB{Pool: limited}
	repo := repository.NewEmailRepostory(handle, enc)
	s := &emailService{
		emailRepository: repo,
		syncState:       repository.NewEmailSyncStateRepository(handle),
		historyID:       repository.NewEmailHistoryIDRepository(handle),
	}
	before, xerr := repo.GetByID(t.Context(), f.mailbox)
	if xerr != nil {
		t.Fatal(xerr)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	loaded, err := s.buildAddWorkerEmail(ctx, before)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TransportGeneration != before.TransportGeneration || loaded.Google == nil || loaded.Google.Token.AccessToken != "old-access" || len(loaded.Sync.Policy.SkipFolders) != 1 || loaded.Sync.Policy.SkipFolders[0] != "Old" {
		t.Fatal("single-connection load lost its credential or policy snapshot")
	}
	next := uuid.New()
	if _, err := f.pool.Exec(ctx, `UPDATE email_accounts SET transport_generation=$2,sync_skip_folders=ARRAY['Fresh'] WHERE id=$1`, f.mailbox, next); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE email_accounts_oauth SET access_token='fresh-access' WHERE email_account_id=$1`, f.mailbox); err != nil {
		t.Fatal(err)
	}
	if loaded, err := s.buildAddWorkerEmail(ctx, before); err == nil || loaded != nil {
		t.Fatal("outdated preload crossed the restore fence")
	}
	after, xerr := repo.GetByID(ctx, f.mailbox)
	if xerr != nil {
		t.Fatal(xerr)
	}
	loaded, err = s.buildAddWorkerEmail(ctx, after)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TransportGeneration != next || loaded.Google.Token.AccessToken != "fresh-access" || len(loaded.Sync.Policy.SkipFolders) != 1 || loaded.Sync.Policy.SkipFolders[0] != "Fresh" {
		t.Fatal("fresh load mixed old and new configuration")
	}
}
