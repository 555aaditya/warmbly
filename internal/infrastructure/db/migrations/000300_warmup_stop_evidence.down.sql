DROP INDEX warmup_tasks_target_account;
DROP INDEX email_accounts_warmup_stopped;
ALTER TABLE email_accounts DROP COLUMN warmup_stopped_at;
