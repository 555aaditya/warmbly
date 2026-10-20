DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM tasks WHERE warmup_send_retired_at IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot remove permanent warmup send retirement evidence; retain the schema and roll forward';
    END IF;
END $$;

DROP INDEX tasks_retirable_unknown_warmup;
ALTER TABLE tasks DROP CONSTRAINT tasks_retired_warmup_send_check,
    DROP COLUMN warmup_send_retired_at;
