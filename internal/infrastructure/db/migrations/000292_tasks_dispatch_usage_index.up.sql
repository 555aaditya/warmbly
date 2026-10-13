CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tasks_dispatch_usage
    ON public.tasks (email_account_id, COALESCE(completed_at, updated_at) DESC NULLS LAST)
    INCLUDE (id, task_type)
    WHERE status IN ('active', 'completed')
      AND task_type IN ('warmup', 'campaign', 'email', 'placement')
      AND message_id <> '';
