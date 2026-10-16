package emailsend

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

type fakeSuppression map[string]*models.SuppressedRecipient

func (f fakeSuppression) IsRecipientSuppressed(_ context.Context, _ uuid.UUID, email string) (*models.SuppressedRecipient, error) {
	if e, ok := f[email]; ok {
		return e, nil
	}
	domain := email[strings.LastIndex(email, "@")+1:]
	return f[domain], nil
}

func TestCheckSuppression(t *testing.T) {
	list := fakeSuppression{
		"partner.example":  {Email: "partner.example", Kind: models.SuppressionKindDomain, Reason: "client list"},
		"gone@example.com": {Email: "gone@example.com", Kind: models.SuppressionKindEmail},
	}
	svc := &emailSendService{suppression: list}
	org := uuid.New()

	cases := []struct {
		name string
		req  SendEmailRequest
		want string
	}{
		{"clean", SendEmailRequest{To: []string{"ana@example.com"}}, ""},
		{"suppressed domain in to", SendEmailRequest{To: []string{"Lea <Lea@Partner.example>"}}, "lea@partner.example is on this workspace's suppression list through its domain partner.example (client list)."},
		{"suppressed address in bcc", SendEmailRequest{To: []string{"ana@example.com"}, BCC: []string{"gone@example.com"}}, "gone@example.com is on this workspace's suppression list."},
		{"forward to a suppressed domain", SendEmailRequest{To: []string{"lea@partner.example"}, BCC: []string{"gone@example.com"}, Forward: &models.ForwardedMessage{}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			xerr := svc.checkSuppression(context.Background(), org, &tc.req)
			if tc.want == "" {
				if xerr != nil {
					t.Fatalf("refused: %v", xerr.Message)
				}
				return
			}
			if xerr == nil {
				t.Fatal("suppressed recipient was accepted")
			}
			if xerr.Code != errx.BadRequest || xerr.Identifier != ErrCodeRecipientSuppressed {
				t.Fatalf("got %d %q, want 400 %q", xerr.Code, xerr.Identifier, ErrCodeRecipientSuppressed)
			}
			if !strings.HasPrefix(xerr.Message, tc.want) {
				t.Fatalf("message %q does not start with %q", xerr.Message, tc.want)
			}
		})
	}
}
