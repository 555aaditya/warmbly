-- Alone in its file for CONCURRENTLY. Serves the unread badge and unread counts.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_unibox_emails_inbox_unseen
    ON public.unibox_emails (email_id) INCLUDE (thread_id, user_id, automated)
    WHERE NOT seen AND folder = 'inbox';
