package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
)

func TestLiveEmailErrorResolveMethod(t *testing.T) {
	handle, pool := liveContactDB(t)
	repo := NewEmailAccountErrorRepository(handle)
	ctx := context.Background()
	account, user, org := uuid.New(), uuid.New(), uuid.New()
	tag := "resolve-" + org.String()[:8]
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		exec(`DELETE FROM email_account_errors WHERE email_account_id=$1`, account)
		exec(`DELETE FROM email_accounts WHERE id=$1`, account)
		exec(`DELETE FROM organizations WHERE id=$1`, org)
		exec(`DELETE FROM users WHERE id=$1`, user)
	})
	exec(`INSERT INTO users (id, first_name, last_name, email, password_hash)
		VALUES ($1, 'Resolve', 'Test', $2, 'x')`, user, tag+"@test.local")
	exec(`INSERT INTO organizations (id, name, slug, owner_user_id)
		VALUES ($1, 'Resolve method test', $2, $3)`, org, tag, user)
	exec(`INSERT INTO email_accounts (id, user_id, organization_id, email, name, signature_plain, signature_html, provider)
		VALUES ($1, $2, $3, $4, 'Resolve', '', '', 'smtp_imap')`, account, user, org, tag+"-mail@test.local")

	for _, tt := range []struct {
		name   string
		method string
		want   string
	}{
		{"no action", string(errx.MailErrorResolveMethodNone), "NONE"},
		{"explicit none", "NONE", "NONE"},
		{"authentication", string(errx.MailErrorResolveMethodAuth), "OAUTH"},
		{"retry", string(errx.MailErrorResolveMethodRetry), "RETRY"},
		{"reload", string(errx.MailErrorResolveMethodReload), "RELOAD"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := &CreateEmailAccountError{
				EmailAccountID: account,
				UserID:         user,
				ErrorCode:      string(errx.MailErrorCodeRateLimitExceeded),
				Severity:       "WARNING",
				ResolveMethod:  tt.method,
				Title:          "Email Error",
				Message:        "Rate limit exceeded",
			}
			saved, xerr := repo.CreateOnce(ctx, data)
			if xerr != nil || saved == nil {
				t.Fatalf("CreateOnce = %+v, %v", saved, xerr)
			}
			if saved.ResolveMethod != tt.want {
				t.Fatalf("ResolveMethod = %q, want %q", saved.ResolveMethod, tt.want)
			}
			if data.ResolveMethod != tt.method {
				t.Fatal("CreateOnce mutated the input resolve method")
			}
			var stored string
			if err := pool.QueryRow(ctx, `SELECT resolve_method FROM email_account_errors WHERE id=$1`, saved.ID).Scan(&stored); err != nil || stored != tt.want {
				t.Fatalf("stored ResolveMethod = %q, want %q: %v", stored, tt.want, err)
			}
			again, xerr := repo.CreateOnce(ctx, data)
			if xerr != nil || again != nil {
				t.Fatalf("duplicate CreateOnce = %+v, %v", again, xerr)
			}
			if xerr := repo.Resolve(ctx, saved.ID, "test"); xerr != nil {
				t.Fatal(xerr)
			}
		})
	}
}
