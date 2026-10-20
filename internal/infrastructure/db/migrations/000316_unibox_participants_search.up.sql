CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE FUNCTION public.unibox_participants(from_addr text[], to_addr text[], cc text[])
RETURNS text
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
SET search_path = pg_catalog, pg_temp
AS $$
    SELECT array_to_string(COALESCE(from_addr, '{}'::text[]) || COALESCE(to_addr, '{}'::text[]) || COALESCE(cc, '{}'::text[]), E'\n')
$$;
