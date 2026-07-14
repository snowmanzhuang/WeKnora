package types

import "testing"

func TestChatManageClonePreservesFallbackModel(t *testing.T) {
	original := &ChatManage{PipelineRequest: PipelineRequest{
		ChatModelID:            "primary-model",
		FallbackModelID:        "fallback-model",
		FallbackSupportsVision: true,
	}}

	clone := original.Clone()

	if clone.FallbackModelID != "fallback-model" {
		t.Fatalf("FallbackModelID = %q, want fallback-model", clone.FallbackModelID)
	}
	if !clone.FallbackSupportsVision {
		t.Fatal("FallbackSupportsVision = false, want true")
	}
}
