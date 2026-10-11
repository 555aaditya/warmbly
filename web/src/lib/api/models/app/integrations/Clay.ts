// Mirror of models/clay.go.

import type { IntegrationHealth, IntegrationStatus } from "./Integration";

export type ClayDedup = "skip" | "update";

export interface ClayInboundDefaults {
    campaign_id?: string | null;
    category_ids: string[];
    segment_ids: string[];
    dedup: ClayDedup;
    subscribed: boolean;
}

export interface ClayOutbound {
    webhook_url_set: boolean;
    webhook_host?: string;
    auth_token_set: boolean;
    events: string[];
}

export interface ClayStats {
    received: number;
    created: number;
    updated: number;
    skipped: number;
    failed: number;
    last_received_at?: string;
    last_error?: string;
}

export interface ClaySettings {
    connection_id: string;
    label: string;
    status: IntegrationStatus;
    health: IntegrationHealth;
    inbound_url_minted: boolean;
    defaults: ClayInboundDefaults;
    outbound: ClayOutbound;
    stats: ClayStats;
    available_events: string[];
}

// Absent leaves a value alone; an empty webhook_url or auth_token clears it.
export interface ClaySettingsUpdate {
    defaults?: ClayInboundDefaults;
    webhook_url?: string;
    auth_token?: string;
    events?: string[];
}
