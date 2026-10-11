package apollo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/warmbly/warmbly/internal/models"
)

const defaultBaseURL = "https://api.apollo.io/api/v1"

// Errors a caller branches on. Messages are written for the person who pasted
// the key, so they can be shown as they are.
var (
	ErrKey         = errors.New("Apollo rejected this API key")
	ErrForbidden   = errors.New("this Apollo API key is not allowed to read lists and contacts. Use a master key, or a key with access to lists and contact search")
	ErrRateLimited = errors.New("Apollo is rate limiting this account. Try again in a few minutes")
	ErrUnavailable = errors.New("Apollo did not answer. Try again in a few minutes")
)

// Client calls Apollo's REST API with one workspace's API key.
type Client struct {
	key  string
	base string
	http *http.Client
	// sleep is replaced in tests so retries do not wait.
	sleep func(ctx context.Context, d time.Duration) error
}

// NewClient builds a client for one API key.
func NewClient(key string) *Client {
	return &Client{
		key:   strings.TrimSpace(key),
		base:  defaultBaseURL,
		http:  &http.Client{Timeout: 30 * time.Second},
		sleep: sleepCtx,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// do sends one request, retrying a rate limit or a server error a few times.
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		raw = b
	}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		var rd io.Reader
		if raw != nil {
			rd = bytes.NewReader(raw)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
		if err != nil {
			return err
		}
		req.Header.Set("x-api-key", c.key)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Cache-Control", "no-cache")
		if raw != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = ErrUnavailable
			if serr := c.sleep(ctx, backoff(attempt, "")); serr != nil {
				return serr
			}
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		_ = resp.Body.Close()
		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			if out == nil {
				return nil
			}
			if err := json.Unmarshal(data, out); err != nil {
				return fmt.Errorf("apollo: decode %s: %w", path, err)
			}
			return nil
		case resp.StatusCode == http.StatusUnauthorized:
			return ErrKey
		case resp.StatusCode == http.StatusForbidden:
			return ErrForbidden
		case resp.StatusCode == http.StatusTooManyRequests:
			lastErr = ErrRateLimited
		case resp.StatusCode >= 500:
			lastErr = ErrUnavailable
		default:
			return &APIError{Status: resp.StatusCode, Message: apiMessage(data)}
		}
		if serr := c.sleep(ctx, backoff(attempt, resp.Header.Get("Retry-After"))); serr != nil {
			return serr
		}
	}
	return lastErr
}

// backoff honours a Retry-After of up to a minute, else grows 1s, 2s, 4s.
func backoff(attempt int, retryAfter string) time.Duration {
	if s, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && s > 0 && s <= 60 {
		return time.Duration(s) * time.Second
	}
	return time.Duration(1<<attempt) * time.Second
}

// APIError is a refusal Apollo explained; Message is Apollo's own words.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("Apollo refused the request (HTTP %d)", e.Status)
	}
	return "Apollo: " + e.Message
}

func apiMessage(data []byte) string {
	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &body) == nil {
		if m := strings.TrimSpace(body.Message); m != "" {
			return truncate(m, 200)
		}
		if m := strings.TrimSpace(body.Error); m != "" {
			return truncate(m, 200)
		}
	}
	return ""
}

// Health confirms the key belongs to a live Apollo account.
func (c *Client) Health(ctx context.Context) error {
	var out struct {
		Healthy    *bool `json:"healthy"`
		IsLoggedIn *bool `json:"is_logged_in"`
	}
	if err := c.do(ctx, http.MethodGet, "/auth/health", nil, &out); err != nil {
		return err
	}
	if out.IsLoggedIn != nil && !*out.IsLoggedIn {
		return ErrKey
	}
	return nil
}

