-- Alone in its file for CONCURRENTLY. Serves the placement half of the daily send count.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tasks_placement_by_account
    ON public.tasks (email_account_id) WHERE task_type = 'placement';
