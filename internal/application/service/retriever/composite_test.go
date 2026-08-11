package retriever

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// blockingRetrieveEngine lets the test observe both retrieval stages reaching
// the repository before either is allowed to finish. This verifies real
// overlap without relying on fragile wall-clock assertions.
type blockingRetrieveEngine struct {
	started chan types.RetrieverType
	release <-chan struct{}
}

func (e *blockingRetrieveEngine) EngineType() types.RetrieverEngineType {
	return types.PostgresRetrieverEngineType
}

func (e *blockingRetrieveEngine) Retrieve(
	ctx context.Context, params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	e.started <- params.RetrieverType
	select {
	case <-e.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return []*types.RetrieveResult{{
		RetrieverEngineType: types.PostgresRetrieverEngineType,
		RetrieverType:       params.RetrieverType,
		Results: []*types.IndexWithScore{{
			ChunkID: string(params.RetrieverType),
		}},
	}}, nil
}

func (e *blockingRetrieveEngine) Index(
	context.Context, embedding.Embedder, *types.IndexInfo, []types.RetrieverType,
) error {
	return nil
}

func (e *blockingRetrieveEngine) BatchIndex(
	context.Context, embedding.Embedder, []*types.IndexInfo, []types.RetrieverType,
) error {
	return nil
}

func (e *blockingRetrieveEngine) DeleteByChunkIDList(context.Context, []string, int, string) error {
	return nil
}

func (e *blockingRetrieveEngine) DeleteBySourceIDList(context.Context, []string, int, string) error {
	return nil
}

func (e *blockingRetrieveEngine) DeleteByKnowledgeIDList(context.Context, []string, int, string) error {
	return nil
}

func (e *blockingRetrieveEngine) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType, types.KeywordsRetrieverType}
}

func (e *blockingRetrieveEngine) EstimateStorageSize(
	context.Context, embedding.Embedder, []*types.IndexInfo, []types.RetrieverType,
) int64 {
	return 0
}

func (e *blockingRetrieveEngine) CopyIndices(
	context.Context, string, map[string]string, map[string]string, string, int, string,
) error {
	return nil
}

func (e *blockingRetrieveEngine) BatchUpdateChunkEnabledStatus(context.Context, map[string]bool) error {
	return nil
}

func (e *blockingRetrieveEngine) BatchUpdateChunkTagID(context.Context, map[string]string) error {
	return nil
}

func TestCompositeRetrieveRunsVectorAndKeywordStagesConcurrently(t *testing.T) {
	started := make(chan types.RetrieverType, 2)
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	engine := &blockingRetrieveEngine{started: started, release: release}
	composite := &CompositeRetrieveEngine{engineInfos: []*engineInfo{{
		retrieveEngine: engine,
		retrieverType:  engine.Support(),
	}}}

	var (
		results []*types.RetrieveResult
		err     error
		wg      sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		results, err = composite.Retrieve(ctx, []types.RetrieveParams{
			{RetrieverType: types.VectorRetrieverType},
			{RetrieverType: types.KeywordsRetrieverType},
		})
	}()

	seen := make(map[types.RetrieverType]bool, 2)
	for range 2 {
		select {
		case retrieverType := <-started:
			seen[retrieverType] = true
		case <-ctx.Done():
			t.Fatal("vector and keyword retrieval did not overlap")
		}
	}
	require.True(t, seen[types.VectorRetrieverType])
	require.True(t, seen[types.KeywordsRetrieverType])

	close(release)
	wg.Wait()
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, 2, retrieveHitCount(results))
}

func TestRetrieveHitCountIgnoresNilResultSets(t *testing.T) {
	require.Equal(t, 2, retrieveHitCount([]*types.RetrieveResult{
		nil,
		{Results: []*types.IndexWithScore{{}, {}}},
	}))
}
