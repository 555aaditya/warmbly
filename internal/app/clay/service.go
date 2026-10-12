// Package clay connects Clay tables to Warmbly in both directions: rows a
// Clay HTTP API column posts become contacts, and Warmbly events are sent to
// a Clay table's webhook source.
package clay

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/contact"
	"github.com/warmbly/warmbly/internal/app/integration"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

const (
	// MaxRows is the most rows one inbound request may carry.
	MaxRows = 100
	// MaxBody bounds an inbound request body.
	MaxBody = 1 << 20
	// perMinute bounds inbound requests per connection; Clay retries a 429.
	perMinute = 600
	// maxCustomKeys bounds how many distinct custom fields one request adds.
	maxCustomKeys = 60
	defaultsKey   = "clay_defaults"
)

var (
	// ErrUnknownURL means no Clay connection owns the inbound URL.
	ErrUnknownURL = errors.New("unknown Clay inbound URL")
	// ErrRateLimited means the connection sent too many requests this minute.
	ErrRateLimited = errors.New("too many requests from this Clay table; Clay will retry")
	// ErrNotActive means the connection is not usable.
	ErrNotActive = errors.New("this Clay connection is not active in Warmbly")
)

// Deps is everything the Clay integration talks to.
type Deps struct {
	Repo         repository.ClayRepository
	Integrations integration.Service
	Contacts     contact.ContactService
}

// Service is the Clay integration.
type Service struct {
	Deps
	limMu   sync.Mutex
	windows map[uuid.UUID]*window
}

type window struct {
	start time.Time
	count int
}

// NewService builds the Clay integration.
func NewService(d Deps) *Service {
	return &Service{Deps: d, windows: map[uuid.UUID]*window{}}
}

// allow counts one request against the connection's per-minute budget.
func (s *Service) allow(connID uuid.UUID) bool {
	s.limMu.Lock()
	defer s.limMu.Unlock()
	now := time.Now()
	w := s.windows[connID]
	if w == nil || now.Sub(w.start) >= time.Minute {
		if len(s.windows) > 10000 {
			for id, old := range s.windows {
				if now.Sub(old.start) >= time.Minute {
					delete(s.windows, id)
				}
			}
		}
		s.windows[connID] = &window{start: now, count: 1}
		return true
	}
	w.count++
	return w.count <= perMinute
}

func (s *Service) connection(ctx context.Context, orgID, connID uuid.UUID) (*models.IntegrationConnection, error) {
	c, err := s.Integrations.GetConnection(ctx, orgID, connID)
	if err != nil {
		return nil, err
	}
	if c == nil || c.Provider != models.IntegrationClay {
		return nil, errx.New(errx.NotFound, "Clay connection not found")
	}
	return c, nil
}

// defaultsOf reads a connection's inbound defaults, filling what was never set.
func defaultsOf(c *models.IntegrationConnection) models.ClayInboundDefaults {
	d := models.ClayInboundDefaults{Dedup: models.ContactImportDedupUpdate, Subscribed: true}
	var caps map[string]json.RawMessage
	if len(c.ConfigCapabilities) > 0 && json.Unmarshal(c.ConfigCapabilities, &caps) == nil {
		if raw, ok := caps[defaultsKey]; ok {
			_ = json.Unmarshal(raw, &d)
		}
	}
	if d.Dedup == "" {
		d.Dedup = models.ContactImportDedupUpdate
	}
	if d.CategoryIDs == nil {
		d.CategoryIDs = []uuid.UUID{}
	}
	if d.SegmentIDs == nil {
		d.SegmentIDs = []uuid.UUID{}
	}
	return d
}

func statsOf(c *models.IntegrationConnection) models.ClayStats {
	var st models.ClayStats
	var caps map[string]json.RawMessage
	if len(c.ConfigCapabilities) > 0 && json.Unmarshal(c.ConfigCapabilities, &caps) == nil {
		if raw, ok := caps[repository.ClayStatsKey]; ok {
			_ = json.Unmarshal(raw, &st)
		}
	}
	return st
}

