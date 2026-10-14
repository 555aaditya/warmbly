CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_warmup_received_account_created
    ON public.warmup_received (email_account_id, created_at);
