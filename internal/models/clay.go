package models

import (
	"time"

	"github.com/google/uuid"
)

// ClayInboundDefaults decide where rows a Clay table sends land. Stored in the
// connection's config_capabilities; a row may name its own campaign.
type ClayInboundDefaults struct {
	CampaignID  *uuid.UUID                 `json:"campaign_id,omitempty"`
	CategoryIDs []uuid.UUID                `json:"category_ids"`
	SegmentIDs  []uuid.UUID                `json:"segment_ids"`
	Dedup       ContactImportDedupStrategy `json:"dedup"`
	Subscribed  bool                       `json:"subscribed"`
}

// ClayOutbound is the default Clay table Warmbly sends events to. The URL and
// the token are sealed in the connection's config; only their presence is read.
type ClayOutbound struct {
	WebhookURLSet bool     `json:"webhook_url_set"`
	WebhookHost   string   `json:"webhook_host,omitempty"`
	AuthTokenSet  bool     `json:"auth_token_set"`
	Events        []string `json:"events"`
}

// ClayStats count what the inbound URL has received, kept on the connection.
type ClayStats struct {
	Received       int        `json:"received"`
	Created        int        `json:"created"`
	Updated        int        `json:"updated"`
	Skipped        int        `json:"skipped"`
	Failed         int        `json:"failed"`
	LastReceivedAt *time.Time `json:"last_received_at,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
}

// ClaySettings is everything the Clay page edits and shows.
type ClaySettings struct {
	ConnectionID     uuid.UUID           `json:"connection_id"`
	Label            string              `json:"label"`
	Status           IntegrationStatus   `json:"status"`
	Health           string              `json:"health"`
	InboundURLMinted bool                `json:"inbound_url_minted"`
	Defaults         ClayInboundDefaults `json:"defaults"`
	Outbound         ClayOutbound        `json:"outbound"`
	Stats            ClayStats           `json:"stats"`
	AvailableEvents  []string            `json:"available_events"`
}

// ClaySettingsUpdate edits the Clay page. Nil leaves a value alone; an empty
// WebhookURL or AuthToken clears it.
type ClaySettingsUpdate struct {
	Defaults   *ClayInboundDefaults `json:"defaults"`
	WebhookURL *string              `json:"webhook_url"`
	AuthToken  *string              `json:"auth_token"`
	Events     *[]string            `json:"events"`
}

// ClayRowResult is what became of one row a Clay table sent. Clay writes the
// response into the column, so the shape is flat and readable in a cell.
type ClayRowResult struct {
	Status    string     `json:"status"`
	ContactID *uuid.UUID `json:"contact_id,omitempty"`
	Email     string     `json:"email,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	Campaign  *uuid.UUID `json:"campaign_id,omitempty"`
}

// ClayInboundResponse answers a Clay HTTP API call. A single-row request gets
// the row's fields at the top level as well, so a Clay column can read
// "status" without indexing into an array.
type ClayInboundResponse struct {
	ClayRowResult
	Results  []ClayRowResult `json:"results"`
	Received int             `json:"received"`
	Created  int             `json:"created"`
	Updated  int             `json:"updated"`
	Skipped  int             `json:"skipped"`
	Failed   int             `json:"failed"`
}
