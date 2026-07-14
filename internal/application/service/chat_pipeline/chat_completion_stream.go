package chatpipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// PluginChatCompletionStream implements streaming chat completion functionality
// as a plugin that can be registered to EventManager
type PluginChatCompletionStream struct {
	modelService interfaces.ModelService // Interface for model operations
}

// NewPluginChatCompletionStream creates a new PluginChatCompletionStream instance
// and registers it with the EventManager
func NewPluginChatCompletionStream(eventManager *EventManager,
	modelService interfaces.ModelService,
) *PluginChatCompletionStream {
	res := &PluginChatCompletionStream{
		modelService: modelService,
	}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the event types this plugin handles
func (p *PluginChatCompletionStream) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHAT_COMPLETION_STREAM}
}

// OnEvent handles streaming chat completion events
// It prepares the chat model, messages, and initiates streaming response
func (p *PluginChatCompletionStream) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	pipelineInfo(ctx, "Stream", "input", map[string]interface{}{
		"session_id":     chatManage.SessionID,
		"user_question":  chatManage.UserContent,
		"history_rounds": len(chatManage.History),
		"chat_model":     chatManage.ChatModelID,
	})

	// Prepare chat model and options
	usingFallback := false
	chatModel, opt, err := prepareChatModel(ctx, p.modelService, chatManage)
	if err != nil {
		primaryErr := err
		if chatManage.FallbackModelID == "" || !chat.ShouldFailover(ctx, err) {
			return ErrGetChatModel.WithError(err)
		}
		chatModel, opt, err = prepareFallbackChatModel(ctx, p.modelService, chatManage)
		if err != nil {
			return ErrGetChatModel.WithError(err)
		}
		usingFallback = true
		logStreamFallbackActivation(ctx, chatManage, primaryErr)
	}

	// Prepare base messages without history

	activeModelID := chatManage.ChatModelID
	chatMessages := prepareMessagesWithHistory(chatManage)
	if usingFallback {
		activeModelID = chatManage.FallbackModelID
		chatMessages = prepareMessagesWithHistoryForVision(chatManage, chatManage.FallbackSupportsVision)
	}
	pipelineInfo(ctx, "Stream", "messages_ready", map[string]interface{}{
		"message_count": len(chatMessages),
		"system_prompt": chatMessages[0].Content,
	})
	pipelineInfo(ctx, "Stream", "user_message", map[string]interface{}{
		"content": chatMessages[len(chatMessages)-1].Content,
	})
	// EventBus is required for event-driven streaming
	if chatManage.EventBus == nil {
		pipelineError(ctx, "Stream", "eventbus_missing", map[string]interface{}{
			"session_id": chatManage.SessionID,
		})
		return ErrModelCall.WithError(errors.New("EventBus is required for streaming"))
	}
	eventBus := chatManage.EventBus

	pipelineInfo(ctx, "Stream", "eventbus_ready", map[string]interface{}{
		"session_id": chatManage.SessionID,
	})

	// Initiate streaming chat model call with independent context
	pipelineInfo(ctx, "Stream", "model_call", map[string]interface{}{
		"chat_model": chatManage.ChatModelID,
	})
	responseChan, streamAttempt, err := startChatStreamWithRetry(
		ctx, chatModel, activeModelID, chatMessages, opt,
	)
	if err != nil && !usingFallback && chatManage.FallbackModelID != "" && chat.ShouldFailover(ctx, err) {
		fallbackModel, fallbackOpt, fallbackErr := prepareFallbackChatModel(ctx, p.modelService, chatManage)
		if fallbackErr == nil {
			fallbackMessages := prepareMessagesWithHistoryForVision(chatManage, chatManage.FallbackSupportsVision)
			fallbackChan, fallbackAttempt, startErr := startChatStreamWithRetry(
				ctx, fallbackModel, chatManage.FallbackModelID, fallbackMessages, fallbackOpt,
			)
			if startErr == nil {
				logStreamFallbackActivation(ctx, chatManage, err)
				chatModel = fallbackModel
				opt = fallbackOpt
				chatMessages = fallbackMessages
				activeModelID = chatManage.FallbackModelID
				usingFallback = true
				responseChan = fallbackChan
				streamAttempt = fallbackAttempt
				err = nil
			} else {
				err = startErr
			}
		} else {
			err = fallbackErr
		}
	}
	if err != nil {
		pipelineError(ctx, "Stream", "model_call", map[string]interface{}{
			"chat_model": chatManage.ChatModelID,
			"error":      err.Error(),
		})
		return ErrModelCall.WithError(err)
	}
	if responseChan == nil {
		pipelineError(ctx, "Stream", "model_call", map[string]interface{}{
			"chat_model": chatManage.ChatModelID,
			"error":      "nil_channel",
		})
		return ErrModelCall.WithError(errors.New("chat stream returned nil channel"))
	}

	pipelineInfo(ctx, "Stream", "model_started", map[string]interface{}{
		"session_id": chatManage.SessionID,
	})

	// Start goroutine to consume channel and emit events directly.
	// reasoning_content is routed to EventAgentThought (SSE response_type=thinking)
	// and plain answer text to EventAgentFinalAnswer, matching the Agent pipeline.
	// The goroutine monitors ctx.Done() to avoid leaking when the context is cancelled
	// and the upstream channel is not closed promptly.
	go func() {
		thinkingID := fmt.Sprintf("%s-thinking", uuid.New().String()[:8])
		answerID := fmt.Sprintf("%s-answer", uuid.New().String()[:8])
		thinkingOpen := false
		var answerBuilder strings.Builder
		answerDone := false
		terminalErrorEmitted := false
		outputStarted := false

		activateFallback := func(cause error) bool {
			if usingFallback || outputStarted || chatManage.FallbackModelID == "" ||
				!chat.ShouldFailover(ctx, cause) {
				return false
			}
			fallbackModel, fallbackOpt, fallbackErr := prepareFallbackChatModel(ctx, p.modelService, chatManage)
			if fallbackErr != nil {
				pipelineError(ctx, "Stream", "fallback_model_init_failed", map[string]interface{}{
					"fallback_model": chatManage.FallbackModelID,
					"error":          fallbackErr.Error(),
				})
				return false
			}
			fallbackMessages := prepareMessagesWithHistoryForVision(chatManage, chatManage.FallbackSupportsVision)
			fallbackChan, fallbackAttempt, fallbackErr := startChatStreamWithRetry(
				ctx, fallbackModel, chatManage.FallbackModelID, fallbackMessages, fallbackOpt,
			)
			if fallbackErr != nil || fallbackChan == nil {
				if fallbackErr != nil {
					pipelineError(ctx, "Stream", "fallback_model_call_failed", map[string]interface{}{
						"fallback_model": chatManage.FallbackModelID,
						"error":          fallbackErr.Error(),
					})
				}
				return false
			}
			logStreamFallbackActivation(ctx, chatManage, cause)
			chatModel = fallbackModel
			opt = fallbackOpt
			chatMessages = fallbackMessages
			activeModelID = chatManage.FallbackModelID
			usingFallback = true
			responseChan = fallbackChan
			streamAttempt = fallbackAttempt
			return true
		}

		restartCurrentModel := func(cause error, retryCurrent bool) bool {
			if retryCurrent {
				for streamAttempt < chatCompletionMaxAttempts {
					if !sleepBeforeChatRetry(ctx, streamAttempt) {
						return false
					}
					streamAttempt++
					nextChan, restartErr := chatModel.ChatStream(ctx, chatMessages, opt)
					if restartErr == nil && nextChan != nil {
						responseChan = nextChan
						pipelineWarn(ctx, "Stream", "stream_retry", map[string]interface{}{
							"chat_model": activeModelID,
							"attempt":    streamAttempt,
							"max":        chatCompletionMaxAttempts,
						})
						return true
					}
					if restartErr != nil {
						cause = restartErr
						if !isRetryableChatModelError(ctx, restartErr) {
							break
						}
					}
				}
			}
			return activateFallback(cause)
		}

		closeThinking := func() {
			if !thinkingOpen {
				return
			}
			eventBus.Emit(ctx, types.Event{
				ID:        thinkingID,
				Type:      types.EventType(event.EventAgentThought),
				SessionID: chatManage.SessionID,
				Data: event.AgentThoughtData{
					Done: true,
				},
			})
			thinkingOpen = false
		}

		emitTerminalError := func(errorCode, detail string) {
			if terminalErrorEmitted {
				return
			}
			terminalErrorEmitted = true
			closeThinking()
			userMessage := streamErrorUserMessage(chatManage.Language, answerBuilder.Len() > 0)
			eventBus.Emit(ctx, types.Event{
				ID:        fmt.Sprintf("%s-error", uuid.New().String()[:8]),
				Type:      types.EventType(event.EventError),
				SessionID: chatManage.SessionID,
				Data: event.ErrorData{
					Error:     userMessage,
					ErrorCode: errorCode,
					Stage:     "chat_completion_stream",
					SessionID: chatManage.SessionID,
					Extra: map[string]interface{}{
						"detail":   detail,
						"terminal": true,
					},
				},
			})
		}

	streamLoop:
		for {
			select {
			case <-ctx.Done():
				closeThinking()
				pipelineInfo(ctx, "Stream", "context_cancelled", map[string]interface{}{
					"session_id": chatManage.SessionID,
				})
				return

			case response, ok := <-responseChan:
				if !ok {
					closeThinking()
					pipelineInfo(ctx, "Stream", "channel_close", map[string]interface{}{
						"session_id": chatManage.SessionID,
					})
					if !answerDone && !terminalErrorEmitted && ctx.Err() == nil {
						pipelineWarn(ctx, "Stream", "channel_closed_without_done", map[string]interface{}{
							"session_id": chatManage.SessionID,
							"answer_len": answerBuilder.Len(),
						})
						if !outputStarted && restartCurrentModel(
							errors.New("model stream closed before sending a done event"), true,
						) {
							continue streamLoop
						}
						emitTerminalError("chat_stream_closed", "model stream closed before sending a done event")
					}
					return
				}

				if response.ResponseType == types.ResponseTypeError {
					pipelineError(ctx, "Stream", "stream_error", map[string]interface{}{
						"session_id": chatManage.SessionID,
						"error":      response.Content,
					})
					streamErr := errors.New(response.Content)
					if !outputStarted && restartCurrentModel(
						streamErr, isRetryableChatModelError(ctx, streamErr),
					) {
						continue streamLoop
					}
					emitTerminalError("chat_stream_error", response.Content)
					return
				}

				if response.ResponseType == types.ResponseTypeThinking {
					if response.Content != "" {
						outputStarted = true
						thinkingOpen = true
						eventBus.Emit(ctx, types.Event{
							ID:        thinkingID,
							Type:      types.EventType(event.EventAgentThought),
							SessionID: chatManage.SessionID,
							Data: event.AgentThoughtData{
								Content: response.Content,
								Done:    false,
							},
						})
					}
					if response.Done {
						closeThinking()
					}
					continue
				}

				if response.ResponseType == types.ResponseTypeAnswer {
					closeThinking()
					if response.Content != "" {
						outputStarted = true
						answerBuilder.WriteString(response.Content)
					}
					if response.Done && strings.TrimSpace(answerBuilder.String()) == "" {
						pipelineWarn(ctx, "Stream", "empty_answer_done", map[string]interface{}{
							"session_id":    chatManage.SessionID,
							"finish_reason": response.FinishReason,
						})
						emitTerminalError("chat_stream_empty_answer", "model stream finished without answer content")
						return
					}
					if response.Done {
						answerDone = true
						warnIfAnswerMissingKBCitations(ctx, "Stream", chatManage, answerBuilder.String())
					}
					eventBus.Emit(ctx, types.Event{
						ID:        answerID,
						Type:      types.EventType(event.EventAgentFinalAnswer),
						SessionID: chatManage.SessionID,
						Data: event.AgentFinalAnswerData{
							Content: response.Content,
							Done:    response.Done,
						},
					})
				}
			}
		}
	}()

	return next()
}

