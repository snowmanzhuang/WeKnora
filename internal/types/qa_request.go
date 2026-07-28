package types

// KnowledgeBaseRoute describes one knowledge base that a query-understanding
// model may select for the current request.
type KnowledgeBaseRoute struct {
	Code            string `json:"code"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
}

// KnowledgeRoutingConfig enables request-scoped knowledge-base routing. The
// query-understanding stage selects one or more route codes, while required
// IDs (for example, explicitly @mentioned specialist bots) are always kept.
type KnowledgeRoutingConfig struct {
	Routes                   []KnowledgeBaseRoute `json:"routes"`
	RequiredKnowledgeBaseIDs []string             `json:"required_knowledge_base_ids,omitempty"`
	// ExplicitOnly prevents query understanding from adding knowledge bases
	// beyond those explicitly selected by the user.
	ExplicitOnly bool `json:"explicit_only,omitempty"`
}

// Clone returns an independent copy safe for an in-flight pipeline.
func (c *KnowledgeRoutingConfig) Clone() *KnowledgeRoutingConfig {
	if c == nil {
		return nil
	}
	return &KnowledgeRoutingConfig{
		Routes:                   append([]KnowledgeBaseRoute(nil), c.Routes...),
		RequiredKnowledgeBaseIDs: append([]string(nil), c.RequiredKnowledgeBaseIDs...),
		ExplicitOnly:             c.ExplicitOnly,
	}
}

// QARequest consolidates all parameters for KnowledgeQA and AgentQA service calls,
// replacing the previous 14-parameter method signatures.
// EventBus is passed separately to avoid circular dependency with the event package.
type QARequest struct {
	Session            *Session                // The conversation session
	Query              string                  // User query text
	AssistantMessageID string                  // Pre-created assistant message ID
	SummaryModelID     string                  // Optional model override; empty = use agent/KB default
	CustomAgent        *CustomAgent            // Optional custom agent for config override
	KnowledgeBaseIDs   []string                // Knowledge base IDs to search (from request + @mentions)
	KnowledgeIDs       []string                // Specific knowledge (file) IDs to search
	TagScopes          []TagScope              // Tag-constrained KB scopes from @mentions
	MCPServiceIDs      []string                // Per-request MCP service IDs from @mentions
	SkillNames         []string                // Per-request preloaded skill names from @mentions
	ImageURLs          []string                // Image URLs for multimodal input
	ImageDescription   string                  // VLM report: non-vision fallback or smart-agent auxiliary observation
	UserMessageID      string                  // Created user message ID
	WebSearchEnabled   bool                    // Whether web search is enabled for this request
	QuotedContext      string                  // Quoted message content from IM quote-reply (appended at LLM prompt stage, not used for retrieval)
	Attachments        MessageAttachments      // File attachments (processed and ready for prompt injection)
	KnowledgeRouting   *KnowledgeRoutingConfig // Optional dynamic KB routing for aggregate IM assistants
}
