// Package appattest is the native app's anti-automation check: a key attested
// by Apple once per install, then an assertion over a single-use challenge on
// every unauthenticated auth write, in place of the browser's Turnstile.
package appattest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"slices"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/infrastructure/cache"
	"github.com/warmbly/warmbly/internal/observability/errs"
	attest "github.com/warmbly/warmbly/internal/pkg/appattest"
	"github.com/warmbly/warmbly/internal/repository"
)

const (
	challengeTTL    = 5 * time.Minute
	challengePrefix = "app_attest:challenge:"
	// keyBudget bounds what one install can trigger, emails included.
	keyBudget       = 30
	keyBudgetWindow = time.Hour
	keyBudgetPrefix = "app_attest:key:"
	clientDataTag   = "warmbly-app-attest/v1"
)

// Actions name the request an assertion was made for, so one cannot be spent on another.
const (
	ActionLogin                = "login"
	ActionRegister             = "register"
	ActionResetPassword        = "reset_password"
	ActionResetPasswordConfirm = "reset_password_confirm"
)

// Proof is what the app sends with a protected request.
type Proof struct {
	KeyID     string `json:"key_id"`
	Assertion string `json:"assertion"`
	Challenge string `json:"challenge"`
}

// Config is what this instance accepts.
type Config struct {
	AppIDs           []string
	AllowDevelopment bool
}

type Service struct {
	repo   repository.AppAttestRepository
	cache  *cache.Cache
	policy attest.Policy
	now    func() time.Time
}

// New returns nil when no app id is configured, which turns the check off.
func New(repo repository.AppAttestRepository, c *cache.Cache, cfg Config) *Service {
	if repo == nil || c == nil || len(cfg.AppIDs) == 0 {
		return nil
	}
	return &Service{
		repo:   repo,
		cache:  c,
		policy: attest.Policy{AppIDs: cfg.AppIDs, AllowDevelopment: cfg.AllowDevelopment},
		now:    time.Now,
	}
}

// Enabled reports whether native app proofs are accepted here.
func (s *Service) Enabled() bool { return s != nil }

// ClientData is the exact byte string the app asserts over.
func ClientData(action, challenge string) []byte {
	return []byte(clientDataTag + "\n" + action + "\n" + challenge)
}

// Challenge mints a single-use challenge.
func (s *Service) Challenge(ctx context.Context) (string, *errx.Error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		errs.CaptureException(err)
		return "", errx.InternalError()
	}
	challenge := base64.RawURLEncoding.EncodeToString(buf)
	if err := s.cache.SetEx(ctx, challengePrefix+challenge, 1, challengeTTL).Err(); err != nil {
		errs.CaptureException(err)
		return "", errx.InternalError()
	}
	return challenge, nil
}

// consume deletes the challenge and reports whether this call was the one that did.
func (s *Service) consume(ctx context.Context, challenge string) (bool, *errx.Error) {
	if challenge == "" || len(challenge) > 128 {
		return false, nil
	}
	n, err := s.cache.Del(ctx, challengePrefix+challenge).Result()
	if err != nil {
		errs.CaptureException(err)
		return false, errx.InternalError()
	}
	return n == 1, nil
}

// RegisterKey verifies a key attestation made over SHA-256 of the challenge and stores the key.
func (s *Service) RegisterKey(ctx context.Context, keyID, attestation, challenge string) *errx.Error {
	ok, xerr := s.consume(ctx, challenge)
	if xerr != nil {
		return xerr
	}
	if !ok {
		return errx.ErrAppAttest
	}
	rawKeyID, err := base64.StdEncoding.DecodeString(keyID)
	if err != nil || len(rawKeyID) != sha256.Size {
		return errx.ErrAppAttest
	}
	object, err := base64.StdEncoding.DecodeString(attestation)
	if err != nil {
		return errx.ErrAppAttest
	}
	clientDataHash := sha256.Sum256([]byte(challenge))
	key, err := attest.VerifyAttestation(object, rawKeyID, clientDataHash[:], s.policy, s.now())
	if err != nil {
		log.Info().Err(err).Msg("app attest: attestation refused")
		return errx.ErrAppAttest
	}
	inserted, err := s.repo.Insert(ctx, repository.AppAttestKey{
		KeyID:       base64.StdEncoding.EncodeToString(key.ID),
		PublicKey:   key.PublicKey,
		AppID:       key.AppID,
		Environment: string(key.Environment),
		Receipt:     key.Receipt,
	})
	if err != nil {
		errs.CaptureException(err)
		return errx.InternalError()
	}
	// A key is attested once; a second attestation of the same id is a replay.
	if !inserted {
		return errx.ErrAppAttest
	}
	return nil
}

// Verify checks an assertion made for action over a fresh challenge.
func (s *Service) Verify(ctx context.Context, action string, proof *Proof) *errx.Error {
	if proof == nil || proof.KeyID == "" || proof.Assertion == "" {
		return errx.ErrAppAttest
	}
	ok, xerr := s.consume(ctx, proof.Challenge)
	if xerr != nil {
		return xerr
	}
	if !ok {
		return errx.ErrAppAttest
	}
	key, err := s.repo.Get(ctx, proof.KeyID)
	if err != nil {
		errs.CaptureException(err)
		return errx.InternalError()
	}
	if key == nil {
		return errx.ErrAppAttestUnknown
	}
	// Fails open on a cache error, like every other auth budget.
	if within, err := s.cache.ReserveAttempt(ctx, keyBudgetPrefix+key.KeyID, keyBudget, keyBudgetWindow); err != nil {
		errs.CaptureException(err)
	} else if !within {
		return errx.ErrAuthLimit
	}
	object, err := base64.StdEncoding.DecodeString(proof.Assertion)
	if err != nil {
		return errx.ErrAppAttest
	}
	// A key outlives the policy it was attested under; it answers to the current one.
	if (key.Environment == string(attest.Development) && !s.policy.AllowDevelopment) || !slices.Contains(s.policy.AppIDs, key.AppID) {
		return errx.ErrAppAttest
	}
	counter, err := attest.VerifyAssertion(object, ClientData(action, proof.Challenge), key.PublicKey, key.AppID, key.Counter, s.policy.AllowDevelopment)
	if err != nil {
		if errors.Is(err, attest.ErrInvalid) {
			log.Info().Err(err).Msg("app attest: assertion refused")
			return errx.ErrAppAttest
		}
		errs.CaptureException(err)
		return errx.InternalError()
	}
	advanced, err := s.repo.AdvanceCounter(ctx, key.KeyID, counter)
	if err != nil {
		errs.CaptureException(err)
		return errx.InternalError()
	}
	if !advanced {
		return errx.ErrAppAttest
	}
	return nil
}
