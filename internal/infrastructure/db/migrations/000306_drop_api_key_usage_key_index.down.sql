CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_api_key_usage_key
    ON public.api_key_usage_logs USING btree (api_key_id, created_at DESC);
