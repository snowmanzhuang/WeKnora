package chatpipeline

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
)

type streamTestChatModel struct {
	responses       []types.StreamResponse
	streamSequences [][]types.StreamResponse
	streamErrs      []error
	streamCalls     int
	chatResponses   []*types.ChatResponse
	chatErrs        []error
	chatCalls       int
}

func (m *streamTestChatModel) Chat(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (*types.ChatResponse, error) {
	call := m.chatCalls
	m.chatCalls++
	if call < len(m.chatErrs) && m.chatErrs[call] != nil {
		return nil, m.chatErrs[call]
	}
	if call < len(m.chatResponses) && m.chatResponses[call] != nil {
		return m.chatResponses[call], nil
	}
	return nil, nil
}

func (m *streamTestChatModel) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	call := m.streamCalls
	m.streamCalls++
	if call < len(m.streamErrs) && m.streamErrs[call] != nil {
		return nil, m.streamErrs[call]
	}
	responses := m.responses
	if call < len(m.streamSequences) {
		responses = m.streamSequences[call]
	}
	ch := make(chan types.StreamResponse, len(responses))
	for _, response := range responses {
		ch <- response
	}
	close(ch)
	return ch, nil
}

func (m *streamTestChatModel) GetModelName() string { return "stream-test" }
func (m *streamTestChatModel) GetModelID() string   { return "stream-test" }

type streamTestModelService struct {
	chatModel  chat.Chat
	chatModels map[string]chat.Chat
}

func (s *streamTestModelService) CreateModel(context.Context, *types.Model) error { return nil }
func (s *streamTestModelService) GetModelByID(context.Context, string) (*types.Model, error) {
	return nil, nil
}
func (s *streamTestModelService) ListModels(context.Context) ([]*types.Model, error) { return nil, nil }
func (s *streamTestModelService) UpdateModel(context.Context, *types.Model) error    { return nil }
func (s *streamTestModelService) DeleteModel(context.Context, string) error          { return nil }
func (s *streamTestModelService) UpdateModelCredentials(
	context.Context,
	string,
	*string,
	*string,
) (*types.Model, error) {
	return nil, nil
}
func (s *streamTestModelService) ClearModelCredential(context.Context, string, string) error {
	return nil
}
func (s *streamTestModelService) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return nil, nil
}
func (s *streamTestModelService) GetEmbeddingModelForTenant(
	context.Context,
	string,
	uint64,
) (embedding.Embedder, error) {
	return nil, nil
}
func (s *streamTestModelService) GetRerankModel(context.Context, string) (rerank.Reranker, error) {
	return nil, nil
}
func (s *streamTestModelService) GetChatModel(_ context.Context, modelID string) (chat.Chat, error) {
	if s.chatModels != nil {
		model, ok := s.chatModels[modelID]
		if !ok {
			return nil, errors.New("chat model not found: " + modelID)
		}
		return model, nil
	}
	return s.chatModel, nil
}
func (s *streamTestModelService) GetVLMModel(context.Context, string) (vlm.VLM, error) {
	return nil, nil
}
func (s *streamTestModelService) GetASRModel(context.Context, string) (asr.ASR, error) {
	return nil, nil
}

func TestChatCompletionStreamEmptyDoneEmitsTerminalError(t *testing.T) {
	errData := runStreamPluginAndWaitForError(t, []types.StreamResponse{
		{
			ResponseType: types.ResponseTypeAnswer,
			Done:         true,
			FinishReason: "stop",
		},
	})

	if errData.ErrorCode != "chat_stream_empty_answer" {
		t.Fatalf("ErrorCode = %q, want chat_stream_empty_answer", errData.ErrorCode)
	}
	if !strings.Contains(errData.Error, "回答生成失败") {
		t.Fatalf("Error = %q, want Chinese failure message", errData.Error)
	}
}

func TestChatCompletionStreamClosedWithoutDoneEmitsTerminalError(t *testing.T) {
	errData := runStreamPluginAndWaitForError(t, []types.StreamResponse{
		{
			ResponseType: types.ResponseTypeAnswer,
			Content:      "部分答案",
			Done:         false,
		},
	})

	if errData.ErrorCode != "chat_stream_closed" {
		t.Fatalf("ErrorCode = %q, want chat_stream_closed", errData.ErrorCode)
	}
	if !strings.Contains(errData.Error, "回答生成中断") {
		t.Fatalf("Error = %q, want Chinese interruption message", errData.Error)
	}
}

