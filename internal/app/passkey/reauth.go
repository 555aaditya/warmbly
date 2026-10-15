package passkey

import (
	"context"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
)

// Re-authentication asks for one of the signed-in user's own passkeys, never a
// discoverable one, and keys the challenge by the session it will vouch for.

func reauthKey(sessionID uuid.UUID) string {
	return "webauthn_reauth:" + sessionID.String()
}

func (s *service) BeginReauth(ctx context.Context, userID, sessionID uuid.UUID) (*protocol.CredentialAssertion, *errx.Error) {
	wuser, xerr := s.loadUser(ctx, userID)
	if xerr != nil {
		return nil, xerr
	}
	if len(wuser.credentials) == 0 {
		return nil, errx.ErrPasskeyNone
	}

	options, sessionData, err := s.web.BeginLogin(wuser, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, errx.ErrPasskey
	}
	if xerr := s.saveSession(ctx, reauthKey(sessionID), sessionData); xerr != nil {
		return nil, xerr
	}
	return options, nil
}

func (s *service) FinishReauth(ctx context.Context, userID, sessionID uuid.UUID, credential []byte) *errx.Error {
	sessionData, xerr := s.takeSession(ctx, reauthKey(sessionID))
	if xerr != nil {
		return xerr
	}

	parsed, perr := protocol.ParseCredentialRequestResponseBytes(credential)
	if perr != nil {
		return errx.ErrPasskey
	}
	// The same bar as a passkey sign-in: a PIN or biometric, not just a touch.
	if !parsed.Response.AuthenticatorData.Flags.HasUserVerified() {
		return errx.ErrPasskeyUserVerification
	}

	wuser, xerr := s.loadUser(ctx, userID)
	if xerr != nil {
		return xerr
	}
	// Refuses a challenge minted for another user and a credential that is not one of theirs.
	wcred, verr := s.web.ValidateLogin(wuser, *sessionData, parsed)
	if verr != nil {
		return errx.ErrPasskey
	}

	_ = s.repo.TouchCredential(ctx, wcred.ID, wcred.Authenticator.SignCount, wcred.Authenticator.CloneWarning, wcred.Flags.BackupState, time.Now())
	return nil
}

func (s *service) HasCredentials(ctx context.Context, userID uuid.UUID) (bool, *errx.Error) {
	creds, xerr := s.repo.ListByUser(ctx, userID)
	if xerr != nil {
		return false, xerr
	}
	return len(creds) > 0, nil
}
