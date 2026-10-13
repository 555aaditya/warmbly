package repository

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/encrypt"
)

func TestLiveMailboxBehaviorCreationRespectsGatewayWarmup(t *testing.T) {
	f := newTransportFixture(t)
	enc, err := encrypt.NewEncrypter(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo := NewEmailRepostory(f.db, enc)
	account, xerr := repo.GetByID(t.Context(), f.account)
	if xerr != nil {
		t.Fatal(xerr)
	}
	for _, provider := range []models.InboxProvider{models.InboxProviderSMTPIMAP, models.InboxProviderGoogle, models.InboxProviderOutlook} {
		for _, linked := range []bool{false, true} {
			t.Run(string(provider)+"/linked="+strconv.FormatBool(linked), func(t *testing.T) {
				var a *models.Email
				if provider == models.InboxProviderSMTPIMAP {
					server := &models.Service{Host: "mail.example.test", Port: 993, Security: models.MailSecurityTLS, Username: "test", Password: "synthetic"}
					a, xerr = repo.NewSMTPIMAPAccount(t.Context(), f.user.String(), models.NewSMTPIMAPAccount{OrganizationID: account.OrganizationID, Name: "Linked", Email: uuid.NewString() + "@test.local", SMTP: server, IMAP: server, DisableSendingBehavior: linked})
				} else {
					a, xerr = repo.NewOauthAccount(t.Context(), f.user.String(), models.NewOauthAccount{OrganizationID: account.OrganizationID, Provider: provider, Name: "Linked", Email: uuid.NewString() + "@test.local", AccessToken: "synthetic", RefreshToken: "synthetic", ExpiresAt: time.Now().Add(time.Hour), DisableSendingBehavior: linked})
				}
				if xerr != nil {
					t.Fatal(xerr)
				}
				t.Cleanup(func() {
					if _, err := f.pool.Exec(context.Background(), `DELETE FROM email_accounts WHERE id=$1`, a.ID); err != nil {
						t.Error(err)
					}
				})
				profile, err := NewBehaviorRepository(f.pool).GetBehavior(t.Context(), a.ID)
				if err != nil || profile == nil || profile.Enabled == linked {
					t.Fatalf("profile = %+v, err = %v, linked = %v", profile, err, linked)
				}
				var createdWithMailbox bool
				if err := f.pool.QueryRow(t.Context(), `SELECT b.created_at <= ea.created_at FROM email_account_behavior b JOIN email_accounts ea ON ea.id=b.email_account_id WHERE ea.id=$1`, a.ID).Scan(&createdWithMailbox); err != nil || !createdWithMailbox {
					t.Fatalf("creation-time profile cannot be distinguished from a customer save: %v", err)
				}
			})
		}
	}
}

func TestLiveMailboxBehaviorFirstCustomerWriteIsNotInherited(t *testing.T) {
	f := newTransportFixture(t)
	repo := NewBehaviorRepository(f.pool)
	if err := repo.DeleteBehavior(t.Context(), f.account); err != nil {
		t.Fatal(err)
	}
	profile := models.DefaultSendingBehavior(f.account)
	profile.Enabled = true
	first, err := repo.UpsertBehavior(t.Context(), &profile)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Enabled || !first.UpdatedAt.After(first.CreatedAt) {
		t.Fatal("explicit first save looks like an inherited default")
	}
	profile.Enabled = false
	next, err := repo.UpsertBehavior(t.Context(), &profile)
	if err != nil {
		t.Fatal(err)
	}
	if next.Enabled || !next.CreatedAt.Equal(first.CreatedAt) || !next.UpdatedAt.After(next.CreatedAt) {
		t.Fatal("explicit update lost its customer-configured provenance")
	}
}
