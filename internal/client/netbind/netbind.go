// Package netbind constructs outbound dialers with an optional source IP.
//
// Use cases:
//   - Self-hoster on a single-IP VPS: no env var set, default route is used.
//   - Hosted Warmbly on a multi-IP box: WORKER_BIND_IP set per process so
//     SMTP/IMAP/HTTPS egress leaves from the assigned IP.
//   - Multi-egress per process (future): callers pass an explicit *net.TCPAddr
//     overriding the env-var fallback.
package netbind

import (
	"crypto/tls"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	envBindIP      = "WORKER_BIND_IP"
	envInsecureTLS = "MAIL_TLS_INSECURE"
	defaultTimeout = 10 * time.Second
)

var (
	envOnce sync.Once
	envIP   *net.TCPAddr
)

// FromEnv returns the *net.TCPAddr derived from the WORKER_BIND_IP env var, or
// nil if unset/invalid. The lookup is cached for the lifetime of the process.
func FromEnv() *net.TCPAddr {
	envOnce.Do(func() {
		raw := os.Getenv(envBindIP)
		if raw == "" {
			return
		}
		ip := net.ParseIP(raw)
		if ip == nil {
			return
		}
		envIP = &net.TCPAddr{IP: ip}
	})
	return envIP
}

// Dialer returns a *net.Dialer bound to the given local address. If local is
// nil, the env var is consulted. If still unset, the OS default route is used.
func Dialer(local *net.TCPAddr) *net.Dialer {
	if local == nil {
		local = FromEnv()
	}
	return &net.Dialer{
		Timeout:   defaultTimeout,
		LocalAddr: local,
	}
}

// TLSDialer mirrors Dialer for TLS connections (IMAP, HTTPS).
func TLSDialer(local *net.TCPAddr, cfg *tls.Config) *tls.Dialer {
	if cfg != nil && InsecureTLS() {
		cfg.InsecureSkipVerify = true
	}
	return &tls.Dialer{
		NetDialer: Dialer(local),
		Config:    cfg,
	}
}

var (
	insecureOnce sync.Once
	insecureTLS  bool
)

// InsecureTLS reports whether MAIL_TLS_INSECURE=true applies. It is honoured
// only in development (APP_ENV dev or unset), for the local sandbox; anywhere
// else a server without a valid certificate needs the mailbox's own opt-out.
func InsecureTLS() bool {
	insecureOnce.Do(func() {
		set := os.Getenv(envInsecureTLS) == "true"
		insecureTLS = insecureTLSApplies(set, os.Getenv("APP_ENV"))
		switch {
		case insecureTLS:
			log.Warn().Msg("MAIL_TLS_INSECURE=true: mailbox certificates are not verified and SMTP may skip STARTTLS (development only)")
		case set:
			log.Error().Msg("MAIL_TLS_INSECURE=true is ignored outside APP_ENV=dev: mailbox certificates are verified unless the mailbox records its own opt-out")
		}
	})
	return insecureTLS
}

// insecureTLSApplies uses the backend's reading of APP_ENV: unset is dev.
func insecureTLSApplies(set bool, appEnv string) bool {
	switch strings.ToLower(strings.TrimSpace(appEnv)) {
	case "", "dev", "development", "local":
		return set
	}
	return false
}

// LoopbackPeer reports whether conn's peer is this machine.
//
// The host string was already checked before dialing, but a name check is a
// promise about DNS, not about the socket: the answer can change between the
// two. This asks the connection itself, which is the only thing that cannot
// be moved after the fact, and is what makes the cleartext mailbox mode
// (models.MailSecurityNone) safe to obey.
func LoopbackPeer(conn net.Conn) bool {
	if conn == nil {
		return false
	}
	addr, ok := conn.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return false
	}
	return addr.IP.IsLoopback()
}
