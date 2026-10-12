package appleauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func testClient(t *testing.T, tokenURL string) (*Client, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	p8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	c, err := New("com.example.service", "TEAM123", "KEY123", base64.StdEncoding.EncodeToString(p8))
	if err != nil {
		t.Fatal(err)
	}
	c.tokenURL = tokenURL
	return c, key
}

func TestNewRejectsBadKeys(t *testing.T) {
	if _, err := New("id", "team", "key", "not base64!"); err == nil {
		t.Error("expected an error for invalid base64")
	}
	if _, err := New("id", "team", "key", base64.StdEncoding.EncodeToString([]byte("no pem here"))); err == nil {
		t.Error("expected an error for a non-PEM key")
	}
	if _, err := New("", "team", "key", ""); err == nil {
		t.Error("expected an error for a missing client id")
	}
}

// The client secret is what Apple authenticates the exchange with: an ES256
// JWT from the team, about the Services ID, for Apple, naming the key.
func TestExchangeCodeSendsSignedClientSecret(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","expires_in":3600,"id_token":"idt","refresh_token":"rt","token_type":"Bearer"}`))
	}))
	defer srv.Close()

	c, key := testClient(t, srv.URL)
	resp, err := c.ExchangeCode(context.Background(), "the-code", "https://api.example.com/cb")
	if err != nil {
		t.Fatal(err)
	}
	if resp.IDToken != "idt" || resp.RefreshToken != "rt" {
		t.Errorf("unexpected response %+v", resp)
	}

	for k, want := range map[string]string{
		"client_id":    "com.example.service",
		"code":         "the-code",
		"grant_type":   "authorization_code",
		"redirect_uri": "https://api.example.com/cb",
	} {
		if got := form.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}

	claims := jwt.MapClaims{}
	tok, err := jwt.ParseWithClaims(form.Get("client_secret"), claims, func(*jwt.Token) (any, error) {
		return &key.PublicKey, nil
	}, jwt.WithValidMethods([]string{"ES256"}), jwt.WithIssuer("TEAM123"), jwt.WithSubject("com.example.service"), jwt.WithAudience(appleAudience))
	if err != nil {
		t.Fatalf("client secret does not verify: %v", err)
	}
	if kid := tok.Header["kid"]; kid != "KEY123" {
		t.Errorf("kid = %v", kid)
	}
	if aud, ok := claims["aud"].(string); !ok || aud != appleAudience {
		t.Errorf("aud = %#v, want the single string %q", claims["aud"], appleAudience)
	}
}

func TestExchangeCodeMapsInvalidGrant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()

	c, _ := testClient(t, srv.URL)
	if _, err := c.ExchangeCode(context.Background(), "used", "https://api.example.com/cb"); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("err = %v, want ErrInvalidGrant", err)
	}
}

func TestExchangeCodeReportsOtherErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer srv.Close()

	c, _ := testClient(t, srv.URL)
	_, err := c.ExchangeCode(context.Background(), "code", "https://api.example.com/cb")
	if err == nil || errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("err = %v, want a non-invalid_grant error", err)
	}
}
