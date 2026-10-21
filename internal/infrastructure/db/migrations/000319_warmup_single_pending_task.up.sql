-- One pending warmup task per mailbox: keep its free chain task when it has one, else its newest reply, and cancel the rest.
WITH ranked AS (
    SELECT t.id,
           row_number() OVER (
               PARTITION BY t.email_account_id
               ORDER BY (w.task_id IS NOT NULL AND w.target_account_id IS NULL AND w.parent_task_id IS NULL) DESC,
                        t.created_at DESC, t.id
           ) AS rn
    FROM tasks t
    LEFT JOIN warmup_tasks w ON w.task_id = t.id
    WHERE t.task_type = 'warmup' AND t.status = 'pending'
)
UPDATE tasks t
SET status = 'cancelled', updated_at = NOW()
FROM ranked r
WHERE r.id = t.id AND r.rn > 1;