// Settings is everything the Clay page shows.
func (s *Service) Settings(ctx context.Context, orgID, connID uuid.UUID) (*models.ClaySettings, error) {
	c, err := s.connection(ctx, orgID, connID)
	if err != nil {
		return nil, err
	}
	cfg, _, err := s.Integrations.ConnectionConfig(ctx, orgID, connID)
	if err != nil {
		return nil, err
	}
	events, err := s.Integrations.ClaySubscriptions(ctx, orgID, connID)
	if err != nil {
		return nil, err
	}
	url, _ := cfg["webhook_url"].(string)
	token, _ := cfg["auth_token"].(string)
	out := &models.ClaySettings{
		ConnectionID:     c.ID,
		Label:            c.Label,
		Status:           c.Status,
		Health:           c.Health,
		InboundURLMinted: true,
		Defaults:         defaultsOf(c),
		Outbound: models.ClayOutbound{
			WebhookURLSet: strings.TrimSpace(url) != "",
			WebhookHost:   hostOf(url),
			AuthTokenSet:  strings.TrimSpace(token) != "",
			Events:        events,
		},
		Stats:           statsOf(c),
		AvailableEvents: integration.ClayEvents,
	}
	return out, nil
}

// UpdateSettings saves the inbound defaults, the outbound table and the events
// sent to it. Every part is optional.
func (s *Service) UpdateSettings(ctx context.Context, orgID, connID uuid.UUID, in models.ClaySettingsUpdate) (*models.ClaySettings, error) {
	if _, err := s.connection(ctx, orgID, connID); err != nil {
		return nil, err
	}
	if in.Defaults != nil {
		d := *in.Defaults
		if err := s.validateDefaults(ctx, orgID, &d); err != nil {
			return nil, err
		}
		if err := s.Integrations.SetConfigKey(ctx, orgID, connID, defaultsKey, d); err != nil {
			return nil, err
		}
	}
	patch := map[string]any{}
	if in.WebhookURL != nil {
		u := strings.TrimSpace(*in.WebhookURL)
		if u != "" && !strings.Contains(strings.ToLower(hostOf(u)), "clay") {
			return nil, errx.NewWithIdentifier(errx.BadRequest, "clay_webhook_url", "Paste the webhook URL from your Clay table's webhook source")
		}
		patch["webhook_url"] = u
	}
	if in.AuthToken != nil {
		t := strings.TrimSpace(*in.AuthToken)
		if len(t) > 512 {
			return nil, errx.New(errx.BadRequest, "the auth token is too long")
		}
		patch["auth_token"] = t
	}
	if len(patch) > 0 {
		if err := s.Integrations.MergeConnectionConfig(ctx, orgID, connID, patch); err != nil {
			if msg, ok := integration.PublicMessage(err); ok {
				return nil, errx.New(errx.BadRequest, msg)
			}
			return nil, err
		}
		if u, ok := patch["webhook_url"].(string); ok {
			_ = s.Integrations.MergeConnectionDisplay(ctx, connID, map[string]any{"webhook_host": hostOf(u)})
		}
	}
	if in.Events != nil {
		events := *in.Events
		if len(events) > 0 {
			cfg, _, err := s.Integrations.ConnectionConfig(ctx, orgID, connID)
			if err != nil {
				return nil, err
			}
			if u, _ := cfg["webhook_url"].(string); strings.TrimSpace(u) == "" {
				return nil, errx.NewWithIdentifier(errx.BadRequest, "clay_webhook_missing", "Add the Clay table's webhook URL before choosing events to send")
			}
		}
		if err := s.Integrations.SetClaySubscriptions(ctx, orgID, connID, events); err != nil {
			if msg, ok := integration.PublicMessage(err); ok {
				return nil, errx.New(errx.BadRequest, msg)
			}
			return nil, err
		}
	}
	return s.Settings(ctx, orgID, connID)
}

