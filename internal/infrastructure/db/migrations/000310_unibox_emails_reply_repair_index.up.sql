-- Alone in its file for CONCURRENTLY. Same predicate as the campaign reply repair scan.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_unibox_emails_reply_repair
    ON public.unibox_emails (id) INCLUDE (created_at)
    WHERE campaign_reply_processed_at IS NULL
      AND folder NOT IN ('sent', 'drafts') AND provider_folder NOT IN ('sent', 'drafts');
