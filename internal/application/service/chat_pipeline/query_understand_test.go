package chatpipeline

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestApplyIntentPromptOverride_AgentOverrideWins(t *testing.T) {
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			IntentPromptOverrides: map[string]string{"chitchat": "agent prompt"},
		},
		PipelineState: types.PipelineState{Intent: types.IntentChitchat},
	}
	global := map[string]string{"chitchat": "global prompt"}

	if !applyIntentPromptOverride(cm, global) {
		t.Fatal("expected applied=true")
	}
	if cm.SystemPromptOverride != "agent prompt" {
		t.Errorf("override: got %q, want %q", cm.SystemPromptOverride, "agent prompt")
	}
}

func TestApplyIntentPromptOverride_PreservesAgentWhitespace(t *testing.T) {
	// Agent-supplied prompts with surrounding whitespace must reach the model
	// verbatim; trim is only used for emptiness detection.
	raw := "  agent prompt with trailing newline\n"
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			IntentPromptOverrides: map[string]string{"chitchat": raw},
		},
		PipelineState: types.PipelineState{Intent: types.IntentChitchat},
	}

	if !applyIntentPromptOverride(cm, nil) {
		t.Fatal("expected applied=true")
	}
	if cm.SystemPromptOverride != raw {
		t.Errorf("override: got %q, want %q", cm.SystemPromptOverride, raw)
	}
}

func TestApplyIntentPromptOverride_BlankAgentFallsBackToGlobal(t *testing.T) {
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			IntentPromptOverrides: map[string]string{"chitchat": "   \n\t  "},
		},
		PipelineState: types.PipelineState{Intent: types.IntentChitchat},
	}
	global := map[string]string{"chitchat": "global prompt"}

	if !applyIntentPromptOverride(cm, global) {
		t.Fatal("expected applied=true")
	}
	if cm.SystemPromptOverride != "global prompt" {
		t.Errorf("override: got %q, want %q", cm.SystemPromptOverride, "global prompt")
	}
}

func TestApplyIntentPromptOverride_NoOverrideAndNoGlobal(t *testing.T) {
	cm := &types.ChatManage{
		PipelineState: types.PipelineState{Intent: types.IntentChitchat},
	}

	if applyIntentPromptOverride(cm, nil) {
		t.Fatal("expected applied=false")
	}
	if cm.SystemPromptOverride != "" {
		t.Errorf("override should remain empty, got %q", cm.SystemPromptOverride)
	}
}

func TestApplyIntentPromptOverride_GlobalOnly(t *testing.T) {
	cm := &types.ChatManage{
		PipelineState: types.PipelineState{Intent: types.IntentGreeting},
	}
	global := map[string]string{"greeting": "hi there"}

	if !applyIntentPromptOverride(cm, global) {
		t.Fatal("expected applied=true")
	}
	if cm.SystemPromptOverride != "hi there" {
		t.Errorf("override: got %q, want %q", cm.SystemPromptOverride, "hi there")
	}
}

func TestParseStructuredQueryOutput_KnowledgeBaseCodes(t *testing.T) {
	output, ok := parseStructuredQueryOutput(
		`{"rewrite_query":"视神经炎如何诊断？","intent":"kb_search","image_description":"","knowledge_base_codes":["03","07"]}`,
	)
	if !ok {
		t.Fatal("expected structured output to parse")
	}
	if len(output.KnowledgeBaseCodes) != 2 ||
		output.KnowledgeBaseCodes[0] != "03" ||
		output.KnowledgeBaseCodes[1] != "07" {
		t.Fatalf("unexpected knowledge base codes: %#v", output.KnowledgeBaseCodes)
	}
}

func TestApplyKnowledgeRouting_SelectedAndRequiredUnion(t *testing.T) {
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			KnowledgeBaseIDs: []string{"kb01", "kb02", "kb03"},
			SearchTargets: types.SearchTargets{
				{KnowledgeBaseID: "kb01"},
				{KnowledgeBaseID: "kb02"},
				{KnowledgeBaseID: "kb03"},
			},
			KnowledgeRouting: &types.KnowledgeRoutingConfig{
				Routes: []types.KnowledgeBaseRoute{
					{Code: "01", KnowledgeBaseID: "kb01"},
					{Code: "02", KnowledgeBaseID: "kb02"},
					{Code: "03", KnowledgeBaseID: "kb03"},
				},
				RequiredKnowledgeBaseIDs: []string{"kb02"},
			},
		},
	}

	applyKnowledgeRouting(cm, []string{"03"})

	if len(cm.KnowledgeBaseIDs) != 2 ||
		cm.KnowledgeBaseIDs[0] != "kb02" ||
		cm.KnowledgeBaseIDs[1] != "kb03" {
		t.Fatalf("unexpected routed IDs: %#v", cm.KnowledgeBaseIDs)
	}
	if len(cm.SearchTargets) != 2 ||
		cm.SearchTargets[0].KnowledgeBaseID != "kb02" ||
		cm.SearchTargets[1].KnowledgeBaseID != "kb03" {
		t.Fatalf("unexpected routed targets: %#v", cm.SearchTargets)
	}
}

