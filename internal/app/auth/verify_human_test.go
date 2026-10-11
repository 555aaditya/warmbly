package auth

import (
	"context"
	"testing"

	"github.com/warmbly/warmbly/internal/app/appattest"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/pkg/captcha"
)

func TestVerifyHumanRouting(t *testing.T) {
	proof := &appattest.Proof{KeyID: "k", Assertion: "a", Challenge: "c"}

	off := &authService{captcha: captcha.NewTurnstileFromConfig(captcha.TurnstileConfig{Disabled: true})}
	if err := off.verifyHuman(context.Background(), appattest.ActionLogin, "", proof, "203.0.113.1"); err != nil {
		t.Fatalf("no captcha on the deployment, proof sent: %v", err)
	}

	// With App Attest off a proof is no substitute: the Turnstile check still runs.
	on := &authService{captcha: captcha.NewTurnstileFromConfig(captcha.TurnstileConfig{Secret: "s"})}
	if err := on.verifyHuman(context.Background(), appattest.ActionLogin, "", proof, "203.0.113.1"); err != errx.ErrCaptcha {
		t.Fatalf("err = %v, want the captcha refusal", err)
	}
}
