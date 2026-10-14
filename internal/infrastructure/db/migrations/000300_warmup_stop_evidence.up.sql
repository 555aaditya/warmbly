ALTER TABLE email_accounts ADD COLUMN warmup_stopped_at timestamptz;
UPDATE email_accounts SET warmup_stopped_at=clock_timestamp()
WHERE warmup IS NOT NULL
  AND (status IN ('inactive','revoked') OR test_mode='off'
       OR test_mode='diagnostic' AND NOT test_send_enabled AND NOT test_receive_enabled);
CREATE INDEX email_accounts_warmup_stopped ON email_accounts(warmup_stopped_at) WHERE warmup_stopped_at IS NOT NULL;
CREATE INDEX warmup_tasks_target_account ON warmup_tasks(target_account_id) WHERE target_account_id IS NOT NULL;
