package worker

import (
	"net"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

func TestRecoveryProbeInitialDialFailureHasReportButNoState(t *testing.T) {
	t.Setenv("DEPLOYMENT_MODE", "self_hosted")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	c := &capturedEvents{}
	w := newLoadingWorker(c)
	e := &models.AddWorkerEmail{ID: uuid.New(), UserID: uuid.New(), Type: models.InboxProviderSMTPIMAP, ImapSync: true, SmtpImap: &models.AddWorkerEmailSmtpImapData{Credentials: &models.SmtpImap{IMAP: &models.Service{Host: "127.0.0.1", Port: port, Security: models.MailSecurityNone}}}}
	w.loadMailbox(t.Context(), e, &mailboxLoad{})
	if w.mailManager.Get(e.ID) != nil {
		t.Fatal("failed initial dial installed mailbox")
	}
	if len(c.events) != 1 || c.events[0] != models.JobEventTypeEmailServerError || c.bodies[0].(models.EmailErrorEvent).ErrorCode != string(errx.MailErrorCodeServerUnreachable) {
		t.Fatal("initial dial failure did not produce only a transport report")
	}
}
