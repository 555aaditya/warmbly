ALTER TABLE tasks ADD COLUMN warmup_send_retired_at timestamptz,
    ADD CONSTRAINT tasks_retired_warmup_send_check CHECK (
        warmup_send_retired_at IS NULL OR (
            task_type='warmup' AND status='cancelled'
            AND send_result_state IS NOT DISTINCT FROM 'unknown'
            AND send_result_applied_at IS NULL AND send_released_at IS NULL));

CREATE INDEX tasks_retirable_unknown_warmup ON tasks (COALESCE(send_reserved_at,completed_at,updated_at),id)
    WHERE task_type='warmup' AND send_result_state='unknown'
      AND warmup_send_retired_at IS NULL AND send_result_applied_at IS NULL;
