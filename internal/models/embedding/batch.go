package embedding

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/utils"
	"github.com/panjf2000/ants/v2"
)

const (
	batchEmbedRetryAttempts  = 4
	batchEmbedRetryBaseDelay = 200 * time.Millisecond
)

// ErrEmbeddingSubBatchRetriesExhausted marks a provider failure that already
// consumed the local retry budget for one small sub-batch. Callers must not
// retry the entire document in response, because doing so would discard every
// successful in-memory vector and repeat thousands of paid provider calls.
var ErrEmbeddingSubBatchRetriesExhausted = errors.New("embedding sub-batch retries exhausted")

type batchEmbedder struct {
	pool *ants.Pool
}

func NewBatchEmbedder(pool *ants.Pool) EmbedderPooler {
	return &batchEmbedder{pool: pool}
}

type textEmbedding struct {
	text    string
	results []float32
}

func (e *batchEmbedder) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	// Create goroutine pool for concurrent processing of document chunks
	var wg sync.WaitGroup
	var failed atomic.Bool
	var firstErrOnce sync.Once
	var firstErr error
	batchSizeStr := os.Getenv("BATCH_EMBED_SIZE")
	if batchSizeStr == "" {
		batchSizeStr = "5"
	}
	batchSize, err := strconv.Atoi(batchSizeStr)
	if err != nil {
		return nil, err
	}
	if batchSize <= 0 {
		return nil, fmt.Errorf("BATCH_EMBED_SIZE must be positive, got %d", batchSize)
	}
	if e == nil || e.pool == nil {
		return nil, fmt.Errorf("embedding worker pool is not configured")
	}
	textEmbeddings := utils.MapSlice(texts, func(text string) *textEmbedding {
		return &textEmbedding{text: text}
	})

	recordFirstErr := func(err error) {
		if err == nil {
			return
		}
		firstErrOnce.Do(func() {
			firstErr = err
			failed.Store(true)
		})
	}

	embedChunk := func(chunk []*textEmbedding) ([][]float32, error) {
		inputs := utils.MapSlice(chunk, func(text *textEmbedding) string {
			return text.text
		})
		delay := batchEmbedRetryBaseDelay
		var lastErr error
		for attempt := 1; attempt <= batchEmbedRetryAttempts; attempt++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			embeddings, err := model.BatchEmbed(ctx, inputs)
			if err == nil {
				err = validateBatchEmbeddingResult(embeddings, len(inputs))
			}
			if err == nil {
				return embeddings, nil
			}
			lastErr = err

			if attempt == batchEmbedRetryAttempts {
				break
			}
			logger.Warnf(ctx, "embedding sub-batch attempt %d/%d failed; retrying locally: %v",
				attempt, batchEmbedRetryAttempts, err)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			delay *= 2
		}
		return nil, fmt.Errorf("%w after %d attempts: %v",
			ErrEmbeddingSubBatchRetriesExhausted, batchEmbedRetryAttempts, lastErr)
	}

	// Function to process each document chunk
	processChunk := func(chunk []*textEmbedding) func() {
		return func() {
			defer wg.Done()
			// If an error has already occurred, don't continue processing
			if failed.Load() {
				return
			}

			embeddings, err := embedChunk(chunk)
			if err != nil {
				recordFirstErr(err)
				return
			}

			// Every sub-batch owns a distinct range of textEmbeddings, so these
			// assignments do not need a shared mutex. Keeping the copy lock-free
			// is also important for panic safety: a malformed provider response
			// must never strand the entire shared worker pool behind a locked
			// mutex.
			for i, text := range chunk {
				if text == nil {
					continue
				}
				text.results = embeddings[i]
			}
		}
	}

	// Submit all tasks to the goroutine pool
	for _, chunk := range utils.ChunkSlice(textEmbeddings, batchSize) {
		if failed.Load() {
			break
		}
		wg.Add(1)
		err := e.pool.Submit(processChunk(chunk))
		if err != nil {
			// Submit failed before the worker could run its deferred Done.
			wg.Done()
			recordFirstErr(fmt.Errorf("submit embedding sub-batch: %w", err))
			break
		}
	}

	// Wait for all tasks to complete
	wg.Wait()

	// Check if any errors occurred
	if firstErr != nil {
		return nil, firstErr
	}

	results := utils.MapSlice(textEmbeddings, func(text *textEmbedding) []float32 {
		return text.results
	})
	return results, nil
}

func validateBatchEmbeddingResult(embeddings [][]float32, expected int) error {
	if len(embeddings) != expected {
		return fmt.Errorf("embedding provider returned %d vectors for %d inputs", len(embeddings), expected)
	}
	for i, vector := range embeddings {
		if len(vector) == 0 {
			return fmt.Errorf("embedding provider returned an empty vector at index %d", i)
		}
	}
	return nil
}