// Profile names the Apollo user the key acts as. Best effort: a key that
// cannot read it still works for lists and contacts.
func (c *Client) Profile(ctx context.Context) (name, email string) {
	var out map[string]any
	if err := c.do(ctx, http.MethodGet, "/users/api_profile", nil, &out); err != nil {
		return "", ""
	}
	src := out
	if u, ok := out["user"].(map[string]any); ok {
		src = u
	}
	str := func(k string) string {
		s, _ := src[k].(string)
		return strings.TrimSpace(s)
	}
	name = str("name")
	if name == "" {
		name = strings.TrimSpace(str("first_name") + " " + str("last_name"))
	}
	return name, str("email")
}

// Lists returns the account's saved lists of contacts.
func (c *Client) Lists(ctx context.Context) ([]models.ApolloList, error) {
	var raw []struct {
		ID          string `json:"id"`
		AltID       string `json:"_id"`
		Name        string `json:"name"`
		Modality    string `json:"modality"`
		CachedCount int    `json:"cached_count"`
	}
	if err := c.do(ctx, http.MethodGet, "/labels", nil, &raw); err != nil {
		return nil, err
	}
	out := make([]models.ApolloList, 0, len(raw))
	for _, l := range raw {
		if l.Modality != "" && l.Modality != "contacts" {
			continue
		}
		id := l.ID
		if id == "" {
			id = l.AltID
		}
		if id == "" {
			continue
		}
		out = append(out, models.ApolloList{ID: id, Name: strings.TrimSpace(l.Name), Count: l.CachedCount})
	}
	return out, nil
}

// PhoneNumber is one of a person's numbers.
type PhoneNumber struct {
	RawNumber       string `json:"raw_number"`
	SanitizedNumber string `json:"sanitized_number"`
	Type            string `json:"type"`
}

// Org is the company a person works at.
type Org struct {
	Name          string `json:"name"`
	WebsiteURL    string `json:"website_url"`
	PrimaryDomain string `json:"primary_domain"`
	Domain        string `json:"domain"`
	Industry      string `json:"industry"`
}

// Person is an Apollo contact or a matched person; the two share these fields.
type Person struct {
	ID               string        `json:"id"`
	FirstName        string        `json:"first_name"`
	LastName         string        `json:"last_name"`
	Name             string        `json:"name"`
	Email            string        `json:"email"`
	EmailStatus      string        `json:"email_status"`
	Title            string        `json:"title"`
	Headline         string        `json:"headline"`
	Seniority        string        `json:"seniority"`
	OrganizationName string        `json:"organization_name"`
	LinkedinURL      string        `json:"linkedin_url"`
	City             string        `json:"city"`
	State            string        `json:"state"`
	Country          string        `json:"country"`
	PhoneNumbers     []PhoneNumber `json:"phone_numbers"`
	Organization     *Org          `json:"organization"`
	Account          *Org          `json:"account"`
	Contact          *struct {
		PhoneNumbers []PhoneNumber `json:"phone_numbers"`
	} `json:"contact"`
}

// Company is the person's employer name, from wherever Apollo put it.
func (p Person) Company() string {
	switch {
	case strings.TrimSpace(p.OrganizationName) != "":
		return strings.TrimSpace(p.OrganizationName)
	case p.Organization != nil && strings.TrimSpace(p.Organization.Name) != "":
		return strings.TrimSpace(p.Organization.Name)
	case p.Account != nil:
		return strings.TrimSpace(p.Account.Name)
	}
	return ""
}

// Phone is the person's first usable number.
func (p Person) Phone() string {
	nums := p.PhoneNumbers
	if len(nums) == 0 && p.Contact != nil {
		nums = p.Contact.PhoneNumbers
	}
	for _, n := range nums {
		if v := strings.TrimSpace(n.SanitizedNumber); v != "" {
			return v
		}
		if v := strings.TrimSpace(n.RawNumber); v != "" {
			return v
		}
	}
	return ""
}

func (p Person) org() Org {
	if p.Organization != nil {
		return *p.Organization
	}
	if p.Account != nil {
		return *p.Account
	}
	return Org{}
}

// Website is the employer's site.
func (p Person) Website() string { return strings.TrimSpace(p.org().WebsiteURL) }

