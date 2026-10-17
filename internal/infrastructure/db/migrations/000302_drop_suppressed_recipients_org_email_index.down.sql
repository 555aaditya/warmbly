CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_suppressed_recipients_org_email
    ON public.suppressed_recipients USING btree (organization_id, email);
