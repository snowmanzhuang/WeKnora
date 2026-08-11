-- Migration 000075: HNSW index for 3072-dimensional halfvec embeddings.
--
-- The PostgreSQL retriever orders by the exact expression below:
--
--   embedding::halfvec(3072) <=> $1::halfvec(3072)
--
-- Keeping the expression and cosine operator class identical is required for
-- PostgreSQL to use this partial HNSW index. The partial predicate also keeps
-- embeddings with other dimensions out of this graph.
--
-- IMPORTANT FOR AN EXISTING LARGE TABLE
-- --------------------------------------
-- CREATE INDEX in a golang-migrate transaction cannot use CONCURRENTLY. This
-- migration therefore creates the index only when embeddings is empty. If the
-- table already contains any rows, it fails fast with an actionable message
-- instead of starting a blocking production build during application startup.
--
-- Before restarting/upgrading a production deployment, run:
--
--   psql "$DATABASE_URL" -f scripts/postgres/create_embeddings_hnsw_3072.psql
--
-- That script builds the same index concurrently. This migration then sees
-- the valid index and becomes a no-op. Fresh/empty databases can safely let
-- this migration create it directly.

DO $$
DECLARE
    valid_3072_index_exists boolean;
BEGIN
    IF to_regclass('public.embeddings') IS NULL THEN
        RAISE NOTICE '[Migration 000075] embeddings table does not exist; skipping';
        RETURN;
    END IF;

    SELECT EXISTS (
        SELECT 1
        FROM pg_index AS i
        WHERE i.indrelid = 'public.embeddings'::regclass
          AND i.indisvalid
          AND i.indisready
          AND pg_get_indexdef(i.indexrelid) LIKE '%embedding)::halfvec(3072)%'
          AND pg_get_indexdef(i.indexrelid) LIKE '%halfvec_cosine_ops%'
          AND pg_get_expr(i.indpred, i.indrelid) = '(dimension = 3072)'
    )
    INTO valid_3072_index_exists;

    IF valid_3072_index_exists THEN
        RAISE NOTICE '[Migration 000075] valid 3072-dimensional HNSW index already exists';
        RETURN;
    END IF;

    -- A failed CREATE INDEX CONCURRENTLY can leave an invalid relation with
    -- this name. Do not hide or overwrite that state inside a migration.
    -- The production helper removes only such invalid leftovers concurrently.
    IF to_regclass('public.embeddings_embedding_idx_3072') IS NOT NULL THEN
        RAISE EXCEPTION
            '[Migration 000075] index name embeddings_embedding_idx_3072 exists but is not a valid matching index; run scripts/postgres/create_embeddings_hnsw_3072.psql first';
    END IF;

    -- Never surprise an existing deployment with a long, non-concurrent HNSW
    -- build at application startup. EXISTS stops at the first visible row.
    IF EXISTS (SELECT 1 FROM public.embeddings LIMIT 1) THEN
        RAISE EXCEPTION
            '[Migration 000075] embeddings contains data but no valid 3072-dimensional HNSW index; keep WeKnora online and run scripts/postgres/create_embeddings_hnsw_3072.psql before restarting';
    END IF;

    CREATE INDEX embeddings_embedding_idx_3072 ON public.embeddings
        USING hnsw (((embedding)::halfvec(3072)) halfvec_cosine_ops)
        WITH (m = 16, ef_construction = 64)
        WHERE (dimension = 3072);

    RAISE NOTICE '[Migration 000075] created HNSW index for dimension 3072';
END $$;
