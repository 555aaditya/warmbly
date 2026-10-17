-- Alone in its file for CONCURRENTLY. Lets the body-text backfill read only the rows it fills.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_unibox_emails_missing_body
    ON public.unibox_emails (id) WHERE body_text = '';
