-- Apollo and Clay as contact sources. Apollo lists come in through saved
-- import sources (once or on a schedule) and Apollo enrichment fills in
-- contacts already in Warmbly; Clay tables post rows to an inbound URL.

ALTER TABLE public.contacts DROP CONSTRAINT contacts_source_check;
ALTER TABLE public.contacts
    ADD CONSTRAINT contacts_source_check
    CHECK (source IN ('unknown', 'manual', 'campaign', 'import', 'sheet_sync', 'api', 'ai_assistant', 'form', 'automation', 'crm_sync', 'apollo', 'clay')) NOT VALID;

-- A saved Apollo list that feeds contacts in, once or on a schedule,
-- optionally straight into a Warmbly campaign.
CREATE TABLE apollo_import_sources (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id    uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    connection_id      uuid NOT NULL REFERENCES integration_connections (id) ON DELETE CASCADE,
    created_by_user_id uuid REFERENCES users (id) ON DELETE SET NULL,
    name               text NOT NULL DEFAULT '',
    list_id            text NOT NULL CHECK (list_id <> ''),
    list_name          text NOT NULL DEFAULT '',
    campaign_id        uuid REFERENCES campaigns (id) ON DELETE SET NULL,
    category_ids       uuid[] NOT NULL DEFAULT '{}',
    verified_only      boolean NOT NULL DEFAULT true,
    recurring          boolean NOT NULL DEFAULT false,
    enabled            boolean NOT NULL DEFAULT true,
    status             text NOT NULL DEFAULT 'idle' CHECK (status IN ('idle', 'running', 'error')),
    last_run_at        timestamptz,
    last_result        jsonb,
    last_error         text NOT NULL DEFAULT '',
    total_imported     integer NOT NULL DEFAULT 0,
    created_at         timestamptz NOT NULL DEFAULT NOW(),
    updated_at         timestamptz NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_apollo_import_sources_org ON apollo_import_sources (organization_id);
CREATE INDEX idx_apollo_import_sources_conn ON apollo_import_sources (connection_id);

-- Which Apollo contacts a source has already brought in, so a recurring run
-- only imports people new to the list.
CREATE TABLE apollo_import_members (
    source_id         uuid NOT NULL REFERENCES apollo_import_sources (id) ON DELETE CASCADE,
    apollo_contact_id text NOT NULL,
    contact_id        uuid REFERENCES contacts (id) ON DELETE SET NULL,
    first_seen_at     timestamptz NOT NULL DEFAULT NOW(),
    PRIMARY KEY (source_id, apollo_contact_id)
);

-- One request to enrich a set of Warmbly contacts through Apollo. Runs in the
-- background; the row is its progress and outcome.
CREATE TABLE apollo_enrichment_runs (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id      uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    connection_id        uuid NOT NULL REFERENCES integration_connections (id) ON DELETE CASCADE,
    requested_by_user_id uuid REFERENCES users (id) ON DELETE SET NULL,
    contact_ids          uuid[] NOT NULL,
    overwrite            boolean NOT NULL DEFAULT false,
    status               text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'done', 'error')),
    total                integer NOT NULL DEFAULT 0,
    processed            integer NOT NULL DEFAULT 0,
    enriched             integer NOT NULL DEFAULT 0,
    not_found            integer NOT NULL DEFAULT 0,
    failed               integer NOT NULL DEFAULT 0,
    credits_consumed     integer NOT NULL DEFAULT 0,
    last_error           text NOT NULL DEFAULT '',
    started_at           timestamptz,
    finished_at          timestamptz,
    created_at           timestamptz NOT NULL DEFAULT NOW(),
    updated_at           timestamptz NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_apollo_enrichment_runs_conn ON apollo_enrichment_runs (connection_id, created_at DESC);
CREATE INDEX idx_apollo_enrichment_runs_open ON apollo_enrichment_runs (status, updated_at) WHERE status IN ('queued', 'running');
