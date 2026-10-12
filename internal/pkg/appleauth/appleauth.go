// Package appleauth is the server side of Sign in with Apple's web flow: it
// builds the authorization URL and trades the returned code at Apple's token
// endpoint, authenticating with a client secret signed by the team's .p8 key.
// The ID token that comes back is verified separately (internal/pkg/idtoken).
package appleauth

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/warmbly/warmbly/internal/pkg/applekey"
)

const (
	authorizeEndpoint = "https://appleid.apple.com/auth/authorize"
	tokenEndpoint     = "https://appleid.apple.com/auth/token"
	appleAudience     = "https://appleid.apple.com"

	// A fresh secret is signed per exchange, so it only has to outlive the
	// request; Apple's ceiling is six months.
	clientSecretTTL = 5 * time.Minute
)

// ErrInvalidGrant is Apple's invalid_grant: the code was expired, already used,
// or issued for a different client or redirect URI.
var ErrInvalidGrant = errors.New("appleauth: invalid_grant")

// TokenResponse is Apple's token endpoint response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
}

// Client exchanges authorization codes for one Services ID.
type Client struct {
	clientID string
	teamID   string
	keyID    string
	key      *ecdsa.PrivateKey
	http     *http.Client
	tokenURL string
}

// New builds a client from the AuthKey_<keyID>.p8 file, as PEM or base64.
func New(clientID, teamID, keyID, keyValue string) (*Client, error) {
	if clientID == "" || teamID == "" || keyID == "" {
		return nil, errors.New("appleauth: client id, team id and key id are required")
	}
	key, err := applekey.Parse(keyValue)
	if err != nil {
		return nil, err
	}
	return &Client{
		clientID: clientID,
		teamID:   teamID,
		keyID:    keyID,
		key:      key,
		http:     &http.Client{Timeout: 10 * time.Second},
		tokenURL: tokenEndpoint,
	}, nil
}

func (c *Client) clientSecret(now time.Time) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": c.teamID,
		"sub": c.clientID,
		"aud": appleAudience,
		"iat": now.Unix(),
		"exp": now.Add(clientSecretTTL).Unix(),
	})
	token.Header["kid"] = c.keyID
	return token.SignedString(c.key)
}

// ExchangeCode trades an authorization code at Apple's token endpoint. The
// redirect URI must be the one the authorization request was sent with.
func (c *Client) ExchangeCode(ctx context.Context, code, redirectURI string) (*TokenResponse, error) {
	secret, err := c.clientSecret(time.Now())
	if err != nil {
		return nil, fmt.Errorf("appleauth: signing client secret: %w", err)
	}
	form := url.Values{
		"client_id":     {c.clientID},
		"client_secret": {secret},
		"code":          {code},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("appleauth: token request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("appleauth: reading token response: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		var apiErr struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &apiErr)
		if apiErr.Error == "invalid_grant" {
			return nil, ErrInvalidGrant
		}
		if apiErr.Error != "" {
			return nil, fmt.Errorf("appleauth: token endpoint returned %s (HTTP %d)", apiErr.Error, res.StatusCode)
		}
		return nil, fmt.Errorf("appleauth: token endpoint returned HTTP %d", res.StatusCode)
	}

	var tr TokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("appleauth: decoding token response: %w", err)
	}
	return &tr, nil
}

// AuthorizeURLConfig is the authorization request. Any scope makes Apple
// require response_mode=form_post.
type AuthorizeURLConfig struct {
	ClientID     string
	RedirectURI  string
	State        string
	Nonce        string
	Scope        []string
	ResponseType string
	ResponseMode string
}

// AuthorizeURL builds the URL the browser is sent to.
func AuthorizeURL(cfg AuthorizeURLConfig) string {
	responseType := cfg.ResponseType
	if responseType == "" {
		responseType = "code"
	}
	responseMode := cfg.ResponseMode
	if responseMode == "" {
		responseMode = "form_post"
	}
	q := url.Values{}
	q.Set("response_type", responseType)
	q.Set("response_mode", responseMode)
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", cfg.RedirectURI)
	if cfg.State != "" {
		q.Set("state", cfg.State)
	}
	if cfg.Nonce != "" {
		q.Set("nonce", cfg.Nonce)
	}
	if len(cfg.Scope) > 0 {
		q.Set("scope", strings.Join(cfg.Scope, " "))
	}
	return authorizeEndpoint + "?" + q.Encode()
}
