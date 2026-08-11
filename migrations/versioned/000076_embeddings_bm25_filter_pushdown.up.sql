-- Migration 000076: Make embeddings metadata filters part of the ParadeDB scan.
--
-- ParadeDB can only push ordinary PostgreSQL metadata predicates into a BM25
-- scan when the fields are present in that BM25 index. Text equality/set
-- filters additionally require the literal tokenizer. The previous index used
-- the default word tokenizer for IDs and did not index is_enabled/tag_id, so
-- knowledge-base and enabled-state restrictions were applied as heap filters.

DO $migration$
BEGIN
    IF current_setting('app.skip_embedding', true) = 'true' THEN
        RAISE NOTICE 'Skipping embeddings BM25 filter-pushdown migration (app.skip_embedding=true)';
        RETURN;
    END IF;

    -- DROP + CREATE are deliberately in one DO statement/transaction: if the
    -- replacement build fails, PostgreSQL restores the previous index.
    DROP INDEX IF EXISTS public.embeddings_search_idx;

    CREATE INDEX embeddings_search_idx ON public.embeddings
    USING bm25 (
        id,
        (knowledge_base_id::pdb.literal),
        content,
        (knowledge_id::pdb.literal),
        (chunk_id::pdb.literal),
        (tag_id::pdb.literal),
        is_enabled
    )
    WITH (
        key_field = 'id',
        text_fields = '{
            "content": {
                "tokenizer": {"type": "chinese_lindera"}
            }
        }'
    );

    RAISE NOTICE '[Migration 000076] Rebuilt embeddings_search_idx with pushdown-capable metadata fields';
END
$migration$;
