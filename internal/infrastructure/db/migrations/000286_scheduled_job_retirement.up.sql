ALTER TABLE scheduled_job_runs ADD COLUMN retired_at TIMESTAMPTZ;

UPDATE scheduled_job_runs
SET retired_at = now()
WHERE name = 'fleet_quarantine' AND service = 'backend';
