package im

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestSummaryBotChannelAndMentionRecognition(t *testing.T) {
	if !isSummaryChannel(&IMChannel{Name: "00汇总机器人"}) {
		t.Fatal("00 channel was not recognized")
	}
	if !isSpecialtyChannel(&IMChannel{Name: "07神经眼科"}) {
		t.Fatal("specialty channel was not recognized")
	}
	if !isSpecialtyChannel(&IMChannel{Name: "24眼科临床指南与专家共识"}) {
		t.Fatal("24 specialty channel was not recognized")
	}
	if isSpecialtyChannel(&IMChannel{Name: "99病例推理机器人"}) {
		t.Fatal("99 reasoning channel must not be treated as a specialty channel")
	}
	msg := &IncomingMessage{
		Mentions: []IncomingMention{
			{Name: "00汇总机器人"},
			{Name: "07神经眼科"},
		},
	}
	if !messageMentionsBotCode(msg, "00") {
		t.Fatal("00 mention was not recognized")
	}
}

func TestMultiSpecialistMessageAutoForwardsWithout00(t *testing.T) {
	channel := &IMChannel{Name: "06-眼底内科：视网膜、黄斑与脉络膜"}
	msg := &IncomingMessage{
		Platform: PlatformFeishu,
		Mentions: []IncomingMention{
			{Name: "06-眼底内科：视网膜、黄斑与脉络膜"},
			{Name: "09-神经眼科"},
			{Name: "01-眼科综合：基础与全身病"},
		},
	}
	if !shouldAutoForwardToSummary(channel, msg) {
		t.Fatal("two or more specialist mentions must auto-forward to 00")
	}
	codes := mentionedSpecialtyBotCodes(msg)
	if len(codes) != 3 || codes[0] != "01" || codes[1] != "06" || codes[2] != "09" {
		t.Fatalf("unexpected specialist codes: %#v", codes)
	}

	msg.Mentions = append(msg.Mentions, IncomingMention{Name: "00汇总机器人"})
	if shouldAutoForwardToSummary(channel, msg) {
		t.Fatal("explicit @00 uses the normal 00 event and must not auto-forward")
	}
}

func TestMultiSpecialistMessageNeedsAtLeastTwoBots(t *testing.T) {
	channel := &IMChannel{Name: "09-神经眼科"}
	msg := &IncomingMessage{
		Platform: PlatformFeishu,
		Mentions: []IncomingMention{{Name: "09-神经眼科"}},
	}
	if shouldAutoForwardToSummary(channel, msg) {
		t.Fatal("one specialist mention must keep the original single-bot behavior")
	}
}

func TestMessageForSummaryUsesStableCrossAppIdentity(t *testing.T) {
	original := &IncomingMessage{
		UserID:       "app-specific-open-id",
		GlobalUserID: "stable-union-id",
		Mentions:     []IncomingMention{{Name: "09-神经眼科"}},
		Extra:        map[string]string{"key": "value"},
	}
	forwarded := messageForSummary(original)
	if forwarded == original {
		t.Fatal("summary message must be cloned")
	}
	if forwarded.UserID != "stable-union-id" {
		t.Fatalf("summary user ID = %q", forwarded.UserID)
	}
	forwarded.Mentions[0].Name = "changed"
	forwarded.Extra["key"] = "changed"
	if original.Mentions[0].Name != "09-神经眼科" || original.Extra["key"] != "value" {
		t.Fatal("summary message clone shares mutable fields with original")
	}
}

func TestMessageForExplicitSummaryMarksFixedScope(t *testing.T) {
	original := &IncomingMessage{
		UserID:       "app-specific-open-id",
		GlobalUserID: "stable-union-id",
		Mentions: []IncomingMention{
			{Name: "01眼科综合"},
			{Name: "02眼表"},
			{Name: "08葡萄膜炎"},
		},
	}
	forwarded := messageForExplicitSummary(original)
	if !forwarded.SummaryExplicitOnly {
		t.Fatal("auto-forwarded multi-specialist message must use fixed KB scope")
	}
	if forwarded.UserID != "stable-union-id" {
		t.Fatalf("summary user ID = %q", forwarded.UserID)
	}
	if original.SummaryExplicitOnly {
		t.Fatal("original incoming message was mutated")
	}
}

