-- Covered by the primary key (user_id, email_id, message_id).
DROP INDEX CONCURRENTLY IF EXISTS public.idx_email_message_map_user_email;
