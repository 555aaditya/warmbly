package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

// ClayEvents are the Warmbly events the Clay page offers to send back to a
// Clay table: the outcomes a Clay workflow branches on.
var ClayEvents = []string{
	string(models.WebhookEventCampaignReplyReceived),
	string(models.WebhookEventCampaignEmailSent),
	string(models.WebhookEventCampaignEmailOpened),
	string(models.WebhookEventCampaignEmailClicked),
	string(models.WebhookEventCampaignEmailBounced),
	string(models.WebhookEventCampaignUnsubscribed),
	string(models.WebhookEventMeetingBooked),
	string(models.WebhookEventMeetingCanceled),
}

// clayAuthHeader carries a Clay webhook source's optional auth token.
const clayAuthHeader = "x-clay-webhook-auth"

// CredentialCheck validates a pasted credential before it is stored and
// returns non-secret facts about the account for the connection's display.
type CredentialCheck func(ctx context.Context, config map[string]any) (map[string]any, error)

// SetCredentialCheck registers the check Connect runs for a provider.
func (s *service) SetCredentialCheck(provider models.IntegrationProvider, check CredentialCheck) {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	if s.checks == nil {
		s.checks = map[models.IntegrationProvider]CredentialCheck{}
	}
	s.checks[provider] = check
}

func (s *service) credentialCheck(provider models.IntegrationProvider) CredentialCheck {
	s.checkMu.RLock()
	defer s.checkMu.RUnlock()
	return s.checks[provider]
}

// ConnectionConfig opens an org-owned connection's sealed config.
func (s *service) ConnectionConfig(ctx context.Context, orgID, connID uuid.UUID) (map[string]any, *models.IntegrationConnection, error) {
	sec, err := s.repo.GetConnectionSecrets(ctx, connID)
	if err != nil {
		return nil, nil, err
	}
	if sec == nil || sec.Conn.OrganizationID != orgID {
		return nil, nil, ErrConnectionNotFound
	}
	cfg, err := s.openConfig(ctx, sec)
	if err != nil {
		return nil, nil, lowerLayer(err)
	}
	conn := sec.Conn
	return cfg, &conn, nil
}

// MergeConnectionConfig overlays keys onto an org-owned connection's sealed
// config; an empty string value removes the key.
func (s *service) MergeConnectionConfig(ctx context.Context, orgID, connID uuid.UUID, patch map[string]any) error {
	cfg, _, err := s.ConnectionConfig(ctx, orgID, connID)
	if err != nil {
		return err
	}
	for k, v := range patch {
		if str, ok := v.(string); ok && strings.TrimSpace(str) == "" {
			delete(cfg, k)
			continue
		}
		cfg[k] = v
	}
	if err := validateOutboundConfigURLs(cfg); err != nil {
		return err
	}
	sealed, err := s.sealConfig(ctx, orgID, cfg)
	if err != nil {
		return err
	}
	return s.repo.SetSigningSecretConfig(ctx, orgID, connID, sealed)
}

// ErrConnectionNotFound means no connection with that id is in the organization.
var ErrConnectionNotFound = errors.New("connection not found")

// clayDeliver posts one flat row to a Clay table's webhook source.
func (s *service) clayDeliver(ctx context.Context, sub models.IntegrationEventSubscription, secretCfg map[string]any, data map[string]any, msg eventMessage) error {
	saved := stringFromMap(secretCfg, "webhook_url")
	target := configString(sub.Config, "url")
	if target == "" {
		target = saved
	}
	if target == "" {
		return errors.New("add the Clay table's webhook URL on the Clay page first")
	}
	rendered, err := renderOutboundURL(target, data)
	if err != nil {
		return err
	}
	// The connection's sealed token goes only to the connection's own table webhook.
	token := configString(sub.Config, "auth_token")
	if token == "" && target == saved {
		token = stringFromMap(secretCfg, "auth_token")
	}
	return clayPost(ctx, rendered, token, clayRow(sub.EventType, data, msg))
}

// clayPost sends one row, retrying a 429 or 5xx twice; Clay rate limits
// webhook sources per table.
func clayPost(ctx context.Context, target, token string, row map[string]any) error {
	body, err := json.Marshal(row)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set(clayAuthHeader, token)
		}
		resp, err := actionHTTP.Do(req)
		if err != nil {
			lastErr = errors.New("Clay did not answer; check the table's webhook URL")
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			return nil
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return errors.New("Clay refused the row; check the webhook auth token")
		case resp.StatusCode == http.StatusNotFound:
			return errors.New("Clay has no table at that webhook URL")
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			lastErr = fmt.Errorf("Clay answered HTTP %d", resp.StatusCode)
			continue
		default:
			return fmt.Errorf("Clay answered HTTP %d", resp.StatusCode)
		}
	}
	return lastErr
}

