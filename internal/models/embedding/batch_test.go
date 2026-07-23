package embedding

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/panjf2000/ants/v2"
)

type retryBatchEmbedder struct {
	mu        sync.Mutex
	calls     int
	failures  int
	shortData bool
	pooler    EmbedderPooler
}

func (e *retryBatchEmbedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{1}, nil
}

func (e *retryBatchEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	if e.calls <= e.failures {
		if e.shortData {
			return nil, nil
		}
		return nil, errors.New("temporary provider failure")
	}
	results := make([][]float32, len(texts))
	for i := range texts {
		results[i] = []float32{float32(i + 1)}
	}
	return results, nil
}

func (e *retryBatchEmbedder) BatchEmbedWithPool(
	ctx context.Context, model Embedder, texts []string,
) ([][]float32, error) {
	return e.pooler.BatchEmbedWithPool(ctx, model, texts)
}

func (e *retryBatchEmbedder) GetModelName() string { return "retry-test" }
func (e *retryBatchEmbedder) GetDimensions() int   { return 1 }
func (e *retryBatchEmbedder) GetModelID() string   { return "retry-test" }

func TestBatchEmbedWithPoolRetriesOnlyMalformedSubBatch(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "2")
	pool, err := ants.NewPool(1)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	defer pool.Release()

	model := &retryBatchEmbedder{failures: 1, shortData: true}
	model.pooler = NewBatchEmbedder(pool)

	done := make(chan struct{})
	var got [][]float32
	var embedErr error
	go func() {
		defer close(done)
		got, embedErr = model.BatchEmbedWithPool(
			context.Background(), model, []string{"first", "second"})
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("BatchEmbedWithPool deadlocked after a malformed provider response")
	}
	if embedErr != nil {
		t.Fatalf("BatchEmbedWithPool: %v", embedErr)
	}
	if model.calls != 2 {
		t.Fatalf("provider calls = %d, want one failed call plus one local retry", model.calls)
	}
	if len(got) != 2 || len(got[0]) == 0 || len(got[1]) == 0 {
		t.Fatalf("unexpected embeddings: %v", got)
	}
}

func TestBatchEmbedWithPoolReturnsErrorAndKeepsPoolUsable(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "1")
	pool, err := ants.NewPool(1)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	defer pool.Release()

	broken := &retryBatchEmbedder{failures: batchEmbedRetryAttempts, shortData: true}
	broken.pooler = NewBatchEmbedder(pool)

	_, err = broken.BatchEmbedWithPool(context.Background(), broken, []string{"broken"})
	if !errors.Is(err, ErrEmbeddingSubBatchRetriesExhausted) {
		t.Fatalf("BatchEmbedWithPool error = %v, want exhausted retry error", err)
	}

	healthy := &retryBatchEmbedder{}
	healthy.pooler = NewBatchEmbedder(pool)
	done := make(chan error, 1)
	go func() {
		_, callErr := healthy.BatchEmbedWithPool(context.Background(), healthy, []string{"healthy"})
		done <- callErr
	}()
	select {
	case callErr := <-done:
		if callErr != nil {
			t.Fatalf("reusing pool after malformed response: %v", callErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker pool remained blocked after malformed response")
	}
}
