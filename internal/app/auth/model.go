package auth

import (
	"github.com/warmbly/warmbly/internal/app/appattest"
	"github.com/warmbly/warmbly/internal/models"
)

type AuthData struct {
	Email     string `json:"email"`
	Password  string `json:"password"`
	Turnstile string `json:"turnstile"`
	// AppAttest is the native app's proof, sent instead of Turnstile.
	AppAttest *appattest.Proof `json:"app_attest,omitempty"`
	// ReferralCode is the optional ?ref= code the signup arrived with. Carried
	// to RegistrationConfirm so the new org can be attributed to its referrer.
	ReferralCode string `json:"referral_code"`
	// Invite is the team-invitation token the signup arrived with. It is what
	// lets an invited person create an account under DISABLE_REGISTRATION=invite_only.
	Invite string `json:"invite"`
	// Acquisition is where the visitor came from, read by the dashboard from
	// the signup URL's query string. Recorded once on the new organization and
	// never updated. Absent for a direct visit, which is most signups.
	Acquisition models.OrgAcquisition `json:"acquisition"`
	// DeviceToken is what LoginConfirm returned when the user asked to
	// remember this browser. Login only: it skips the emailed code.
	DeviceToken string `json:"device_token"`
}

type ConfirmData struct {
	Session   string `json:"session"`
	Code      string `json:"code"`
	Turnstile string `json:"turnstile"`
	// RememberDevice asks for a trusted-device token with the session, so the
	// next sign-in from this browser skips the emailed code. Login only.
	RememberDevice bool `json:"remember_device"`
}

type ResetPasswordStart struct {
	Email     string           `json:"email"`
	Turnstile string           `json:"turnstile"`
	AppAttest *appattest.Proof `json:"app_attest,omitempty"`
}

type ResetPasswordConfirm struct {
	Session   string           `json:"session"`
	Password  string           `json:"password"`
	Turnstile string           `json:"turnstile"`
	AppAttest *appattest.Proof `json:"app_attest,omitempty"`
}

type ChangePassword struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// SSOLinkData is what POST /auth/sso/link takes: the challenge a federated
// sign-in came back with, and the password of the account it resolved to.
type SSOLinkData struct {
	PendingToken string `json:"pending_token"`
	Password     string `json:"password"`
}
