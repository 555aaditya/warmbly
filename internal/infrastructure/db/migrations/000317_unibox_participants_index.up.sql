CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_unibox_emails_participants_trgm
    ON public.unibox_emails USING gin (public.unibox_participants(from_addr, to_addr, cc) gin_trgm_ops);
