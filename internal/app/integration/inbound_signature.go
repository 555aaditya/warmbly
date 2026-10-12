package integration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// inboundSigningKeyField is the sealed-config key holding a Calendly or Cal.com
// webhook signing key. Once set, a delivery without a valid signature is refused.
const inboundSigningKeyField = "signing_key"

// calendlySignatureTolerance bounds how old a Calendly signature timestamp may
// be, so a captured delivery cannot be replayed later.
const calendlySignatureTolerance = 5 * time.Minute

// Signing keys are pasted by a person; the bounds only refuse obvious mistakes.
const (
	minInboundSigningKeyLen = 8
	maxInboundSigningKeyLen = 512
)

var (
	// ErrNotInboundProvider means the connection does not receive webhooks.
	ErrNotInboundProvider = errors.New("only Calendly and Cal.com connections take a signing key")
	// ErrInboundSigningKeyLength means the pasted key is outside the accepted length.
	ErrInboundSigningKeyLength = errors.New("signing key must be between 8 and 512 characters")
)

// IsInboundProvider reports whether the provider POSTs bookings to a minted URL.
func IsInboundProvider(p models.IntegrationProvider) bool {
	return p == models.IntegrationCalendly || p == models.IntegrationCalCom
}

// mintsInboundURL reports whether a provider is reached through a minted inbound URL.
func mintsInboundURL(p models.IntegrationProvider) bool {
	return IsInboundProvider(p) || p == models.IntegrationClay
}

// SetInboundSigningKey stores (or, with an empty key, removes) the key that
// inbound deliveries for this connection must be signed with.
func (s *service) SetInboundSigningKey(ctx context.Context, orgID, connID uuid.UUID, key string) (*models.IntegrationConnection, error) {
	key = strings.TrimSpace(key)
	if key != "" && (len(key) < minInboundSigningKeyLen || len(key) > maxInboundSigningKeyLen) {
		return nil, ErrInboundSigningKeyLength
	}
	sec, err := s.repo.GetConnectionSecrets(ctx, connID)
	if err != nil {
		return nil, err
	}
	if sec == nil || sec.Conn.OrganizationID != orgID {
		return nil, repository.ErrInboundConnectionNotFound
	}
	if !IsInboundProvider(sec.Conn.Provider) {
		return nil, ErrNotInboundProvider
	}
	cfg, err := s.openConfig(ctx, sec)
	if err != nil {
		return nil, err
	}
	if key == "" {
		delete(cfg, inboundSigningKeyField)
	} else {
		cfg[inboundSigningKeyField] = key
	}
	sealed, err := s.sealConfig(ctx, orgID, cfg)
	if err != nil {
		return nil, err
	}
	if err := s.repo.SetInboundSigningConfig(ctx, orgID, connID, sealed, key != ""); err != nil {
		return nil, err
	}
	return s.repo.GetConnectionByID(ctx, orgID, connID)
}

// InboundSigningKey returns the connection's signing key, or "" when none is set.
func (s *service) InboundSigningKey(ctx context.Context, conn *models.IntegrationConnection) (string, error) {
	sec, err := s.repo.GetConnectionSecrets(ctx, conn.ID)
	if err != nil {
		return "", err
	}
	if sec == nil || sec.Conn.OrganizationID != conn.OrganizationID {
		return "", repository.ErrInboundConnectionNotFound
	}
	cfg, err := s.openConfig(ctx, sec)
	if err != nil {
		return "", err
	}
	return stringFromMap(cfg, inboundSigningKeyField), nil
}

// VerifyInboundSignature checks a delivery against the provider's scheme:
// Calendly signs "t.body" and sends "t=<unix>,v1=<hex>" in
// Calendly-Webhook-Signature; Cal.com signs the body and sends the hex digest
// in X-Cal-Signature-256.
func VerifyInboundSignature(provider models.IntegrationProvider, key string, header func(string) string, body []byte, now time.Time) bool {
	switch provider {
	case models.IntegrationCalendly:
		return verifyCalendlySignature(key, header("Calendly-Webhook-Signature"), body, now)
	case models.IntegrationCalCom:
		return verifyHexHMAC(key, "", strings.TrimSpace(header("X-Cal-Signature-256")), body)
	}
	return false
}

func verifyCalendlySignature(key, header string, body []byte, now time.Time) bool {
	var ts, sig string
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			ts = v
		case "v1":
			sig = v
		}
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || sig == "" {
		return false
	}
	age := now.Sub(time.Unix(unix, 0))
	if age > calendlySignatureTolerance || age < -calendlySignatureTolerance {
		return false
	}
	return verifyHexHMAC(key, ts+".", sig, body)
}

// verifyHexHMAC compares hex(HMAC-SHA256(key, prefix+body)) with sig in constant time.
func verifyHexHMAC(key, prefix, sig string, body []byte) bool {
	got, err := hex.DecodeString(strings.TrimPrefix(sig, "sha256="))
	if err != nil || len(got) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(prefix))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), got)
}