// CompanyDomain is the employer's domain.
func (p Person) CompanyDomain() string {
	o := p.org()
	if d := strings.TrimSpace(o.PrimaryDomain); d != "" {
		return d
	}
	return strings.TrimSpace(o.Domain)
}

// Industry is the employer's industry.
func (p Person) Industry() string { return strings.TrimSpace(p.org().Industry) }

// DisplayName is the person's name as one string.
func (p Person) DisplayName() string {
	if n := strings.TrimSpace(p.Name); n != "" {
		return n
	}
	return strings.TrimSpace(p.FirstName + " " + p.LastName)
}

// SearchPageSize is the most contacts Apollo returns per page.
const SearchPageSize = 100

// SearchContacts reads one page of a list's contacts, or of every contact
// when listID is empty. Costs no credits.
func (c *Client) SearchContacts(ctx context.Context, listID string, page, perPage int) ([]Person, int, int, error) {
	if perPage <= 0 || perPage > SearchPageSize {
		perPage = SearchPageSize
	}
	body := map[string]any{
		"page":           page,
		"per_page":       perPage,
		"sort_by_field":  "contact_created_at",
		"sort_ascending": true,
	}
	if listID != "" {
		body["contact_label_ids"] = []string{listID}
	}
	var out struct {
		Contacts   []Person `json:"contacts"`
		Pagination struct {
			TotalEntries int `json:"total_entries"`
			TotalPages   int `json:"total_pages"`
		} `json:"pagination"`
	}
	if err := c.do(ctx, http.MethodPost, "/contacts/search", body, &out); err != nil {
		return nil, 0, 0, err
	}
	return out.Contacts, out.Pagination.TotalEntries, out.Pagination.TotalPages, nil
}

// MatchBatchSize is the most people one bulk match takes.
const MatchBatchSize = 10

// MatchDetail identifies one person to enrich.
type MatchDetail struct {
	Email            string `json:"email,omitempty"`
	FirstName        string `json:"first_name,omitempty"`
	LastName         string `json:"last_name,omitempty"`
	OrganizationName string `json:"organization_name,omitempty"`
	Domain           string `json:"domain,omitempty"`
	LinkedinURL      string `json:"linkedin_url,omitempty"`
}

// MatchResult is a bulk match's answer: one entry per detail, nil where
// Apollo found nobody.
type MatchResult struct {
	Matches         []*Person
	CreditsConsumed int
}

// BulkMatch enriches up to MatchBatchSize people. Personal emails and phone
// numbers are never revealed: they cost extra credits and phones need a webhook.
func (c *Client) BulkMatch(ctx context.Context, details []MatchDetail) (*MatchResult, error) {
	if len(details) == 0 {
		return &MatchResult{}, nil
	}
	if len(details) > MatchBatchSize {
		return nil, fmt.Errorf("apollo: at most %d people per match", MatchBatchSize)
	}
	body := map[string]any{
		"details":                details,
		"reveal_personal_emails": false,
		"reveal_phone_number":    false,
	}
	var out struct {
		Matches         []*Person `json:"matches"`
		CreditsConsumed int       `json:"credits_consumed"`
	}
	if err := c.do(ctx, http.MethodPost, "/people/bulk_match", body, &out); err != nil {
		return nil, err
	}
	res := &MatchResult{Matches: make([]*Person, len(details)), CreditsConsumed: out.CreditsConsumed}
	if len(out.Matches) == len(details) {
		copy(res.Matches, out.Matches)
		return res, nil
	}
	// Apollo left the unmatched out: line the rest up by address.
	byEmail := map[string]*Person{}
	for _, m := range out.Matches {
		if m != nil && m.Email != "" {
			byEmail[strings.ToLower(strings.TrimSpace(m.Email))] = m
		}
	}
	for i, d := range details {
		if d.Email != "" {
			res.Matches[i] = byEmail[strings.ToLower(strings.TrimSpace(d.Email))]
		}
	}
	return res, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
