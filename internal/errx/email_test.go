package errx

import (
	"errors"
	"testing"
)

func TestKnownIMAPConnectionMessageRequiresExactEnvelope(t *testing.T) {
	for _, stage := range []string{MailConnectionStageIMAPDial, MailConnectionStageIMAPTLS, MailConnectionStageIMAPSTARTTLS} {
		for _, reason := range []string{MailConnectionReasonDNSFailure, MailConnectionReasonDNSTimeout, MailConnectionReasonCertificate,
			MailConnectionReasonWorkerAddress, MailConnectionReasonRefused, MailConnectionReasonRoute, MailConnectionReasonClosed,
			MailConnectionReasonTimeout, MailConnectionReasonFailed} {
			e := ErrMailServerUnreachableAt(stage, errors.New(reason))
			if !IsKnownIMAPConnectionMessage(e.Message) || e.Code != MailErrorCodeServerUnreachable || e.ResolveMethod != MailErrorResolveMethodRetry {
				t.Fatal("known category rejected or retry semantics changed")
			}
			for _, malformed := range []string{e.Message + " private", "private " + e.Message, e.Message[:len(e.Message)-1]} {
				if IsKnownIMAPConnectionMessage(malformed) {
					t.Fatal("nonexact envelope recognized")
				}
			}
		}
	}
	for _, message := range []string{ErrMailServerUnreachable.Message, ErrMailServerUnreachableAt("SMTP dial", errors.New("connection refused")).Message,
		ErrMailServerUnreachableAt("IMAP unsupported", errors.New("connection refused")).Message,
		ErrMailServerUnreachableAt("IMAP dial", errors.New("private raw cause")).Message, ""} {
		if IsKnownIMAPConnectionMessage(message) {
			t.Fatal("unrecognized stage or cause accepted")
		}
	}
	if ErrMailServerUnreachableAt("", errors.New("raw")) != ErrMailServerUnreachable || ErrMailServerUnreachableAt("IMAP dial", nil) != ErrMailServerUnreachable {
		t.Fatal("generic factory compatibility changed")
	}
}