func TestApplyKnowledgeRouting_ExplicitOnlyIgnoresModelSelection(t *testing.T) {
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			KnowledgeBaseIDs: []string{"kb01", "kb02", "kb05", "kb08"},
			SearchTargets: types.SearchTargets{
				{KnowledgeBaseID: "kb01"},
				{KnowledgeBaseID: "kb02"},
				{KnowledgeBaseID: "kb05"},
				{KnowledgeBaseID: "kb08"},
			},
			KnowledgeRouting: &types.KnowledgeRoutingConfig{
				Routes: []types.KnowledgeBaseRoute{
					{Code: "01", KnowledgeBaseID: "kb01"},
					{Code: "02", KnowledgeBaseID: "kb02"},
					{Code: "05", KnowledgeBaseID: "kb05"},
					{Code: "08", KnowledgeBaseID: "kb08"},
				},
				RequiredKnowledgeBaseIDs: []string{"kb01", "kb02", "kb08"},
				ExplicitOnly:             true,
			},
		},
	}

	// The model may still suggest 05, but fixed-scope routing must ignore it.
	applyKnowledgeRouting(cm, []string{"01", "02", "05"})

	want := []string{"kb01", "kb02", "kb08"}
	if len(cm.KnowledgeBaseIDs) != len(want) {
		t.Fatalf("unexpected routed IDs: %#v", cm.KnowledgeBaseIDs)
	}
	for i, id := range want {
		if cm.KnowledgeBaseIDs[i] != id {
			t.Fatalf("routed IDs = %#v, want %#v", cm.KnowledgeBaseIDs, want)
		}
		if cm.SearchTargets[i].KnowledgeBaseID != id {
			t.Fatalf("routed targets = %#v, want %#v", cm.SearchTargets, want)
		}
	}
}

func TestApplyKnowledgeRouting_InvalidSelectionKeepsAuthorizedScope(t *testing.T) {
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			KnowledgeBaseIDs: []string{"kb01", "kb02"},
			SearchTargets: types.SearchTargets{
				{KnowledgeBaseID: "kb01"},
				{KnowledgeBaseID: "kb02"},
			},
			KnowledgeRouting: &types.KnowledgeRoutingConfig{
				Routes: []types.KnowledgeBaseRoute{
					{Code: "01", KnowledgeBaseID: "kb01"},
					{Code: "02", KnowledgeBaseID: "kb02"},
				},
			},
		},
	}

	applyKnowledgeRouting(cm, []string{"99"})

	if len(cm.KnowledgeBaseIDs) != 2 || len(cm.SearchTargets) != 2 {
		t.Fatalf("invalid routing must keep authorized scope: ids=%#v targets=%#v",
			cm.KnowledgeBaseIDs, cm.SearchTargets)
	}
}

func TestApplyKnowledgeRouting_NonRetrievalIntentClearsScope(t *testing.T) {
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			KnowledgeBaseIDs: []string{"kb01"},
			SearchTargets: types.SearchTargets{
				{KnowledgeBaseID: "kb01"},
			},
			KnowledgeRouting: &types.KnowledgeRoutingConfig{
				Routes: []types.KnowledgeBaseRoute{
					{Code: "01", KnowledgeBaseID: "kb01"},
				},
			},
		},
		PipelineState: types.PipelineState{Intent: types.IntentSummarize},
	}

	applyKnowledgeRouting(cm, nil)

	if len(cm.KnowledgeBaseIDs) != 0 || len(cm.SearchTargets) != 0 {
		t.Fatalf("non-retrieval intent retained KB scope: ids=%#v targets=%#v",
			cm.KnowledgeBaseIDs, cm.SearchTargets)
	}
	if cm.NeedsRetrieval() {
		t.Fatal("aggregate conversation summary must not retrieve")
	}
}
