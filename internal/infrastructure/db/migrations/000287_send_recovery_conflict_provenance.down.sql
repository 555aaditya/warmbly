DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM send_recovery_resolutions) THEN
        RAISE EXCEPTION 'Recovery audit evidence remains; roll forward rather than deleting evidence to downgrade';
    END IF;
END $$;

DROP INDEX send_recovery_native_conflict;
DROP INDEX send_recovery_unknown_resolution;
CREATE UNIQUE INDEX send_recovery_unknown_resolution ON send_recovery_resolutions (recovery_task_id)
    WHERE previous_reason = 'unknown';
ALTER TABLE send_recovery_resolutions
    DROP CONSTRAINT send_recovery_automatic_conflict_check,
    DROP CONSTRAINT send_recovery_resolutions_conflicting_result_state_check,
    DROP CONSTRAINT send_recovery_resolutions_evidence_type_check,
    ADD CONSTRAINT send_recovery_resolutions_evidence_type_check CHECK (evidence_type IN (
        'authentication_repaired', 'operator_provider_confirmation',
        'operator_confirmed_sent', 'operator_confirmed_not_sent')),
    DROP COLUMN conflicting_result_state,
    DROP COLUMN recorded_locally_at,
    DROP COLUMN conflict_resolved_at;
