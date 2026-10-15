package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/observability/errs"
	"github.com/warmbly/warmbly/internal/pkg/idtoken"
)

// A signed-in account with no password re-proves itself the way it signs in:
// through the identity provider it is linked to. Unlike sign-in, nothing here
// resolves an account. The verified (issuer, subject) must already belong to
// the session's own user, so the email fallback never applies.

const ssoPurposeReauth = "reauth"

// reauthStatePrefix routes a provider callback to the re-authentication
// landing instead of the sign-in one. Hex states never contain "_".
const reauthStatePrefix = "ra_"

// IsReauthState reports whether a callback's state was minted by ReauthSSOBegin.
func IsReauthState(state string) bool { return strings.HasPrefix(state, reauthStatePrefix) }

// reauthHandoffTTL covers the popup's redirect, its message to the opener and
// the opener's finish call.
const reauthHandoffTTL = 2 * time.Minute

// reauthNonceTTL is how long the app has to finish the provider sheet.
const reauthNonceTTL = 10 * time.Minute

func reauthHandoffKey(code string) string { return "reauth_handoff:" + code }

func reauthNonceKey(sessionID uuid.UUID) string { return "reauth_nonce:" + sessionID.String() }

// reauthHandoff is a verified identity waiting for the session that began the
// flow to collect it.
type reauthHandoff struct {
	Binding   string    `json:"binding"`
	UserID    uuid.UUID `json:"user_id"`
	SessionID uuid.UUID `json:"session_id"`
}

// ReauthIdentities lists the providers the user can re-authenticate with:
// browser flows this deployment has configured and native ID tokens it can
// verify, each only where the account has an identity from that provider.
func (s *authService) ReauthIdentities(ctx context.Context, userID uuid.UUID) ([]string, []string, *errx.Error) {
	if s.identities == nil {
		return nil, nil, nil
	}
	linked, err := s.identities.ListForUser(ctx, userID)
	if err != nil {
		errs.CaptureException(err)
		return nil, nil, errx.InternalError()
	}
	has := map[string]bool{}
	for _, id := range linked {
		has[id.Provider] = true
	}

	browser := []string{}
	for _, name := range s.FederatedProviders() {
		if has[name] {
			browser = append(browser, name)
		}
	}
	native := []string{}
	if has[models.IdentityProviderApple] && s.appleIDTokens != nil {
		native = append(native, models.IdentityProviderApple)
	}
	if has[models.IdentityProviderGoogle] && s.googleIDTokens != nil {
		native = append(native, models.IdentityProviderGoogle)
	}
	return browser, native, nil
}

// ReauthSSOBegin starts a provider authorization that re-proves the holder of
// one session rather than signing anyone in.
func (s *authService) ReauthSSOBegin(ctx context.Context, provider string, userID, sessionID uuid.UUID) (*SSORedirect, *errx.Error) {
	p, ok := s.providers[provider]
	if !ok || p == nil {
		return nil, errx.ErrExternalProvider
	}
	return s.beginFlow(ctx, p, ssoFlow{
		Provider:  provider,
		Purpose:   ssoPurposeReauth,
		UserID:    userID,
		SessionID: sessionID,
	}, reauthStatePrefix)
}

// completeReauthFlow runs at the callback once the provider's claims are
// verified: the identity must be the flow user's own, and the result waits
// behind a single-use code only the initiating browser can redeem.
func (s *authService) completeReauthFlow(ctx context.Context, flow ssoFlow, claims *idtoken.Claims) (string, *errx.Error) {
	if xerr := s.identityBelongsTo(ctx, claims, flow.UserID); xerr != nil {
		return "", xerr
	}
	code, err := randomHex(32)
	if err != nil {
		errs.CaptureException(err)
		return "", errx.InternalError()
	}
	payload, err := json.Marshal(reauthHandoff{Binding: flow.Binding, UserID: flow.UserID, SessionID: flow.SessionID})
	if err != nil {
		errs.CaptureException(err)
		return "", errx.InternalError()
	}
	if err := s.cache.SetEx(ctx, reauthHandoffKey(code), payload, reauthHandoffTTL).Err(); err != nil {
		errs.CaptureException(err)
		return "", errx.InternalError()
	}
	return code, nil
}

