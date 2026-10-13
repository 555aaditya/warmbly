CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_unibox_emails_folder_generation
    ON public.unibox_emails (user_id, email_id, folder_path, mailbox) INCLUDE (uid, id);