// urlHost is the host of a URL, for showing which table a connection sends to
// without showing the URL, which is itself the credential.
func urlHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return u.Host
}

// clayRow flattens an event into the one-level object a Clay table turns into
// columns: nested keys are joined with underscores, lists become text.
func clayRow(eventType string, data map[string]any, msg eventMessage) map[string]any {
	row := map[string]any{
		"event":       eventType,
		"occurred_at": time.Now().UTC().Format(time.RFC3339),
	}
	flattenInto(row, "", publicEventData(data), 0)
	if msg.Subject != "" {
		row["subject"] = msg.Subject
	}
	if t := msg.plainText(); t != "" {
		if _, ok := row["content"]; !ok {
			row["content"] = truncate(t, 4000)
		}
	}
	if msg.Email != "" {
		if _, ok := row["email"]; !ok {
			row["email"] = msg.Email
		}
	}
	if _, ok := row["email"]; !ok {
		if v, ok := row["contact_email"]; ok {
			row["email"] = v
		}
	}
	return row
}

func flattenInto(out map[string]any, prefix string, v any, depth int) {
	switch t := v.(type) {
	case map[string]any:
		if depth >= 3 {
			if raw, err := json.Marshal(t); err == nil && prefix != "" {
				out[prefix] = string(raw)
			}
			return
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			key := k
			if prefix != "" {
				key = prefix + "_" + k
			}
			flattenInto(out, key, t[k], depth+1)
		}
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			switch it := item.(type) {
			case string:
				parts = append(parts, it)
			case float64:
				parts = append(parts, strconv.FormatFloat(it, 'f', -1, 64))
			case bool:
				parts = append(parts, strconv.FormatBool(it))
			default:
				if raw, err := json.Marshal(it); err == nil {
					parts = append(parts, string(raw))
				}
			}
		}
		if prefix != "" {
			out[prefix] = strings.Join(parts, ", ")
		}
	case []string:
		if prefix != "" {
			out[prefix] = strings.Join(t, ", ")
		}
	case nil:
		// Clay shows a missing key and an empty one the same way.
	default:
		if prefix != "" {
			out[prefix] = t
		}
	}
}

// ClaySubscriptions returns the events the connection sends to its default
// Clay table.
func (s *service) ClaySubscriptions(ctx context.Context, orgID, connID uuid.UUID) ([]string, error) {
	subs, err := s.repo.ListEventSubscriptions(ctx, orgID, connID)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, sub := range subs {
		if sub.Action == models.IntegrationActionClaySendRow && sub.AutomationID == nil && sub.Enabled {
			out = append(out, sub.EventType)
		}
	}
	sort.Strings(out)
	return out, nil
}

// SetClaySubscriptions replaces the events sent to the connection's default
// Clay table. Automation nodes that send to Clay are not touched.
func (s *service) SetClaySubscriptions(ctx context.Context, orgID, connID uuid.UUID, events []string) error {
	want := map[string]bool{}
	for _, e := range events {
		if !slicesContains(ClayEvents, e) {
			return fmt.Errorf("Clay cannot receive %s", e)
		}
		want[e] = true
	}
	subs, err := s.repo.ListEventSubscriptions(ctx, orgID, connID)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, sub := range subs {
		if sub.Action != models.IntegrationActionClaySendRow || sub.AutomationID != nil {
			continue
		}
		if !want[sub.EventType] || have[sub.EventType] {
			if err := s.repo.DeleteEventSubscription(ctx, orgID, sub.ID); err != nil {
				return err
			}
			continue
		}
		have[sub.EventType] = true
	}
	for e := range want {
		if have[e] {
			continue
		}
		sub := &models.IntegrationEventSubscription{
			ConnectionID:   connID,
			OrganizationID: orgID,
			EventType:      e,
			Action:         models.IntegrationActionClaySendRow,
			Config:         json.RawMessage(`{}`),
			Enabled:        true,
			UseCase:        "clay_events",
		}
		if err := s.repo.CreateEventSubscription(ctx, sub); err != nil {
			return err
		}
	}
	return nil
}

func slicesContains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ClayConnectionByInboundSecret resolves a Clay inbound URL to its connection.
func (s *service) ClayConnectionByInboundSecret(ctx context.Context, secret string) (*models.IntegrationConnection, error) {
	return s.repo.GetConnectionByInboundSecret(ctx, models.IntegrationClay, secret)
}
