-- What each spam rescue came to, as the recipient's worker reported it.
ALTER TABLE warmup_received
    ADD COLUMN rescue_outcome text CHECK (rescue_outcome IN ('moved','already_out','gone','failed')),
    ADD COLUMN rescue_reported_at timestamptz;

ALTER TABLE warmup_placement_daily
    ADD COLUMN rescue_confirmed integer NOT NULL DEFAULT 0 CHECK (rescue_confirmed >= 0),
    ADD COLUMN rescue_missing integer NOT NULL DEFAULT 0 CHECK (rescue_missing >= 0),
    ADD COLUMN rescue_failed integer NOT NULL DEFAULT 0 CHECK (rescue_failed >= 0);
