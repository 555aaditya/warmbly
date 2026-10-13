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
			})
		}
	}
}