func startChatStreamWithRetry(
	ctx context.Context,
	model chat.Chat,
	modelID string,
	messages []chat.Message,
	opt *chat.ChatOptions,
) (<-chan types.StreamResponse, int, error) {
	var responseChan <-chan types.StreamResponse
	var err error
	attempt := 1
	for ; attempt <= chatCompletionMaxAttempts; attempt++ {
		responseChan, err = model.ChatStream(ctx, messages, opt)
		if err == nil && responseChan == nil {
			err = errors.New("chat stream returned nil channel")
		}
		if err == nil || !isRetryableChatModelError(ctx, err) || attempt == chatCompletionMaxAttempts {
			break
		}
		pipelineWarn(ctx, "Stream", "model_call_retry", map[string]interface{}{
			"chat_model": modelID,
			"attempt":    attempt,
			"max":        chatCompletionMaxAttempts,
			"error":      err.Error(),
		})
		if !sleepBeforeChatRetry(ctx, attempt) {
			break
		}
	}
	return responseChan, attempt, err
}

func logStreamFallbackActivation(ctx context.Context, chatManage *types.ChatManage, cause error) {
	detail := "primary model unavailable"
	if cause != nil {
		detail = cause.Error()
	}
	pipelineWarn(ctx, "Stream", "fallback_model_activate", map[string]interface{}{
		"primary_model":  chatManage.ChatModelID,
		"fallback_model": chatManage.FallbackModelID,
		"error":          detail,
	})
}

func streamErrorUserMessage(language string, hasPartialAnswer bool) string {
	if strings.Contains(strings.ToLower(language), "chinese") || strings.HasPrefix(strings.ToLower(language), "zh") {
		if hasPartialAnswer {
			return "回答生成中断：模型服务暂时不可用或连接中断，请稍后重试。"
		}
		return "回答生成失败：模型服务暂时不可用或连接中断，请稍后重试。"
	}
	if hasPartialAnswer {
		return "Answer generation was interrupted because the model service was unavailable or the connection was closed. Please try again later."
	}
	return "Answer generation failed because the model service was unavailable or the connection was closed. Please try again later."
}