func TestChatCompletionRetriesTransientError(t *testing.T) {
	disableChatRetrySleep(t)
	model := &streamTestChatModel{
		chatErrs: []error{
			errors.New("API request failed with status 503: service unavailable"),
		},
		chatResponses: []*types.ChatResponse{
			nil,
			{Content: "重试后答案", FinishReason: "stop"},
		},
	}
	plugin := &PluginChatCompletion{
		modelService: &streamTestModelService{chatModel: model},
	}
	cm := testChatManage(nil)

	if err := plugin.OnEvent(context.Background(), types.CHAT_COMPLETION, cm, func() *PluginError {
		return nil
	}); err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}

	if model.chatCalls != 2 {
		t.Fatalf("Chat calls = %d, want 2", model.chatCalls)
	}
	if cm.ChatResponse == nil || cm.ChatResponse.Content != "重试后答案" {
		t.Fatalf("ChatResponse = %#v, want retry response", cm.ChatResponse)
	}
}

func TestChatCompletionDoesNotRetryPermanentError(t *testing.T) {
	disableChatRetrySleep(t)
	model := &streamTestChatModel{
		chatErrs: []error{
			errors.New("API request failed with status 403: forbidden"),
		},
	}
	plugin := &PluginChatCompletion{
		modelService: &streamTestModelService{chatModel: model},
	}
	cm := testChatManage(nil)

	err := plugin.OnEvent(context.Background(), types.CHAT_COMPLETION, cm, func() *PluginError {
		return nil
	})

	if err == nil {
		t.Fatal("OnEvent returned nil error, want permanent model error")
	}
	if model.chatCalls != 1 {
		t.Fatalf("Chat calls = %d, want 1", model.chatCalls)
	}
}

func TestChatCompletionFallsBackAfterPrimaryRetries(t *testing.T) {
	disableChatRetrySleep(t)
	primary := &streamTestChatModel{
		chatErrs: []error{
			errors.New("API request failed with status 503: service unavailable"),
			errors.New("API request failed with status 503: service unavailable"),
			errors.New("API request failed with status 503: service unavailable"),
		},
	}
	fallback := &streamTestChatModel{
		chatResponses: []*types.ChatResponse{{Content: "备用模型答案", FinishReason: "stop"}},
	}
	plugin := &PluginChatCompletion{
		modelService: &streamTestModelService{chatModels: map[string]chat.Chat{
			"model-1":        primary,
			"fallback-model": fallback,
		}},
	}
	cm := testChatManage(nil)
	cm.FallbackModelID = "fallback-model"

	if err := plugin.OnEvent(context.Background(), types.CHAT_COMPLETION, cm, func() *PluginError {
		return nil
	}); err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}

	assertChatCalls(t, primary, 3)
	assertChatCalls(t, fallback, 1)
	if cm.ChatResponse == nil || cm.ChatResponse.Content != "备用模型答案" {
		t.Fatalf("ChatResponse = %#v, want fallback response", cm.ChatResponse)
	}
}

func TestChatCompletionStreamRetriesTransientErrorBeforeOutput(t *testing.T) {
	disableChatRetrySleep(t)
	model := &streamTestChatModel{
		streamSequences: [][]types.StreamResponse{
			{
				{
					ResponseType: types.ResponseTypeError,
					Content:      "API request failed with status 504: gateway timeout",
					Done:         true,
				},
			},
			{
				{
					ResponseType: types.ResponseTypeAnswer,
					Content:      "重试后流式答案",
					Done:         false,
				},
				{
					ResponseType: types.ResponseTypeAnswer,
					Done:         true,
					FinishReason: "stop",
				},
			},
		},
	}

	answer := runStreamPluginAndWaitForAnswer(t, model)

	if model.streamCalls != 2 {
		t.Fatalf("ChatStream calls = %d, want 2", model.streamCalls)
	}
	if answer != "重试后流式答案" {
		t.Fatalf("answer = %q, want retry answer", answer)
	}
}

func TestChatCompletionStreamFallsBackBeforeOutput(t *testing.T) {
	disableChatRetrySleep(t)
	primaryError := types.StreamResponse{
		ResponseType: types.ResponseTypeError,
		Content:      "API request failed with status 503: service unavailable",
		Done:         true,
	}
	primary := &streamTestChatModel{
		streamSequences: [][]types.StreamResponse{{primaryError}, {primaryError}, {primaryError}},
	}
	fallback := &streamTestChatModel{
		responses: []types.StreamResponse{
			{ResponseType: types.ResponseTypeAnswer, Content: "流式备用答案"},
			{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: "stop"},
		},
	}
	service := &streamTestModelService{chatModels: map[string]chat.Chat{
		"model-1":        primary,
		"fallback-model": fallback,
	}}

	answer := runStreamPluginAndWaitForAnswerWithService(t, service, func(cm *types.ChatManage) {
		cm.FallbackModelID = "fallback-model"
	})

	if primary.streamCalls != 3 {
		t.Fatalf("primary ChatStream calls = %d, want 3", primary.streamCalls)
	}
	if fallback.streamCalls != 1 {
		t.Fatalf("fallback ChatStream calls = %d, want 1", fallback.streamCalls)
	}
	if answer != "流式备用答案" {
		t.Fatalf("answer = %q, want fallback answer", answer)
	}
}

