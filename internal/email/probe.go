package email

import (
	"context"
	"errors"
	"io"
	"net"
	"net/textproto"
	"os"
	"strings"
	"syscall"
	"unicode"

	"github.com/emersion/go-imap/v2"
	"github.com/warmbly/warmbly/internal/client/mailtls"
	"github.com/warmbly/warmbly/internal/models"
)

// ProbeResult is the outcome of one credential probe. Reason is one of the
// models.MailProbe* values; Detail is the server's reply or the dial error.
type ProbeResult struct {
	OK     bool
	Reason string
	Detail string
	// Port and Security are set when the server was reached on a port other
	// than the one asked for.
	Port     int
	Security string
	// Certificate is the leaf the server presented, when TLS got that far.
	Certificate *models.MailCertificate
}

// Leg converts the result to the wire shape the worker publishes.
func (r ProbeResult) Leg() models.EmailValidationLeg {
	return models.EmailValidationLeg{OK: r.OK, Reason: r.Reason, Detail: r.Detail, Port: r.Port, Security: r.Security, Certificate: r.Certificate}
}

// tlsFail classifies a failed handshake and keeps the certificate the server
// presented, so the owner can be shown what a confirmation would trust.
func tlsFail(ctx context.Context, err error, capture *mailtls.Capture) ProbeResult {
	res := probeFail(ctx, models.MailProbeTLS, err)
	if perr := mailtls.AsPinError(err, capture); perr != nil {
		res.Reason = models.MailProbeTLS
		if perr.Kind == mailtls.PinMissing {
			res.Detail = "the server's certificate has not been confirmed for this mailbox"
		} else {
			res.Detail = "the server's certificate does not match the one confirmed for this mailbox"
		}
	}
	res.Certificate = capture.Certificate(err)
	return res
}

func probeOK() ProbeResult { return ProbeResult{OK: true} }

// probeFail classifies err. A deadline wins over the given reason, because a
// server that never answered has not refused anything; a reply that did
// arrive keeps its reason even if the deadline passed while reading it.
func probeFail(ctx context.Context, reason string, err error) ProbeResult {
	if !isServerReply(err) && timedOut(ctx, err) {
		reason = models.MailProbeTimeout
	}
	detail := probeDetail(err)
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		detail = netDetail(err)
	}
	return ProbeResult{Reason: reason, Detail: detail}
}

// isServerReply reports whether err is the server's own tagged answer.
func isServerReply(err error) bool {
	var te *textproto.Error
	var ie *imap.Error
	return errors.As(err, &te) || errors.As(err, &ie)
}

// netDetail describes a socket failure in closed words. A net.OpError names
// both ends of the socket when the worker binds a local address, and the
// worker's address is not the customer's to see.
func netDetail(err error) string {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsNotFound {
			return "the host name does not exist"
		}
		return "the host name could not be resolved"
	}
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "the port refused the connection"
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return "the host is not reachable from the worker's network"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "the server closed the connection"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "the connection timed out"
	}
	return "the connection failed"
}

func probeFailText(reason, detail string) ProbeResult {
	return ProbeResult{Reason: reason, Detail: detail}
}

// timedOut reports whether err, or the context it ran under, is a deadline.
func timedOut(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return true
	}
	if err == nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded)
}

// probeDetail flattens an error into one printable line, capped, so a server
// reply can be shown at the form without dragging a protocol dump along.
func probeDetail(err error) string {
	if err == nil {
		return ""
	}
	const maxDetail = 240
	var b strings.Builder
	space := false
	for _, r := range err.Error() {
		if unicode.IsSpace(r) || !unicode.IsPrint(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
		if b.Len() >= maxDetail {
			break
		}
	}
	return b.String()
}