func (s *Service) validateDefaults(ctx context.Context, orgID uuid.UUID, d *models.ClayInboundDefaults) error {
	switch d.Dedup {
	case "":
		d.Dedup = models.ContactImportDedupUpdate
	case models.ContactImportDedupSkip, models.ContactImportDedupUpdate:
	default:
		return errx.New(errx.BadRequest, "dedup must be skip or update")
	}
	if len(d.CategoryIDs) > 50 || len(d.SegmentIDs) > 20 {
		return errx.New(errx.BadRequest, "at most 50 tags and 20 segments")
	}
	if d.CampaignID != nil {
		owned, err := s.Repo.OwnedCampaigns(ctx, orgID, []uuid.UUID{*d.CampaignID})
		if err != nil {
			return err
		}
		if !owned[*d.CampaignID] {
			return errx.New(errx.BadRequest, "The campaign to enroll into was not found")
		}
	}
	if len(d.CategoryIDs) > 0 {
		owned, err := s.Repo.OwnedCategories(ctx, orgID, d.CategoryIDs)
		if err != nil {
			return err
		}
		for _, id := range d.CategoryIDs {
			if !owned[id] {
				return errx.New(errx.BadRequest, "A tag to apply was not found")
			}
		}
	}
	if len(d.SegmentIDs) > 0 {
		owned, err := s.Repo.OwnedSegments(ctx, orgID, d.SegmentIDs)
		if err != nil {
			return err
		}
		for _, id := range d.SegmentIDs {
			if !owned[id] {
				return errx.New(errx.BadRequest, "A segment to add to was not found")
			}
		}
	}
	if d.CategoryIDs == nil {
		d.CategoryIDs = []uuid.UUID{}
	}
	if d.SegmentIDs == nil {
		d.SegmentIDs = []uuid.UUID{}
	}
	return nil
}

// Receive takes one request from a Clay HTTP API column.
func (s *Service) Receive(ctx context.Context, secret string, body []byte) (*models.ClayInboundResponse, error) {
	c, err := s.Integrations.ClayConnectionByInboundSecret(ctx, strings.TrimSpace(secret))
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, ErrUnknownURL
	}
	if c.Status == models.IntegrationStatusDisconnected || c.ConnectedByUserID == nil {
		return nil, ErrNotActive
	}
	if !s.allow(c.ID) {
		return nil, ErrRateLimited
	}
	raw, err := parseBody(body)
	if err != nil {
		return nil, err
	}
	resp, err := s.importRows(ctx, c, raw)
	delta := models.ClayStats{Received: len(raw)}
	if resp != nil {
		delta.Created, delta.Updated, delta.Skipped, delta.Failed = resp.Created, resp.Updated, resp.Skipped, resp.Failed
		for _, r := range resp.Results {
			if r.Status == "failed" && r.Reason != "" {
				delta.LastError = r.Reason
				break
			}
		}
	}
	var xe *errx.Error
	if err != nil && errors.As(err, &xe) {
		delta.Failed, delta.LastError = len(raw), xe.Message
	}
	_ = s.Repo.IncrementStats(context.WithoutCancel(ctx), c.ID, delta)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// importRows turns the rows into contacts, one import per target campaign.
