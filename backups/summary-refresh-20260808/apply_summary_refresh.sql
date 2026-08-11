\set ON_ERROR_STOP on

BEGIN;

CREATE TEMP TABLE summary_refresh_map (
    knowledge_id varchar(36) PRIMARY KEY,
    knowledge_base_id varchar(36) NOT NULL,
    tenant_id integer NOT NULL,
    summary_chunk_id varchar(36),
    new_summary text NOT NULL
);

CREATE TEMP TABLE summary_refresh_vector (
    knowledge_id varchar(36) PRIMARY KEY,
    dimension integer NOT NULL,
    embedding halfvec NOT NULL
);

\copy summary_refresh_map (knowledge_id, knowledge_base_id, tenant_id, summary_chunk_id, new_summary) FROM '/tmp/summary_map_stage.csv' CSV HEADER
\copy summary_refresh_vector (knowledge_id, dimension, embedding) FROM '/tmp/summary_vector_stage.csv' CSV HEADER

DO $$
DECLARE
    map_count integer;
    vector_count integer;
    target_count integer;
BEGIN
    SELECT count(*) INTO map_count FROM summary_refresh_map;
    SELECT count(*) INTO vector_count FROM summary_refresh_vector;
    SELECT count(*) INTO target_count
    FROM summary_refresh_map m
    JOIN knowledges k ON k.id = m.knowledge_id
    JOIN knowledge_bases kb ON kb.id = k.knowledge_base_id
    WHERE k.deleted_at IS NULL
      AND kb.deleted_at IS NULL
      AND kb.name ~ '^(0[1-9]|1[0-9]|2[0-3])-'
      AND k.knowledge_base_id = m.knowledge_base_id
      AND k.tenant_id = m.tenant_id;

    IF map_count <> 320 OR vector_count <> 320 OR target_count <> 320 THEN
        RAISE EXCEPTION 'preflight count mismatch: map=%, vectors=%, targets=%',
            map_count, vector_count, target_count;
    END IF;

    IF EXISTS (SELECT 1 FROM summary_refresh_map WHERE btrim(new_summary) = '') THEN
        RAISE EXCEPTION 'preflight failed: blank new summary';
    END IF;

    IF EXISTS (SELECT 1 FROM summary_refresh_vector WHERE dimension <> 3072) THEN
        RAISE EXCEPTION 'preflight failed: unexpected embedding dimension';
    END IF;
END $$;

UPDATE knowledges k
SET description = m.new_summary,
    summary_status = 'completed',
    updated_at = CURRENT_TIMESTAMP
FROM summary_refresh_map m
WHERE k.id = m.knowledge_id;

UPDATE chunks c
SET content = '# Summary' || E'\n' || m.new_summary,
    updated_at = CURRENT_TIMESTAMP
FROM summary_refresh_map m
WHERE c.id = NULLIF(m.summary_chunk_id, '')
  AND c.knowledge_id = m.knowledge_id
  AND c.chunk_type = 'summary'
  AND c.deleted_at IS NULL;

INSERT INTO chunks (
    id,
    tenant_id,
    knowledge_base_id,
    knowledge_id,
    content,
    chunk_index,
    is_enabled,
    start_at,
    end_at,
    chunk_type,
    parent_chunk_id,
    status,
    flags,
    content_hash,
    created_at,
    updated_at
)
SELECT
    uuid_generate_v4()::text,
    m.tenant_id,
    m.knowledge_base_id,
    m.knowledge_id,
    '# Summary' || E'\n' || m.new_summary,
    mx.max_chunk_index + 1,
    true,
    0,
    0,
    'summary',
    ft.id,
    0,
    1,
    '',
    CURRENT_TIMESTAMP,
    CURRENT_TIMESTAMP
FROM summary_refresh_map m
CROSS JOIN LATERAL (
    SELECT COALESCE(max(c.chunk_index), 0) AS max_chunk_index
    FROM chunks c
    WHERE c.knowledge_id = m.knowledge_id
      AND c.deleted_at IS NULL
) mx
CROSS JOIN LATERAL (
    SELECT c.id
    FROM chunks c
    WHERE c.knowledge_id = m.knowledge_id
      AND c.chunk_type = 'text'
      AND c.deleted_at IS NULL
    ORDER BY c.chunk_index
    LIMIT 1
) ft
WHERE NULLIF(m.summary_chunk_id, '') IS NULL;

