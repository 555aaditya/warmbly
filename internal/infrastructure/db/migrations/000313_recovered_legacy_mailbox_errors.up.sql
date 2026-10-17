BEGIN;

UPDATE email_account_errors report
SET resolved_at=recovery.recovered_at,resolved_by=report.operation || ' recovered'
FROM email_account_transport_recovery recovery
WHERE report.resolved_at IS NULL AND report.observation_source IN ('worker','receipt')
    AND recovery.email_account_id=report.email_account_id AND recovery.operation=report.operation
    AND report.created_at < recovery.recovered_at - INTERVAL '1 second'
    AND report.error_code IN ('SENDING_TOO_FAST','QUOTA_EXCEEDED');

-- Legacy failures have no reliable operation; require both local recovery clocks.
UPDATE email_account_errors report
SET resolved_at=GREATEST(sync.recovered_at,send.recovered_at),resolved_by='unknown recovered'
FROM email_account_transport_recovery sync, email_account_transport_recovery send
WHERE (report.operation='unknown' OR report.observation_source='unknown') AND report.resolved_at IS NULL
    AND sync.email_account_id=report.email_account_id AND sync.operation='sync'
    AND send.email_account_id=report.email_account_id AND send.operation='send'
    AND report.created_at > to_timestamp(0)
    AND report.created_at < LEAST(sync.recovered_at,send.recovered_at) - INTERVAL '1 second'
    AND report.error_code IN ('SERVER_UNREACHABLE','CONNECTION_LOST','RESOURCE_NOT_FOUND','IMAP_UNKNOWN','SENDING_TOO_FAST','QUOTA_EXCEEDED');

COMMIT;