func (s *Service) importRows(ctx context.Context, c *models.IntegrationConnection, raw []map[string]any) (*models.ClayInboundResponse, error) {
	defaults := defaultsOf(c)
	rows := make([]row, len(raw))
	var asked []uuid.UUID
	for i, m := range raw {
		rows[i] = normalizeRow(m)
		if rows[i].campaign != nil {
			asked = append(asked, *rows[i].campaign)
		}
	}
	owned, err := s.Repo.OwnedCampaigns(ctx, c.OrganizationID, asked)
	if err != nil {
		return nil, err
	}

	results := make([]models.ClayRowResult, len(rows))
	groups := map[string][]int{}
	var order []string
	for i, r := range rows {
		target := defaults.CampaignID
		switch {
		case r.campaignErr:
			results[i] = models.ClayRowResult{Status: "failed", Email: r.email, Reason: "campaign_id is not a valid id"}
			continue
		case r.campaign != nil && !owned[*r.campaign]:
			results[i] = models.ClayRowResult{Status: "failed", Email: r.email, Reason: "campaign not found"}
			continue
		case r.campaign != nil:
			target = r.campaign
		}
		key := ""
		if target != nil {
			key = target.String()
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], i)
	}

	cats := make([]string, 0, len(defaults.CategoryIDs))
	for _, id := range defaults.CategoryIDs {
		cats = append(cats, id.String())
	}
	segs := make([]string, 0, len(defaults.SegmentIDs))
	for _, id := range defaults.SegmentIDs {
		segs = append(segs, id.String())
	}
	subscribed := defaults.Subscribed
	actor := c.ConnectedByUserID.String()
	for _, key := range order {
		idx := groups[key]
		group := make([]row, len(idx))
		for j, i := range idx {
			group[j] = rows[i]
		}
		importRows, mapping := layout(group)
		var campaigns []string
		if key != "" {
			campaigns = []string{key}
		}
		opts := &models.ContactImportCommit{
			Mapping:             mapping,
			Dedup:               defaults.Dedup,
			CategoryIDs:         cats,
			CampaignIDs:         campaigns,
			SegmentIDs:          segs,
			SkipMissingSegments: true,
			SubscribedDefault:   &subscribed,
			Source:              models.ContactSourceClay,
			SourceDetail:        truncate("Clay: "+c.Label, 200),
		}
		sink := &collectSink{outcomes: map[int]models.ContactImportRowOutcome{}}
		if _, xerr := s.Contacts.RunImport(ctx, actor, c.OrganizationID, importRows, opts, sink, nil); xerr != nil {
			return nil, xerr
		}
		for j, i := range idx {
			o, ok := sink.outcomes[j+1]
			if !ok {
				results[i] = models.ClayRowResult{Status: "failed", Email: rows[i].email, Reason: "not processed"}
				continue
			}
			res := models.ClayRowResult{Status: rowStatus(o.Status), ContactID: o.ContactID, Email: o.Email, Reason: o.Reason}
			if res.Email == "" {
				res.Email = rows[i].email
			}
			if key != "" && o.ContactID != nil {
				id, _ := uuid.Parse(key)
				res.Campaign = &id
			}
			results[i] = res
		}
	}

	out := &models.ClayInboundResponse{Results: results, Received: len(results)}
	for _, r := range results {
		switch r.Status {
		case "created":
			out.Created++
		case "updated":
			out.Updated++
		case "skipped":
			out.Skipped++
		default:
			out.Failed++
		}
	}
	if len(results) == 1 {
		out.ClayRowResult = results[0]
	}
	return out, nil
}

func rowStatus(s string) string {
	switch s {
	case models.ContactImportRowImported:
		return "created"
	case models.ContactImportRowUpdated:
		return "updated"
	case models.ContactImportRowSkipped:
		return "skipped"
	}
	return "failed"
}

// layout lays rows out as importer cells: the fixed fields, then one column
// per custom key any row carries.
func layout(rows []row) ([]contact.ImportRow, []models.ContactImportColumnMapping) {
	fixed := []models.ContactImportColumnTarget{
		models.ContactImportTargetEmail,
		models.ContactImportTargetFirstName,
		models.ContactImportTargetLastName,
		models.ContactImportTargetCompany,
		models.ContactImportTargetPhone,
		models.ContactImportTargetSubscribed,
		models.ContactImportTargetCategories,
		models.ContactImportTargetVerificationStatus,
	}
	mapping := make([]models.ContactImportColumnMapping, 0, len(fixed)+8)
	for i, t := range fixed {
		mapping = append(mapping, models.ContactImportColumnMapping{Index: i, Target: t})
	}
	keySet := map[string]bool{}
	var keys []string
	for _, r := range rows {
		for k := range r.custom {
			if !keySet[k] && len(keys) < maxCustomKeys {
				keySet[k] = true
				keys = append(keys, k)
			}
		}
	}
	slices.Sort(keys)
	for j, k := range keys {
		mapping = append(mapping, models.ContactImportColumnMapping{Index: len(fixed) + j, Target: models.ContactImportTargetCustom, CustomKey: k})
	}
	out := make([]contact.ImportRow, len(rows))
	for i, r := range rows {
		cells := []string{r.email, r.first, r.last, r.company, r.phone, r.subscribed, strings.Join(r.categories, ","), r.verification}
		for _, k := range keys {
			cells = append(cells, r.custom[k])
		}
		out[i] = contact.ImportRow{Line: i + 1, Cells: cells}
	}
	return out, mapping
}

type collectSink struct {
	outcomes map[int]models.ContactImportRowOutcome
}

func (c *collectSink) Settle(_ context.Context, outcomes []models.ContactImportRowOutcome) error {
	for _, o := range outcomes {
		c.outcomes[o.Line] = o
	}
	return nil
}

func (c *collectSink) Cancelled(context.Context) bool { return false }
