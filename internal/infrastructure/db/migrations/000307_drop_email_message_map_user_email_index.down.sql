CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_email_message_map_user_email
    ON public.email_message_map USING btree (user_id, email_id);
