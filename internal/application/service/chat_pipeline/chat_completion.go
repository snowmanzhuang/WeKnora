package chatpipeline

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginChatCompletion implements chat completion functionality
// as a plugin that can be registered to EventManager
type PluginChatCompletion struct {
	modelService interfaces.ModelService // Interface for model operations
}

// NewPluginChatCompletion creates a new PluginChatCompletion instance
// and registers it with the EventManager
func NewPluginChatCompletion(eventManager *EventManager, modelService interfaces.ModelService) *PluginChatCompletion {
	res := &PluginChatCompletion{
		modelService: modelService,
	}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the event types this plugin handles
func (p *PluginChatCompletion) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHAT_COMPLETION}
}

// OnEvent handles chat completion events
// It prepares the chat model, messages, and calls the model to generate responses
func (p *PluginChatCompletion) OnEvent(
	ctx context.Context, eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	pipelineInfo(ctx, "Completion", "input", map[string]interface{}{
		"session_id":     chatManage.SessionID,
		"user_question":  chatManage.UserContent,
		"history_rounds": len(chatManage.History),
		"chat_model":     chatManage.ChatModelID,
	})

	// Prepare chat model and options
	usingFallback := false
	chatModel, opt, err := prepareChatModel(ctx, p.modelService, chatManage)
	if err != nil {
		if chatManage.FallbackModelID == "" || !chat.ShouldFailover(ctx, err) {
			return ErrGetChatModel.WithError(err)
		}
		pipelineWarn(ctx, "Completion", "fallback_model_activate", map[string]interface{}{
			"primary_model":  chatManage.ChatModelID,
			"fallback_model": chatManage.FallbackModelID,
			"error":          err.Error(),
		})
		chatModel, opt, err = prepareFallbackChatModel(ctx, p.modelService, chatManage)
		if err != nil {
			return ErrGetChatModel.WithError(fmt.Errorf("primary model unavailable and fallback initialization failed: %w", err))
		}
		usingFallback = true
	}

	// Prepare messages including conversation history
	pipelineInfo(ctx, "Completion", "messages_ready", map[string]interface{}{
		"message_count": len(chatManage.History) + 2,
	})
	chatMessages := prepareMessagesWithHistory(chatManage)
	activeModelID := chatManage.ChatModelID
	if usingFallback {
		activeModelID = chatManage.FallbackModelID
		chatMessages = prepareMessagesWithHistoryForVision(chatManage, chatManage.FallbackSupportsVision)
	}

	// Call the chat model to generate response
	pipelineInfo(ctx, "Completion", "model_call", map[string]interface{}{
		"chat_model": chatManage.ChatModelID,
	})
	chatResponse, err := callChatModelWithRetry(ctx, chatModel, activeModelID, chatMessages, opt)
	if err != nil && !usingFallback && chatManage.FallbackModelID != "" && chat.ShouldFailover(ctx, err) {
		pipelineWarn(ctx, "Completion", "fallback_model_activate", map[string]interface{}{
			"primary_model":  chatManage.ChatModelID,
			"fallback_model": chatManage.FallbackModelID,
			"error":          err.Error(),
		})
		fallbackModel, fallbackOpt, fallbackErr := prepareFallbackChatModel(ctx, p.modelService, chatManage)
		if fallbackErr == nil {
			fallbackMessages := prepareMessagesWithHistoryForVision(chatManage, chatManage.FallbackSupportsVision)
			chatResponse, err = callChatModelWithRetry(
				ctx, fallbackModel, chatManage.FallbackModelID, fallbackMessages, fallbackOpt,
			)
		} else {
			err = fmt.Errorf("primary model failed and fallback initialization failed: %v: %w", err, fallbackErr)
		}
	}
	if err != nil {
		pipelineError(ctx, "Completion", "model_call", map[string]interface{}{
			"chat_model": chatManage.ChatModelID,
			"error":      err.Error(),
		})
		return ErrModelCall.WithError(err)
	}

	pipelineInfo(ctx, "Completion", "output", map[string]interface{}{
		"answer_preview":    chatResponse.Content,
		"finish_reason":     chatResponse.FinishReason,
		"completion_tokens": chatResponse.Usage.CompletionTokens,
		"prompt_tokens":     chatResponse.Usage.PromptTokens,
	})
	warnIfAnswerMissingKBCitations(ctx, "Completion", chatManage, chatResponse.Content)
	chatManage.ChatResponse = chatResponse
	return next()
}

func callChatModelWithRetry(
	ctx context.Context,
	model chat.Chat,
	modelID string,
	messages []chat.Message,
	opt *chat.ChatOptions,
) (*types.ChatResponse, error) {
	var response *types.ChatResponse
	var err error
	for attempt := 1; attempt <= chatCompletionMaxAttempts; attempt++ {
		response, err = model.Chat(ctx, messages, opt)
		if err == nil || !isRetryableChatModelError(ctx, err) || attempt == chatCompletionMaxAttempts {
			break
		}
		pipelineWarn(ctx, "Completion", "model_call_retry", map[string]interface{}{
			"chat_model": modelID,
			"attempt":    attempt,
			"max":        chatCompletionMaxAttempts,
			"error":      err.Error(),
		})
		if !sleepBeforeChatRetry(ctx, attempt) {
			break
		}
	}
	return response, err
}
