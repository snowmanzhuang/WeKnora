-- Restore the pre-000076 BM25 index layout.

DO $migration$
BEGIN
    IF current_setting('app.skip_embedding', true) = 'true' THEN
        RAISE NOTICE 'Skipping embeddings BM25 filter-pushdown rollback (app.skip_embedding=true)';
        RETURN;
    END IF;

    DROP INDEX IF EXISTS public.embeddings_search_idx;

    CREATE INDEX embeddings_search_idx ON public.embeddings
    USING bm25 (id, knowledge_base_id, content, knowledge_id, chunk_id)
    WITH (
        key_field = 'id',
        text_fields = '{
            "content": {
                "tokenizer": {"type": "chinese_lindera"}
            }
        }'
    );

    RAISE NOTICE '[Migration 000076] Restored legacy embeddings_search_idx';
END
$migration$;
