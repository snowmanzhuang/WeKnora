-- Rollback migration 000075.
--
-- A rollback is normally run during a maintenance window. PostgreSQL does not
-- allow DROP INDEX CONCURRENTLY inside the migration transaction/DO block, so
-- the standard rollback remains a regular DROP INDEX.
DROP INDEX IF EXISTS public.embeddings_embedding_idx_3072;