func TestChatCompletionStreamDoesNotFallbackAfterOutput(t *testing.T) {
	disableChatRetrySleep(t)
	primary := &streamTestChatModel{responses: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "已输出部分"},
		{ResponseType: types.ResponseTypeError, Content: "API request failed with status 503", Done: true},
	}}
	fallback := &streamTestChatModel{responses: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "不应输出", Done: true},
	}}
	service := &streamTestModelService{chatModels: map[string]chat.Chat{
		"model-1":        primary,
		"fallback-model": fallback,
	}}

	bus := event.NewEventBus()
	errCh := make(chan event.ErrorData, 1)
	bus.On(event.EventError, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.ErrorData)
		if !ok {
			t.Fatalf("unexpected error event data type %T", evt.Data)
		}
		errCh <- data
		return nil
	})
	cm := testChatManage(bus.AsEventBusInterface())
	cm.FallbackModelID = "fallback-model"
	plugin := &PluginChatCompletionStream{modelService: service}

	if err := plugin.OnEvent(context.Background(), types.CHAT_COMPLETION_STREAM, cm, func() *PluginError {
		return nil
	}); err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for terminal error event")
	}

	if primary.streamCalls != 1 {
		t.Fatalf("primary ChatStream calls = %d, want 1", primary.streamCalls)
	}
	if fallback.streamCalls != 0 {
		t.Fatalf("fallback ChatStream calls = %d, want 0 after output started", fallback.streamCalls)
	}
}

func runStreamPluginAndWaitForError(
	t *testing.T,
	responses []types.StreamResponse,
) event.ErrorData {
	t.Helper()

	bus := event.NewEventBus()
	errCh := make(chan event.ErrorData, 1)
	bus.On(event.EventError, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.ErrorData)
		if !ok {
			t.Fatalf("unexpected error event data type %T", evt.Data)
		}
		errCh <- data
		return nil
	})

	plugin := &PluginChatCompletionStream{
		modelService: &streamTestModelService{
			chatModel: &streamTestChatModel{responses: responses},
		},
	}
	cm := testChatManage(bus.AsEventBusInterface())

	if err := plugin.OnEvent(context.Background(), types.CHAT_COMPLETION_STREAM, cm, func() *PluginError {
		return nil
	}); err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}

	select {
	case data := <-errCh:
		return data
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for terminal error event")
		return event.ErrorData{}
	}
}

func runStreamPluginAndWaitForAnswer(
	t *testing.T,
	model *streamTestChatModel,
) string {
	return runStreamPluginAndWaitForAnswerWithService(
		t,
		&streamTestModelService{chatModel: model},
		nil,
	)
}

func runStreamPluginAndWaitForAnswerWithService(
	t *testing.T,
	service *streamTestModelService,
	configure func(*types.ChatManage),
) string {
	t.Helper()

	bus := event.NewEventBus()
	answerCh := make(chan string, 1)
	errorCh := make(chan event.ErrorData, 1)
	var answer strings.Builder
	bus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentFinalAnswerData)
		if !ok {
			t.Fatalf("unexpected answer event data type %T", evt.Data)
		}
		answer.WriteString(data.Content)
		if data.Done {
			answerCh <- answer.String()
		}
		return nil
	})
	bus.On(event.EventError, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.ErrorData)
		if !ok {
			t.Fatalf("unexpected error event data type %T", evt.Data)
		}
		errorCh <- data
		return nil
	})

	plugin := &PluginChatCompletionStream{
		modelService: service,
	}
	cm := testChatManage(bus.AsEventBusInterface())
	if configure != nil {
		configure(cm)
	}

	if err := plugin.OnEvent(context.Background(), types.CHAT_COMPLETION_STREAM, cm, func() *PluginError {
		return nil
	}); err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}

	select {
	case answer := <-answerCh:
		return answer
	case errData := <-errorCh:
		t.Fatalf("unexpected stream error: %#v", errData)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for final answer event")
	}
	return ""
}

func assertChatCalls(t *testing.T, model *streamTestChatModel, want int) {
	t.Helper()
	if model.chatCalls != want {
		t.Fatalf("Chat calls = %d, want %d", model.chatCalls, want)
	}
}

func disableChatRetrySleep(t *testing.T) {
	t.Helper()
	original := chatCompletionRetrySleeper
	chatCompletionRetrySleeper = func(context.Context, time.Duration) bool { return true }
	t.Cleanup(func() {
		chatCompletionRetrySleeper = original
	})
}

func testChatManage(eventBus types.EventBusInterface) *types.ChatManage {
	return &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SessionID:   "session-1",
			ChatModelID: "model-1",
			Language:    "zh-CN",
			SummaryConfig: types.SummaryConfig{
				Prompt: "You are helpful.",
			},
		},
		PipelineState: types.PipelineState{
			UserContent: "你好",
		},
		PipelineContext: types.PipelineContext{
			EventBus: eventBus,
		},
	}
}
