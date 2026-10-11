package mailmanager

import (
	"net"
	"testing"
	"time"

	goimap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

func TestRecoveryProbeFailedReplacementRetainsStoppedButReofferRecovers(t *testing.T) {
	t.Setenv("DEPLOYMENT_MODE", "self_hosted")
	events := 0
	m := NewMailManager(func(models.JobEventType, string, any) error { events++; return nil }, nil, nil, nil, nil, nil, nil)
	d := smtpPayload()
	stamp := time.Now().Add(-time.Hour)
	d.Sync.State.LastSyncedAt = &stamp
	if err := m.AddWMail(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	old := m.Get(d.ID)
	t.Cleanup(func() { m.Terminate(d.ID) })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr, port := ln.Addr().String(), ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	retry := *d
	s := *d.SmtpImap
	creds := *s.Credentials
	creds.IMAP = &models.Service{Host: "127.0.0.1", Port: port, Username: "local", Password: "local", Security: models.MailSecurityNone}
	s.Credentials = &creds
	retry.SmtpImap, retry.ImapSync = &s, true
	if err := m.AddWMail(t.Context(), &retry); err != errx.ErrMailServerUnreachable {
		t.Fatalf("initial unavailable provider: %v", err)
	}
	if m.Get(d.ID) != old || !m.Has(d.ID) || old.Ctx.Err() == nil {
		t.Fatal("failed replacement did not retain stopped pointer")
	}
	if _, ok := old.BeginExecution(); ok {
		t.Fatal("stopped pointer admitted work")
	}
	if old.SameExecution(d) || old.SameExecution(&retry) {
		t.Fatal("stopped pointer bypasses reload on republish")
	}
	old.CaptureError(errx.ErrMailServerUnreachable)
	if events != 0 {
		t.Fatal("retired pointer emitted a fresh transport report")
	}
	mem := imapmemserver.New()
	u := imapmemserver.NewUser("local", "local")
	if err := u.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	mem.AddUser(u)
	ln, err = net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := imapserver.New(&imapserver.Options{Caps: goimap.CapSet{goimap.CapIMAP4rev1: {}}, InsecureAuth: true, NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		return mem.NewSession(), nil, nil
	}})
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	if err := m.AddWMail(t.Context(), &retry); err != nil {
		t.Fatal(err)
	}
	next := m.Get(d.ID)
	if next == nil || next == old || next.Ctx.Err() != nil || !next.SameExecution(&retry) {
		t.Fatal("same reoffered credentials did not recover")
	}
	resumed := retry
	next.ResumeInto(&resumed)
	if resumed.Sync.State.LastSyncedAt == nil || !resumed.Sync.State.LastSyncedAt.Equal(stamp) || resumed.Sync.State.BackfillSynced != 7 {
		t.Fatal("retry lost previous sync evidence")
	}
}
