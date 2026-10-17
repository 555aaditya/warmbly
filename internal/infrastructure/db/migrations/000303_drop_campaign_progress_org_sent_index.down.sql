CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_campaign_progress_org_sent
    ON public.campaign_contact_progress USING btree (campaign_id, sent_at DESC) WHERE (sent_at IS NOT NULL);
