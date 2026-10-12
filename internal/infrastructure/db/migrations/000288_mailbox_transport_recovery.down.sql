-- Old uniqueness cannot represent multiple unresolved native or historical reports.
-- Refuse rather than delete history, invent resolution, or erase task context.
DO $$
BEGIN
CREATE UNIQUE INDEX idx_email_account_errors_one_unresolved_per_code
    ON email_account_errors(email_account_id,error_code)
    WHERE resolved_at IS NULL;
DROP INDEX idx_email_account_errors_one_unresolved_per_operation;
DROP TABLE email_account_transport_recovery;
ALTER TABLE email_accounts DROP COLUMN transport_generation;
ALTER TABLE email_account_errors
    DROP COLUMN operation,
    DROP COLUMN observation_source,
    DROP COLUMN received_at;
END
$$;
