ALTER TABLE email_accounts ADD COLUMN warmup_stopped_at timestamptz;
CREATE INDEX email_accounts_warmup_stopped ON email_accounts(warmup_stopped_at) WHERE warmup_stopped_at IS NOT NULL;
CREATE INDEX warmup_tasks_target_account ON warmup_tasks(target_account_id) WHERE target_account_id IS NOT NULL;
