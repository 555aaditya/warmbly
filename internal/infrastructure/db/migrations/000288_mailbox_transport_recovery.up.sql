BEGIN;
ALTER TABLE email_accounts ADD COLUMN transport_generation uuid NOT NULL DEFAULT gen_random_uuid();
ALTER TABLE email_account_errors
    ADD COLUMN operation text NOT NULL DEFAULT 'unknown'
        CHECK (operation IN ('unknown','sync','send')),
    ADD COLUMN observation_source text NOT NULL DEFAULT 'unknown'
        CHECK (observation_source IN ('unknown','worker','receipt')),
    ADD COLUMN received_at timestamptz;

-- No inferred operation or historical clock backfill.
DROP INDEX idx_email_account_errors_one_unresolved_per_code;
CREATE UNIQUE INDEX idx_email_account_errors_one_unresolved_per_operation
    ON email_account_errors(email_account_id,error_code,operation)
    WHERE resolved_at IS NULL AND observation_source IN ('worker','receipt');

CREATE TABLE email_account_transport_recovery (
    email_account_id uuid NOT NULL REFERENCES email_accounts(id) ON DELETE CASCADE,
    operation text NOT NULL CHECK (operation IN ('sync','send')),
    recovered_at timestamptz NOT NULL,
    source_task_id uuid REFERENCES tasks(id) ON DELETE SET NULL,
    PRIMARY KEY(email_account_id,operation)
);
COMMIT;
