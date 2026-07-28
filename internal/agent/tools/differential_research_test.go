package tools

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestDifferentialResearchToolRunsFiveWorkersConcurrently(t *testing.T) {
	tool := &DifferentialResearchTool{
		BaseTool:       differentialResearchTool,
		maxConcurrency: 5,
	}
	started := make(chan struct{}, 5)
	release := make(chan struct{})
	var active atomic.Int32
	var peak atomic.Int32
	tool.runCandidateFn = func(
		ctx context.Context,
		_ DifferentialResearchInput,
		candidate DifferentialCandidate,
	) DifferentialSubagentResult {
		current := active.Add(1)
		for {
			previous := peak.Load()
			if current <= previous || peak.CompareAndSwap(previous, current) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		active.Add(-1)
		return DifferentialSubagentResult{
			Diagnosis:       candidate.Diagnosis,
			Status:          "no_image",
			EvidenceSummary: "bounded result",
		}
	}

	args, err := json.Marshal(DifferentialResearchInput{
		ImageType: "FFA",
		Candidates: []DifferentialCandidate{
			{Diagnosis: "A"},
			{Diagnosis: "B"},
			{Diagnosis: "C"},
			{Diagnosis: "D"},
			{Diagnosis: "E"},
		},
	})
	require.NoError(t, err)

	done := make(chan *types.ToolResult, 1)
	go func() {
		result, _ := tool.Execute(context.Background(), args)
		done <- result
	}()

	for i := 0; i < 5; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for five concurrent workers")
		}
	}
	require.Equal(t, int32(5), peak.Load())
	close(release)

	select {
	case result := <-done:
		require.True(t, result.Success)
		require.Equal(t, 5, result.Data["candidate_count"])
		require.Equal(t, 5, result.Data["max_concurrency"])
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for differential research result")
	}
}

func TestDifferentialResearchToolRejectsMoreThanFiveCandidates(t *testing.T) {
	tool := &DifferentialResearchTool{
		BaseTool:       differentialResearchTool,
		maxConcurrency: 99,
		runCandidateFn: func(
			context.Context,
			DifferentialResearchInput,
			DifferentialCandidate,
		) DifferentialSubagentResult {
			t.Fatal("worker must not run for invalid input")
			return DifferentialSubagentResult{}
		},
	}
	args, err := json.Marshal(DifferentialResearchInput{
		ImageType: "OCT",
		Candidates: []DifferentialCandidate{
			{Diagnosis: "A"},
			{Diagnosis: "B"},
			{Diagnosis: "C"},
			{Diagnosis: "D"},
			{Diagnosis: "E"},
			{Diagnosis: "F"},
		},
	})
	require.NoError(t, err)

	result, executeErr := tool.Execute(context.Background(), args)
	require.Error(t, executeErr)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "2-5")
}
