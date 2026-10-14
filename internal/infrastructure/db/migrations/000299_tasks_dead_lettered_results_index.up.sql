CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tasks_dead_lettered_results
    ON public.tasks (created_at) WHERE status = 'dead_lettered' AND send_executor_result IS NOT NULL;
