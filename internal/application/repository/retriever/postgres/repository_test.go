package postgres

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	postgresdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestKeywordsRetrieveUsesPushdownCompatiblePredicates(t *testing.T) {
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	db, err := gorm.Open(postgresdriver.New(postgresdriver.Config{Conn: sqlDB}), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)

	queryPattern := regexp.QuoteMeta(
		`SELECT paradedb.score(id) as score,"id","content","source_id","source_type","chunk_id","knowledge_id","knowledge_base_id","tag_id" FROM "embeddings" WHERE "knowledge_base_id" IN ($1,$2) AND "knowledge_id" = $3 AND "tag_id" = $4 AND content ||| $5 AND ((is_enabled IS NULL OR is_enabled = $6)) ORDER BY "score" DESC LIMIT $7`,
	)
	mock.ExpectQuery(queryPattern).
		WithArgs("kb-10", "kb-23", "doc-1", "tag-1", "双套环缝线 示意图", true, int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{
			"score", "id", "content", "source_id", "source_type", "chunk_id",
			"knowledge_id", "knowledge_base_id", "tag_id",
		}))

	repo := &pgRepository{db: db}
	results, err := repo.KeywordsRetrieve(context.Background(), types.RetrieveParams{
		Query:            "双套环缝线 示意图",
		TopK:             100,
		KnowledgeBaseIDs: []string{"kb-10", "kb-23"},
		KnowledgeIDs:     []string{"doc-1"},
		TagIDs:           []string{"tag-1"},
	})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Empty(t, results[0].Results)
	require.NoError(t, mock.ExpectationsWereMet())
}
