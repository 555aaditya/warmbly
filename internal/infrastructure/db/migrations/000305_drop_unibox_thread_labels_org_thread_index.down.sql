CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_unibox_thread_labels_org_thread
    ON public.unibox_thread_labels USING btree (organization_id, thread_id);
