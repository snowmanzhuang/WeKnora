package im

import (
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

var (
	knowledgeImageNounRe = regexp.MustCompile(`(?i)(?:图(?:片|像|例|谱|示|片资料)?|影像|照片|超声图|b超图|octa?图|ffa图|icga图|ct图|mri图)`)
	knowledgeImageAskRe  = regexp.MustCompile(`(?:有|还有|有没有|要|想要|给我|发我|发一下|提供|找|查找|搜|看看|看一下|展示|显示|来|再来|更多|多些|多一点|几张|越多越好)`)
)

var knowledgeImageAgentTools = []string{
	"knowledge_search",
	"grep_chunks",
	"list_knowledge_chunks",
	"query_knowledge_graph",
	"get_document_info",
}

// isNumberedOphthalmologyChannel limits special image retrieval routing to the
// user's numbered Feishu assistants (00 through 24). Other IM channels and the
// 99 case-reasoning assistant retain their configured behavior.
func isNumberedOphthalmologyChannel(channel *IMChannel) bool {
	if channel == nil {
		return false
	}
	code := botCode(channel.Name)
	return code >= summaryBotCode && code <= lastSpecialtyBotCode
}

// isKnowledgeImageRequest recognizes requests to fetch/show images from the
// assistant's knowledge scope. It deliberately excludes turns that already
// carry uploaded images: those are image-understanding requests and must keep
// their existing multimodal path.
func isKnowledgeImageRequest(msg *IncomingMessage) bool {
	if msg == nil || len(incomingImages(msg)) > 0 {
		return false
	}
	content := strings.TrimSpace(msg.Content)
	return content != "" && knowledgeImageNounRe.MatchString(content) && knowledgeImageAskRe.MatchString(content)
}

// agentForKnowledgeImageRequest returns a request-local agent copy configured
// like the stable 99 retrieval path. The persisted custom agent is never
// mutated, so non-image questions continue to use their original mode.
func agentForKnowledgeImageRequest(
	channel *IMChannel,
	msg *IncomingMessage,
	agent *types.CustomAgent,
) (*types.CustomAgent, bool) {
	if agent == nil || !isFeishuPlatform(msg.Platform) ||
		!isNumberedOphthalmologyChannel(channel) || !isKnowledgeImageRequest(msg) {
		return agent, false
	}
	if agent.IsAgentMode() {
		return agent, false
	}

	clone := *agent
	clone.Config = agent.Config
	clone.Config.AgentMode = types.AgentModeSmartReasoning
	clone.Config.AgentType = types.AgentTypeRAGQA
	clone.Config.AllowedTools = append([]string(nil), knowledgeImageAgentTools...)
	return &clone, true
}
