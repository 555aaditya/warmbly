package imap

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/pkg/dsn"
)

// globalBounce is the RFC 6533 report Postfix writes for SMTPUTF8 mail.
const globalBounce = "From: MAILER-DAEMON@mx.example.com (Mail Delivery System)\r\n" +
	"To: sender@test\r\n" +
	"Subject: Undelivered Mail Returned to Sender\r\n" +
	"Message-ID: <dsn-1@mx.example.com>\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/report; report-type=delivery-status; boundary=\"B\"\r\n" +
	"\r\n" +
	"--B\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"I'm sorry to have to inform you that your message could not be delivered.\r\n" +
	"--B\r\n" +
	"Content-Type: message/global-delivery-status\r\n" +
	"\r\n" +
	"Reporting-MTA: dns; mx.example.com\r\n" +
	"\r\n" +
	"Final-Recipient: utf-8; nobody@example.org\r\n" +
	"Action: failed\r\n" +
	"Status: 5.1.1\r\n" +
	"Diagnostic-Code: smtp; 550 5.1.1 user unknown\r\n" +
	"--B\r\n" +
	"Content-Type: message/global\r\n" +
	"\r\n" +
	"Message-ID: <warmup-original@test>\r\n" +
	"Subject: Quick question\r\n" +
	"\r\n" +
	"Hi there\r\n" +
	"--B--\r\n"

// A server that sends message/global as a leaf with its extension data, by the
// RFC 3501 grammar, which go-imap cannot read: it expects an envelope after
// the size, as for rfc822.
const structureGlobalLeaf = `* 1 FETCH (UID 1 BODYSTRUCTURE (("text" "plain" ("charset" "utf-8") NIL NIL "8bit" 80 1 NIL NIL NIL NIL)` +
	`("message" "global-delivery-status" NIL NIL NIL "7bit" 160 NIL NIL NIL NIL)("message" "global" NIL NIL NIL "8bit" 70 NIL NIL NIL NIL) "report" ("report-type" "delivery-status" "boundary" "B") NIL NIL NIL))` +
	"\r\n$TAG OK Fetch completed"

// An envelope answer that cannot be read in any form.
const envelopeUnreadable = "* 1 FETCH (UID 1 MODSEQ 21)\r\n$TAG OK Fetch completed"

func appendRaw(t *testing.T, c *Client, raw string) imap.UID {
	t.Helper()
	cmd := c.client.Append("INBOX", int64(len(raw)), nil)
	if _, err := cmd.Write([]byte(raw)); err != nil {
		t.Fatalf("append write: %v", err)
	}
	if err := cmd.Close(); err != nil {
		t.Fatalf("append close: %v", err)
	}
	data, err := cmd.Wait()
	if err != nil {
		t.Fatalf("append wait: %v", err)
	}
	return data.UID
}

// answering stands in for the server's reply to every FETCH that asks for the
// items in need.
func answering(need, reply string) func(line string) string {
	return func(line string) string {
		upper := strings.ToUpper(line)
		if !strings.Contains(upper, "FETCH") || !strings.Contains(upper, need) {
			return ""
		}
		return strings.ReplaceAll(reply, "$TAG", strings.Fields(line)[0])
	}
}

// fetchPass is one sync step as the worker drives it.
func fetchPass(t *testing.T, c *Client, uid imap.UID) ([]*Fetched, *errx.MailError) {
	t.Helper()
	if err := c.ensureConnected(); err != nil {
		return nil, err
	}
	if _, err := c.SelectForSync("INBOX"); err != nil {
		t.Fatalf("SelectForSync: %v", err)
	}
	return c.FetchEnvelopes(context.Background(), []imap.UID{uid})
}

// One failed pass, then the bounce syncs without its structure and its report
// parts reach the DSN parser, so it is read as the hard bounce it is.
func TestFetchEnvelopesStepsDownPastAnUnreadableStructure(t *testing.T) {
	c, wire := interceptingServer(t, nil, answering("BODYSTRUCTURE", structureGlobalLeaf))
	c.IdleTimeout = 5 * time.Second
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	uid := appendRaw(t, c, globalBounce)

	if _, err := fetchPass(t, c, uid); err == nil || err.Code != errx.MailErrorCodeServerUnreachable {
		t.Fatalf("first pass = %v, want the session dropped", err)
	}
	got, err := fetchPass(t, c, uid)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if len(got) != 1 || !got[0].bare || got[0].Email.MessageID != "dsn-1@mx.example.com" {
		t.Fatalf("second pass fetched %+v, want the bounce without its structure", got)
	}
	if fetches := wire.commands("FETCH"); !strings.Contains(fetches[len(fetches)-1], "ENVELOPE") ||
		strings.Contains(fetches[len(fetches)-1], "BODYSTRUCTURE") {
		t.Fatalf("second pass asked %q", fetches[len(fetches)-1])
	}
	if err := c.FetchBody(got[0]); err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	report := dsn.Parse(got[0].Email.BodyPlain)
	if !report.Permanent || report.FailedRecipient != "nobody@example.org" || report.OriginalMessageID != "warmup-original@test" {
		t.Fatalf("report = %+v from body %q", report, got[0].Email.BodyPlain)
	}
}

// A message nothing can read steps down to one at a time and then out of the
// sync, so it costs a few passes and never the folder.
func TestFetchEnvelopesLeavesAnUnreadableMessageBehind(t *testing.T) {
	c, _ := interceptingServer(t, nil, answering("ENVELOPE", envelopeUnreadable))
	c.IdleTimeout = 5 * time.Second
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	uid := appendRaw(t, c, globalBounce)

	for _, want := range []envelopeRung{envelopeBare, envelopeAlone, envelopeSkip} {
		if _, err := fetchPass(t, c, uid); err == nil {
			t.Fatalf("pass before %v succeeded", want)
		}
		if got := c.envelopes.get(c.envelopeView(), uid); got != want {
			t.Fatalf("rung = %v, want %v", got, want)
		}
	}
	got, err := fetchPass(t, c, uid)
	if err != nil || len(got) != 0 {
		t.Fatalf("last pass = %v, %v; want the message left behind", got, err)
	}
}

// The local reader picks the same parts the structure-driven one does.
func TestMIMETextPartsFollowsTheSelectionRules(t *testing.T) {
	raw := "Content-Type: multipart/mixed; boundary=M\r\n\r\n" +
		"--M\r\nContent-Type: multipart/alternative; boundary=A\r\n\r\n" +
		"--A\r\nContent-Type: text/plain; charset=iso-8859-1\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\ncaf=E9\r\n" +
		"--A\r\nContent-Type: text/plain\r\n\r\nsecond alternative\r\n" +
		"--A\r\nContent-Type: text/html\r\n\r\n<p>caf&eacute;</p>\r\n" +
		"--A--\r\n" +
		"--M\r\nContent-Type: text/plain\r\nContent-Disposition: attachment; filename=a.txt\r\n\r\nattached\r\n" +
		"--M\r\nContent-Type: text/plain\r\n\r\nfooter\r\n" +
		"--M--\r\n"
	plain, html := mimeTextParts([]byte(raw))
	if plain != "café\n\nfooter" {
		t.Fatalf("plain = %q, want the first alternative and the footer, decoded", plain)
	}
	if html != "<p>caf&eacute;</p>" {
		t.Fatalf("html = %q", html)
	}
}