// ReauthSSOFinish redeems the callback's code for the session that began the
// flow. The binding never left that browser, so a code forwarded elsewhere is
// worthless, and a wrong binding leaves the code for its rightful owner.
func (s *authService) ReauthSSOFinish(ctx context.Context, code, binding string, userID, sessionID uuid.UUID) *errx.Error {
	if code == "" || binding == "" {
		return errx.ErrReauthExpired
	}
	key := reauthHandoffKey(code)
	raw, err := s.cache.Get(ctx, key).Bytes()
	if err != nil || len(raw) == 0 {
		return errx.ErrReauthExpired
	}
	var handoff reauthHandoff
	if err := json.Unmarshal(raw, &handoff); err != nil {
		errs.CaptureException(err)
		return errx.InternalError()
	}
	if subtle.ConstantTimeCompare([]byte(handoff.Binding), []byte(binding)) != 1 ||
		handoff.UserID != userID || handoff.SessionID != sessionID {
		return errx.ErrSSOBrowser
	}
	// Del reports whether this call removed it, which makes redemption single-use.
	if n, derr := s.cache.Del(ctx, key).Result(); derr != nil || n != 1 {
		return errx.ErrReauthExpired
	}
	return s.refuseSuspended(ctx, userID)
}

// ReauthNativeBegin mints the nonce the app hands the provider, so the ID
// token it gets back is single-use and bound to this session.
func (s *authService) ReauthNativeBegin(ctx context.Context, sessionID uuid.UUID) (string, *errx.Error) {
	nonce, err := randomHex(32)
	if err != nil {
		errs.CaptureException(err)
		return "", errx.InternalError()
	}
	if err := s.cache.SetEx(ctx, reauthNonceKey(sessionID), nonce, reauthNonceTTL).Err(); err != nil {
		errs.CaptureException(err)
		return "", errx.InternalError()
	}
	return nonce, nil
}

// ReauthNativeVerify accepts a native Sign in with Apple or Google ID token
// carrying the session's nonce and an identity linked to the account.
func (s *authService) ReauthNativeVerify(ctx context.Context, provider, rawToken string, userID, sessionID uuid.UUID) *errx.Error {
	var verifier IDTokenVerifier
	switch provider {
	case models.IdentityProviderApple:
		verifier = s.appleIDTokens
	case models.IdentityProviderGoogle:
		verifier = s.googleIDTokens
	}
	if verifier == nil {
		return errx.ErrExternalProvider
	}
	if rawToken == "" {
		return errx.ErrExternalCode
	}

	// Spent before the token is checked, so one nonce answers one attempt.
	nonce, err := s.cache.GetDel(ctx, reauthNonceKey(sessionID)).Result()
	if err != nil || nonce == "" {
		return errx.ErrReauthExpired
	}

	claims, verr := verifier.Verify(ctx, rawToken)
	if verr != nil {
		return errx.ErrExternalCode
	}
	if subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		return errx.ErrReauthExpired
	}
	if xerr := s.identityBelongsTo(ctx, claims, userID); xerr != nil {
		return xerr
	}
	return s.refuseSuspended(ctx, userID)
}

// identityBelongsTo accepts only an identity already bound to userID.
func (s *authService) identityBelongsTo(ctx context.Context, claims *idtoken.Claims, userID uuid.UUID) *errx.Error {
	if s.identities == nil || claims == nil || claims.Issuer == "" || claims.Subject == "" {
		return errx.ErrReauthIdentityMismatch
	}
	owner, err := s.identities.FindUserByIdentity(ctx, claims.Issuer, claims.Subject)
	if err != nil {
		errs.CaptureException(err)
		return errx.InternalError()
	}
	if owner == uuid.Nil || owner != userID {
		return errx.ErrReauthIdentityMismatch
	}
	_ = s.identities.TouchLogin(ctx, claims.Issuer, claims.Subject)
	return nil
}
