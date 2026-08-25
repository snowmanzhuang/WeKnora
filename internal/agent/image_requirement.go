package agent

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

const agentRetrievedImageRequirementMarker = "## Retrieved Image Output Requirement"

const agentRetrievedImageSystemRequirement = `

## Retrieved Image Output Requirement
Retrieved tool results for this turn contain Markdown images. Treat images attached to retrieved passages as relevant by default.
- Unless the user explicitly requests text-only output, or every retrieved image is clearly unrelated, the final answer MUST include at least one relevant Markdown image from the tool results.
- Preserve the source image URL exactly. Derive one complete Chinese caption from the source alt/caption and use exactly the same complete Chinese caption text in both the Markdown image alt and the visible caption immediately below it.
- Do not summarize, shorten, paraphrase, merge, or omit any intelligible information from the source alt/caption, even when it is long. If the source is already Chinese, preserve it as closely as possible and only correct obvious OCR, encoding, layout, or punctuation errors. If it is not Chinese, translate every intelligible item completely into Chinese rather than translating only the main idea.
- Preserve figure numbers, panel labels, laterality, anatomy, examination or imaging type, time points, stages, directions, values, units, grading codes, findings, diagnoses, patient details, literature sources, cross-figure relationships such as "same patient as Fig. X", and every other qualifier.
- Do not add interpretation, inference, contextual supplementation, or facts absent from the source caption. Put any additional explanation in a separate paragraph after the complete caption.
- Use ASCII half-width parentheses exactly as ![alt](url); never use full-width （ or ）.
- Place each image immediately after the paragraph it supports.
- When multiple retrieved images support different sections, distribute them across those sections instead of stopping after the first image.
- Before finishing, compare each final Chinese caption item by item with its source caption and silently verify that no intelligible information was lost.`

func stepContainsMarkdownImage(step types.AgentStep) bool {
	for _, toolCall := range step.ToolCalls {
		if toolCall.Result != nil &&
			toolCall.Result.Success &&
			searchutil.MarkdownImageRegex.MatchString(toolCall.Result.Output) {
			return true
		}
	}
	return false
}

func appendAgentRetrievedImageRequirement(messages []chat.Message) []chat.Message {
	for i := range messages {
		if messages[i].Role != "system" {
			continue
		}
		if !strings.Contains(messages[i].Content, agentRetrievedImageRequirementMarker) {
			messages[i].Content = strings.TrimRight(messages[i].Content, " \t\r\n") + agentRetrievedImageSystemRequirement
		}
		break
	}
	return messages
}
