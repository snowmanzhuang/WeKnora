package im

import (
	"reflect"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestAgentForKnowledgeImageRequestRoutesOnlyNumberedFeishuImageRequests(t *testing.T) {
	original := &types.CustomAgent{
		ID: "agent-17",
		Config: types.CustomAgentConfig{
			AgentMode:    types.AgentModeQuickAnswer,
			AllowedTools: []string{"old-tool"},
		},
	}
	channel := &IMChannel{Name: "17-检查：眼科超声与 UBM"}
	msg := &IncomingMessage{
		Platform: PlatformFeishu,
		Content:  "玻璃体后脱离的B超图给我看看，越多越好",
	}

	got, forced := agentForKnowledgeImageRequest(channel, msg, original)
	if !forced {
		t.Fatal("image request was not routed to request-local agent mode")
	}
	if got == original {
		t.Fatal("persisted agent was returned instead of a request-local copy")
	}
	if !got.IsAgentMode() || got.Config.AgentType != types.AgentTypeRAGQA {
		t.Fatalf("request agent mode/type = %q/%q", got.Config.AgentMode, got.Config.AgentType)
	}
	if !reflect.DeepEqual(got.Config.AllowedTools, knowledgeImageAgentTools) {
		t.Fatalf("allowed tools = %#v", got.Config.AllowedTools)
	}
	if original.Config.AgentMode != types.AgentModeQuickAnswer ||
		!reflect.DeepEqual(original.Config.AllowedTools, []string{"old-tool"}) {
		t.Fatal("persisted agent configuration was mutated")
	}
}

func TestAgentForKnowledgeImageRequestLeavesOtherTurnsUnchanged(t *testing.T) {
	agent := &types.CustomAgent{Config: types.CustomAgentConfig{AgentMode: types.AgentModeQuickAnswer}}
	tests := []struct {
		name    string
		channel *IMChannel
		msg     *IncomingMessage
	}{
		{
			name:    "ordinary clinical question",
			channel: &IMChannel{Name: "17-检查：眼科超声与 UBM"},
			msg:     &IncomingMessage{Platform: PlatformFeishu, Content: "玻璃体后脱离与视网膜脱离如何鉴别？"},
		},
		{
			name:    "uploaded image analysis",
			channel: &IMChannel{Name: "17-检查：眼科超声与 UBM"},
			msg: &IncomingMessage{Platform: PlatformFeishu, Content: "这张图是什么？", Images: []IncomingImage{
				{FileKey: "img-1"},
			}},
		},
		{
			name:    "number 99 excluded",
			channel: &IMChannel{Name: "99-病例推理机器人"},
			msg:     &IncomingMessage{Platform: PlatformFeishu, Content: "再给几张图看看"},
		},
		{
			name:    "non Feishu excluded",
			channel: &IMChannel{Name: "17-检查：眼科超声与 UBM"},
			msg:     &IncomingMessage{Platform: PlatformTelegram, Content: "再给几张图看看"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, forced := agentForKnowledgeImageRequest(tt.channel, tt.msg, agent)
			if forced || got != agent {
				t.Fatalf("unrelated turn was changed: forced=%v", forced)
			}
		})
	}
}

func TestIsKnowledgeImageRequestRecognizesShortFollowUps(t *testing.T) {
	for _, content := range []string{"有图吗？", "还有图片吗", "再来几张示例图", "给我看看OCT图"} {
		if !isKnowledgeImageRequest(&IncomingMessage{Content: content}) {
			t.Errorf("did not recognize %q", content)
		}
	}
}
