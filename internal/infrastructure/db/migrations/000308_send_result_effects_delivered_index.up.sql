-- Alone in its file for CONCURRENTLY. Serves the seven-day cleanup of delivered effects.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_send_result_effects_delivered
    ON public.send_result_effects (delivered_at) WHERE delivered_at IS NOT NULL;
