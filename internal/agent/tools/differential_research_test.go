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

func TestDifferentialImagesFromMarkdown(t *testing.T) {
	content := `正文
![Fig. 3.15 MEWDS fundus photograph](resource://mewds-image)
![Birdshot ICGA](<resource://birdshot-image> "plate 2")
![重复图](resource://mewds-image)`

	images := differentialImagesFromMarkdown(content)
	require.Len(t, images, 2)
	require.Equal(t, "resource://mewds-image", images[0].URL)
	require.Equal(t, "Fig. 3.15 MEWDS fundus photograph", images[0].Caption)
	require.Equal(t, "resource://birdshot-image", images[1].URL)
	require.Equal(t, "Birdshot ICGA", images[1].Caption)
}

func TestSelectDifferentialSourcesRecognizesMarkdownImages(t *testing.T) {
	rows := []map[string]interface{}{
		{"chunk_id": "c1", "content": "best text evidence"},
		{"chunk_id": "c2", "content": "second text evidence"},
		{
			"chunk_id": "c3",
			"content":  "MEWDS\n![MEWDS color fundus photograph](resource://mewds)",
		},
	}

	sources := selectDifferentialSources(rows, 3)
	require.Len(t, sources, 3)
	require.Len(t, sources[2].Images, 1)
	require.Equal(t, "resource://mewds", sources[2].Images[0].URL)
}

func TestRankedDifferentialImagesPrioritizesCandidateCaption(t *testing.T) {
	sources := []differentialSource{{
		KnowledgeTitle: "Atlas",
		Content:        "White dot syndromes",
		Images: []differentialImage{
			{URL: "resource://unrelated", Caption: "Birdshot chorioretinopathy"},
			{URL: "resource://match", Caption: "Fig. 3.15 Multiple evanescent white dot syndrome (MEWDS)"},
		},
	}}
	images, _ := rankedDifferentialImages(
		DifferentialResearchInput{ImageType: "color fundus photograph"},
		DifferentialCandidate{
			Diagnosis: "多发性一过性白点综合征",
			Synonyms:  []string{"MEWDS", "Multiple evanescent white dot syndrome"},
		},
		sources,
	)
	require.Len(t, images, 2)
	require.Equal(t, "resource://match", images[0].URL)
}

func TestExplicitlyMatchedDifferentialImageIndexRequiresNamedCaption(t *testing.T) {
	images := []differentialImage{
		{URL: "resource://generic", Caption: "Posterior pole color fundus photograph"},
		{URL: "resource://match", Caption: "Multiple evanescent white dot syndrome (MEWDS)"},
	}
	candidate := DifferentialCandidate{
		Diagnosis: "多发性一过性白点综合征",
		Synonyms:  []string{"MEWDS", "Multiple evanescent white dot syndrome"},
	}
	require.Equal(t, 2, explicitlyMatchedDifferentialImageIndex(images, candidate))
	require.Zero(t, explicitlyMatchedDifferentialImageIndex(images[:1], candidate))
}

func TestRankDifferentialKnowledgeBasesUsesSubspecialtyAndModality(t *testing.T) {
	kbs := []*types.KnowledgeBase{
		{ID: "general", Name: "01 眼科综合"},
		{ID: "retina", Name: "06 眼底内科"},
		{ID: "uveitis", Name: "08 葡萄膜炎与眼内炎症"},
		{ID: "angiography", Name: "15 FFA/ICGA"},
		{ID: "cornea", Name: "02 角膜与眼表"},
	}
	allowed := map[string]bool{
		"general": true, "retina": true, "uveitis": true, "angiography": true, "cornea": true,
	}
	got := rankDifferentialKnowledgeBases(
		DifferentialResearchInput{ImageType: "ICGA"},
		DifferentialCandidate{
			Diagnosis: "Vogt-Koyanagi-Harada disease",
			Synonyms:  []string{"VKH", "小柳-原田病"},
		},
		kbs,
		allowed,
		4,
	)
	require.Contains(t, got, "uveitis")
	require.Contains(t, got, "angiography")
	require.Contains(t, got, "general")
	require.NotContains(t, got, "cornea")
}

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

func TestAugmentDifferentialCandidatesRestoresMissingHintsIntoFreeSlots(t *testing.T) {
	candidates := []DifferentialCandidate{
		{Diagnosis: "候选甲", Synonyms: []string{"Candidate Alpha", "CA"}},
		{Diagnosis: "候选乙", Synonyms: []string{"Candidate Beta", "CB"}},
		{Diagnosis: "候选丙", Synonyms: []string{"Candidate Gamma", "CG"}},
	}
	hints := []string{
		"候选甲（Candidate Alpha；CA）",
		"候选乙（Candidate Beta；CB）",
		"候选丙（Candidate Gamma；CG）",
		"候选丁（Candidate Delta；CD）",
	}

	got, added := augmentDifferentialCandidates(candidates, hints, 5)

	require.Len(t, got, 4)
	require.Equal(t, "候选丁", got[3].Diagnosis)
	require.Contains(t, got[3].Synonyms, "Candidate Delta")
	require.Contains(t, got[3].Synonyms, "CD")
	require.Equal(t, []string{"候选丁"}, added)
}

func TestAugmentDifferentialCandidatesNeverExceedsFive(t *testing.T) {
	candidates := []DifferentialCandidate{
		{Diagnosis: "候选一"},
		{Diagnosis: "候选二"},
		{Diagnosis: "候选三"},
		{Diagnosis: "候选四"},
	}
	hints := []string{"候选五", "候选六"}

	got, added := augmentDifferentialCandidates(candidates, hints, 5)

	require.Len(t, got, 5)
	require.Equal(t, []string{"候选五"}, added)
}