UPDATE summary_refresh_map m
SET summary_chunk_id = c.id
FROM chunks c
WHERE c.knowledge_id = m.knowledge_id
  AND c.chunk_type = 'summary'
  AND c.deleted_at IS NULL;

DO $$
DECLARE
    chunk_count integer;
BEGIN
    SELECT count(*) INTO chunk_count
    FROM summary_refresh_map m
    JOIN chunks c ON c.id = m.summary_chunk_id
    WHERE c.knowledge_id = m.knowledge_id
      AND c.knowledge_base_id = m.knowledge_base_id
      AND c.chunk_type = 'summary'
      AND c.deleted_at IS NULL
      AND c.content = '# Summary' || E'\n' || m.new_summary;

    IF chunk_count <> 320 THEN
        RAISE EXCEPTION 'summary chunk validation failed: expected 320, found %', chunk_count;
    END IF;
END $$;

INSERT INTO embeddings (
    created_at,
    updated_at,
    source_id,
    source_type,
    chunk_id,
    knowledge_id,
    knowledge_base_id,
    content,
    dimension,
    embedding,
    is_enabled,
    tag_id
)
SELECT
    CURRENT_TIMESTAMP,
    CURRENT_TIMESTAMP,
    m.summary_chunk_id,
    0,
    m.summary_chunk_id,
    m.knowledge_id,
    m.knowledge_base_id,
    '# Summary' || E'\n' || m.new_summary,
    v.dimension,
    v.embedding,
    true,
    NULL
FROM summary_refresh_map m
JOIN summary_refresh_vector v ON v.knowledge_id = m.knowledge_id
ON CONFLICT (source_id, source_type) DO UPDATE
SET updated_at = EXCLUDED.updated_at,
    chunk_id = EXCLUDED.chunk_id,
    knowledge_id = EXCLUDED.knowledge_id,
    knowledge_base_id = EXCLUDED.knowledge_base_id,
    content = EXCLUDED.content,
    dimension = EXCLUDED.dimension,
    embedding = EXCLUDED.embedding,
    is_enabled = EXCLUDED.is_enabled,
    tag_id = EXCLUDED.tag_id;

DO $$
DECLARE
    knowledge_count integer;
    chunk_count integer;
    embedding_count integer;
BEGIN
    SELECT count(*) INTO knowledge_count
    FROM summary_refresh_map m
    JOIN knowledges k ON k.id = m.knowledge_id
    WHERE k.description = m.new_summary
      AND k.summary_status = 'completed';

    SELECT count(*) INTO chunk_count
    FROM summary_refresh_map m
    JOIN chunks c ON c.id = m.summary_chunk_id
    WHERE c.content = '# Summary' || E'\n' || m.new_summary
      AND c.chunk_type = 'summary'
      AND c.deleted_at IS NULL;

    SELECT count(*) INTO embedding_count
    FROM summary_refresh_map m
    JOIN summary_refresh_vector v ON v.knowledge_id = m.knowledge_id
    JOIN embeddings e ON e.source_id = m.summary_chunk_id AND e.source_type = 0
    WHERE e.chunk_id = m.summary_chunk_id
      AND e.knowledge_id = m.knowledge_id
      AND e.knowledge_base_id = m.knowledge_base_id
      AND e.content = '# Summary' || E'\n' || m.new_summary
      AND e.dimension = v.dimension;

    IF knowledge_count <> 320 OR chunk_count <> 320 OR embedding_count <> 320 THEN
        RAISE EXCEPTION 'post-update validation failed: knowledge=%, chunks=%, embeddings=%',
            knowledge_count, chunk_count, embedding_count;
    END IF;
END $$;

COMMIT;

SELECT
    count(*) AS updated_books,
    count(*) FILTER (WHERE c.chunk_type = 'summary' AND c.deleted_at IS NULL) AS summary_chunks,
    count(*) FILTER (WHERE e.dimension = 3072) AS summary_embeddings
FROM summary_refresh_map m
JOIN chunks c ON c.id = m.summary_chunk_id
JOIN embeddings e ON e.source_id = m.summary_chunk_id AND e.source_type = 0;
