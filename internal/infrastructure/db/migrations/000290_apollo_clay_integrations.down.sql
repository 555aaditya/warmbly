DROP TABLE IF EXISTS apollo_enrichment_runs;
DROP TABLE IF EXISTS apollo_import_members;
DROP TABLE IF EXISTS apollo_import_sources;

DELETE FROM integration_connections WHERE provider IN ('apollo', 'clay');

UPDATE public.contacts SET source = 'crm_sync' WHERE source = 'apollo';
UPDATE public.contacts SET source = 'api' WHERE source = 'clay';
ALTER TABLE public.contacts DROP CONSTRAINT contacts_source_check;
ALTER TABLE public.contacts
    ADD CONSTRAINT contacts_source_check
    CHECK (source IN ('unknown', 'manual', 'campaign', 'import', 'sheet_sync', 'api', 'ai_assistant', 'form', 'automation', 'crm_sync')) NOT VALID;
