package captcha

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/observability/errs"
)

type TurnstileConfig struct {
	Secret        string
	SiteVerifyURL string
	HTTPClient    *http.Client
	ExpectedHost  string // optional: verify hostname
	BypassToken   string // optional: local/dev bypass token
	Disabled      bool   // CAPTCHA_PROVIDER=none: accept every request
}

type Response struct {
	Success     bool     `json:"success"`
	ChallengeTs string   `json:"challenge_ts,omitempty"`
	Hostname    string   `json:"hostname,omitempty"`
	ErrorCodes  []string `json:"error-codes,omitempty"`
	Action      string   `json:"action,omitempty"`
	CData       string   `json:"cdata,omitempty"`
}

type Turnstile struct {
	cfg TurnstileConfig
}

const CloudflareTurnstileVerifyEndpoint = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

func NewTurnstile(secret string) *Turnstile {
	return NewTurnstileFromConfig(TurnstileConfig{
		Secret:        secret,
		SiteVerifyURL: CloudflareTurnstileVerifyEndpoint,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	})
}

func NewTurnstileFromConfig(cfg TurnstileConfig) *Turnstile {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.SiteVerifyURL == "" {
		cfg.SiteVerifyURL = CloudflareTurnstileVerifyEndpoint
	}
	return &Turnstile{cfg: cfg}
}

// Required reports whether this deployment checks for a human at all.
func (t *Turnstile) Required() bool { return t != nil && !t.cfg.Disabled }

func (t *Turnstile) Verify(ctx context.Context, token, remoteIP string) *errx.Error {
	if t.cfg.Disabled {
		return nil
	}

	if t.cfg.BypassToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(t.cfg.BypassToken)) == 1 {
		return nil
	}

	if token == "" {
		return errx.ErrCaptcha
	}

	data := url.Values{
		"secret":   {t.cfg.Secret},
		"response": {token},
	}
	if remoteIP != "" {
		if net.ParseIP(remoteIP) == nil {
			return errx.ErrCaptcha
		}
		data.Set("remoteip", remoteIP)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		t.cfg.SiteVerifyURL,
		strings.NewReader(data.Encode()),
	)
	if err != nil {
		errs.CaptureException(err)
		return errx.InternalError()
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := t.cfg.HTTPClient.Do(req)
	if err != nil {
		errs.CaptureException(err)
		return errx.InternalError()
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<12)) // 4KB max
		if readErr != nil {
			errs.CaptureException(readErr)
		}
		errs.CaptureException(fmt.Errorf("turnstile verify http status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw))))
		return errx.ErrCaptcha
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB max
	if err != nil {
		errs.CaptureException(err)
		return errx.InternalError()
	}

	var r Response
	if err := json.Unmarshal(body, &r); err != nil {
		errs.CaptureException(err)
		return errx.InternalError()
	}

	if !r.Success {
		return errx.ErrCaptcha
	}

	// Optional: verify hostname
	if t.cfg.ExpectedHost != "" && r.Hostname != t.cfg.ExpectedHost {
		return errx.ErrCaptcha
	}

	// Optional: verify timestamp is recent (prevent replay)
	if r.ChallengeTs != "" {
		ts, err := time.Parse(time.RFC3339, r.ChallengeTs)
		if err != nil {
			return errx.ErrCaptcha
		}
		if time.Since(ts) > 5*time.Minute {
			return errx.ErrCaptcha
		}
	}

	return nil
}