func TestSummaryChannelIDForSameTenantAndPlatform(t *testing.T) {
	service := &Service{
		channels: map[string]*channelState{
			"summary-other-tenant": {
				Channel: &IMChannel{
					ID:       "summary-other-tenant",
					TenantID: 2,
					Platform: string(PlatformFeishu),
					Name:     "00汇总机器人",
					Enabled:  true,
				},
			},
			"summary-right": {
				Channel: &IMChannel{
					ID:       "summary-right",
					TenantID: 1,
					Platform: string(PlatformFeishu),
					Name:     "00汇总机器人",
					Enabled:  true,
				},
			},
		},
	}
	source := &IMChannel{TenantID: 1, Platform: string(PlatformFeishu)}
	id, ok := service.summaryChannelIDFor(source)
	if !ok || id != "summary-right" {
		t.Fatalf("summary channel = %q, found=%v", id, ok)
	}
}

func TestSummaryMessageIDDedupElectsOneForwarder(t *testing.T) {
	service := &Service{}
	ctx := context.Background()
	if service.isDuplicate(ctx, "summary-channel", "message-1") {
		t.Fatal("first aggregate forward must win")
	}
	if !service.isDuplicate(ctx, "summary-channel", "message-1") {
		t.Fatal("second aggregate forward must be suppressed")
	}
	if service.isDuplicate(ctx, "specialist-channel", "message-1") {
		t.Fatal("dedup must remain channel-scoped")
	}
}

func TestBuildKnowledgeBaseRoutesAndRequiredMentions(t *testing.T) {
	kbs := []*types.KnowledgeBase{
		{ID: "kb03", Name: "03眼底内科", Description: "眼底疾病"},
		{ID: "kb01", Name: "01眼科综合", Description: "眼科综合"},
		{ID: "kb24", Name: "24眼科临床指南与专家共识", Description: "最新临床指南与专家共识"},
		{ID: "kb00", Name: "00汇总机器人"},
		{ID: "kb99", Name: "99病例推理机器人"},
		{ID: "other", Name: "未编号知识库"},
	}
	routes := buildKnowledgeBaseRoutes(kbs)
	if len(routes) != 3 || routes[0].Code != "01" || routes[1].Code != "03" || routes[2].Code != "24" {
		t.Fatalf("unexpected routes: %#v", routes)
	}

	codes, ids := requiredRoutesFromMentions([]IncomingMention{
		{Name: "03眼底内科"},
		{Name: "24眼科临床指南与专家共识"},
		{Name: "00汇总机器人"},
		{Name: "03眼底内科"},
	}, routes)
	if len(codes) != 2 || codes[0] != "03" || codes[1] != "24" ||
		len(ids) != 2 || ids[0] != "kb03" || ids[1] != "kb24" {
		t.Fatalf("unexpected required routes: codes=%#v ids=%#v", codes, ids)
	}
}

