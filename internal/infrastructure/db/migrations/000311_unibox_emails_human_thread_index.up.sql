-- Alone in its file for CONCURRENTLY. Serves the person-written half of the automated-conversation test.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_unibox_emails_human_thread
    ON public.unibox_emails (thread_id) INCLUDE (email_id)
    WHERE NOT automated AND folder NOT IN ('sent', 'drafts', 'spam', 'trash');
