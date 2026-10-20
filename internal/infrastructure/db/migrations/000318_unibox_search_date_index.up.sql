CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_unibox_emails_date
    ON public.unibox_emails (internal_date DESC, id DESC);