func TestCloneSummaryAgentForcesQuickAnswerAndNoWeb(t *testing.T) {
	agent := &types.CustomAgent{
		Config: types.CustomAgentConfig{
			AgentMode:        types.AgentModeSmartReasoning,
			WebSearchEnabled: true,
			HistoryTurns:     3,
		},
	}
	routes := []types.KnowledgeBaseRoute{
		{Code: "01", KnowledgeBaseID: "kb01", Name: "01眼科综合", Description: "眼科综合"},
		{Code: "07", KnowledgeBaseID: "kb07", Name: "07神经眼科", Description: "神经眼科"},
	}

	cloned := cloneSummaryAgent(agent, "base rewrite prompt", routes, []string{"07"}, false)
	if cloned == agent {
		t.Fatal("agent must be cloned per request")
	}
	if cloned.Config.AgentMode != types.AgentModeQuickAnswer {
		t.Fatalf("agent mode = %q", cloned.Config.AgentMode)
	}
	if cloned.Config.WebSearchEnabled {
		t.Fatal("web search must be disabled")
	}
	if cloned.Config.KBSelectionMode != "selected" ||
		len(cloned.Config.KnowledgeBases) != 2 ||
		cloned.Config.KnowledgeBases[0] != "kb01" ||
		cloned.Config.KnowledgeBases[1] != "kb07" {
		t.Fatalf("aggregate allowed KB scope is incorrect: mode=%q kbs=%#v",
			cloned.Config.KBSelectionMode, cloned.Config.KnowledgeBases)
	}
	if !cloned.Config.EnableRewrite || !cloned.Config.MultiTurnEnabled {
		t.Fatal("rewrite and multi-turn context must be enabled")
	}
	for _, want := range []string{
		"base rewrite prompt",
		"Knowledge Base Routing",
		"01眼科综合",
		"07神经眼科",
		"explicitly @mentioned specialist codes 07",
		"必须将24号眼科临床指南和专家共识知识库纳入知识库检索范围中",
		"include both code 24 and the relevant subspecialty code or codes",
		"knowledge_base_codes",
	} {
		if !strings.Contains(cloned.Config.RewritePromptSystem, want) {
			t.Fatalf("rewrite prompt missing %q", want)
		}
	}
	if !strings.Contains(cloned.Config.FallbackPrompt, "暂未在相关知识库中找到直接资料") {
		t.Fatal("fallback prompt must require the no-KB-material notice")
	}
	if agent.Config.AgentMode != types.AgentModeSmartReasoning || !agent.Config.WebSearchEnabled {
		t.Fatal("original agent was mutated")
	}
}

func TestCloneSummaryAgentExplicitOnlyUsesMentionedScope(t *testing.T) {
	agent := &types.CustomAgent{
		Config: types.CustomAgentConfig{
			AgentMode:        types.AgentModeSmartReasoning,
			WebSearchEnabled: true,
		},
	}
	routes := []types.KnowledgeBaseRoute{
		{Code: "01", KnowledgeBaseID: "kb01", Name: "01眼科综合", Description: "综合眼科"},
		{Code: "02", KnowledgeBaseID: "kb02", Name: "02眼表", Description: "角膜与眼表"},
		{Code: "05", KnowledgeBaseID: "kb05", Name: "05青光眼", Description: "青光眼"},
		{Code: "08", KnowledgeBaseID: "kb08", Name: "08葡萄膜炎", Description: "葡萄膜炎"},
	}

	cloned := cloneSummaryAgent(agent, "base rewrite prompt", routes, []string{"01", "02", "08"}, true)

	want := []string{"kb01", "kb02", "kb08"}
	if len(cloned.Config.KnowledgeBases) != len(want) {
		t.Fatalf("fixed aggregate scope = %#v, want %#v", cloned.Config.KnowledgeBases, want)
	}
	for i, id := range want {
		if cloned.Config.KnowledgeBases[i] != id {
			t.Fatalf("fixed aggregate scope = %#v, want %#v", cloned.Config.KnowledgeBases, want)
		}
	}
	for _, wantText := range []string{
		"Fixed Knowledge Base Scope",
		"Do not infer, select, or add any other knowledge base",
		"The explicitly mentioned specialist codes are: 01, 02, 08",
		`"knowledge_base_codes":["01","02","08"]`,
	} {
		if !strings.Contains(cloned.Config.RewritePromptSystem, wantText) {
			t.Fatalf("fixed-scope rewrite prompt missing %q", wantText)
		}
	}
	if strings.Contains(cloned.Config.RewritePromptSystem, "05青光眼") {
		t.Fatal("fixed-scope prompt must not expose unmentioned knowledge-base descriptions")
	}
}
