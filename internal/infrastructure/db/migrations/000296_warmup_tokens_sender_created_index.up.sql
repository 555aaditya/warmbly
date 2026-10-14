CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_warmup_tokens_sender_created
    ON public.warmup_tokens (sender_account_id, created_at);
