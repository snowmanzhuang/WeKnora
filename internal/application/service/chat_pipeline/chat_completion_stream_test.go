package chatpipeline

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type streamTestChatModel struct {
	responses       []types.StreamResponse
	streamSequences [][]types.StreamResponse
	streamErrs      []error
	streamCalls     int
	streamMessages  [][]chat.Message
	chatResponses   []*types.ChatResponse
	chatErrs        []error
	chatCalls       int
	chatMessages    [][]chat.Message
}

const streamTestResourceRef = "resource://AbCdEfGhIjKlMnOpQrStUv"

func (m *streamTestChatModel) Chat(
	_ context.Context,
	messages []chat.Message,
	_ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.chatMessages = append(m.chatMessages, append([]chat.Message(nil), messages...))
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
	_ context.Context,
	messages []chat.Message,
	_ *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	m.streamMessages = append(m.streamMessages, append([]chat.Message(nil), messages...))
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

func TestChatCompletionFallbackPreservesReferencesAndVisionAdaptation(t *testing.T) {
	disableChatRetrySleep(t)
	primary := &streamTestChatModel{chatErrs: []error{
		errors.New("API request failed with status 503: service unavailable"),
		errors.New("API request failed with status 503: service unavailable"),
		errors.New("API request failed with status 503: service unavailable"),
	}}
	fallback := &streamTestChatModel{chatResponses: []*types.ChatResponse{{
		Content:      `asset res://0001 <ref id="c1"/>`,
		FinishReason: "stop",
	}}}
	plugin := &PluginChatCompletion{modelService: &streamTestModelService{chatModels: map[string]chat.Chat{
		"model-1":        primary,
		"fallback-model": fallback,
	}}}
	cm := testChatManage(nil)
	cm.FallbackModelID = "fallback-model"
	cm.ChatModelSupportsVision = true
	cm.FallbackSupportsVision = false
	cm.Images = []string{"data:image/png;base64,AA=="}
	cm.ImageDescription = "a fallback-readable diagram"
	configureReferenceTestChatManage(cm)

	if err := plugin.OnEvent(context.Background(), types.CHAT_COMPLETION, cm, func() *PluginError {
		return nil
	}); err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}

	require.NotNil(t, cm.ChatResponse)
	require.Contains(t, cm.ChatResponse.Content, streamTestResourceRef)
	require.Contains(t, cm.ChatResponse.Content, `chunk_id="chunk-1"`)
	require.NotContains(t, cm.ChatResponse.Content, "res://0001")
	require.Len(t, fallback.chatMessages, 1)
	fallbackUserMessage := fallback.chatMessages[0][len(fallback.chatMessages[0])-1]
	require.Empty(t, fallbackUserMessage.Images)
	require.Contains(t, fallbackUserMessage.Content, "a fallback-readable diagram")
	require.Contains(t, fallbackUserMessage.Content, "res://0001")
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

func TestChatCompletionStreamRetryResetsHeldDecoderState(t *testing.T) {
	disableChatRetrySleep(t)
	model := &streamTestChatModel{streamSequences: [][]types.StreamResponse{
		{
			{ResponseType: types.ResponseTypeAnswer, Content: "res://0"},
			{ResponseType: types.ResponseTypeError, Content: "status 504: gateway timeout", Done: true},
		},
		{
			{ResponseType: types.ResponseTypeAnswer, Content: "res://0001", Done: true},
		},
	}}

	answer := runStreamPluginAndWaitForAnswerWithService(
		t,
		&streamTestModelService{chatModel: model},
		func(cm *types.ChatManage) { cm.UserContent = streamTestResourceRef },
	)

	require.Equal(t, streamTestResourceRef, answer)
	require.Equal(t, 2, model.streamCalls)
}

func TestChatCompletionStreamFallbackUsesFallbackRegistries(t *testing.T) {
	disableChatRetrySleep(t)
	primaryError := types.StreamResponse{
		ResponseType: types.ResponseTypeError,
		Content:      "status 503: service unavailable",
		Done:         true,
	}
	primary := &streamTestChatModel{streamSequences: [][]types.StreamResponse{
		{primaryError}, {primaryError}, {primaryError},
	}}
	fallback := &streamTestChatModel{responses: []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer,
		Content:      `asset res://0001 <ref id="c1"/>`,
		Done:         true,
	}}}
	service := &streamTestModelService{chatModels: map[string]chat.Chat{
		"model-1":        primary,
		"fallback-model": fallback,
	}}

	answer := runStreamPluginAndWaitForAnswerWithService(t, service, func(cm *types.ChatManage) {
		cm.FallbackModelID = "fallback-model"
		configureReferenceTestChatManage(cm)
	})

	require.Contains(t, answer, streamTestResourceRef)
	require.Contains(t, answer, `chunk_id="chunk-1"`)
	require.NotContains(t, answer, "res://0001")
	require.Equal(t, 3, primary.streamCalls)
	require.Equal(t, 1, fallback.streamCalls)
	require.Len(t, fallback.streamMessages, 1)
	require.Contains(t, fallback.streamMessages[0][len(fallback.streamMessages[0])-1].Content, "res://0001")
}

func TestChatCompletionStreamDoneFlushesTailBeforeEmptyCheck(t *testing.T) {
	model := &streamTestChatModel{responses: []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer,
		Content:      "res://0",
		Done:         true,
	}}}

	answer := runStreamPluginAndWaitForAnswerWithService(
		t,
		&streamTestModelService{chatModel: model},
		func(cm *types.ChatManage) { cm.UserContent = streamTestResourceRef },
	)

	require.Equal(t, "res://0", answer)
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

func configureReferenceTestChatManage(chatManage *types.ChatManage) {
	chatManage.UserContent = "inspect " + streamTestResourceRef
	chatManage.MergeResult = []*types.SearchResult{{
		ID:                "chunk-1",
		Content:           "grounded context",
		KnowledgeID:       "document-1",
		KnowledgeBaseID:   "kb-1",
		KnowledgeTitle:    "Guide",
		KnowledgeFilename: "guide.md",
	}}
}

// syncEventBus is a thread-safe recorder; the stream plugin emits from a
// background goroutine so the test must guard concurrent appends.
type syncEventBus struct {
	mu     sync.Mutex
	events []types.Event
}

func (b *syncEventBus) On(types.EventType, types.EventHandler) {}

func (b *syncEventBus) Emit(_ context.Context, evt types.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, evt)
	return nil
}

func (b *syncEventBus) finalAnswerContents() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, evt := range b.events {
		if evt.Type != types.EventType(event.EventAgentFinalAnswer) {
			continue
		}
		if data, ok := evt.Data.(event.AgentFinalAnswerData); ok {
			out = append(out, data.Content)
		}
	}
	return out
}

// openStreamChat returns a buffered channel preloaded with chunks and never
// closes it, so the stream plugin blocks on the channel until ctx is cancelled
// — deterministically exercising the ctx.Done() branch.
type openStreamChat struct {
	chunks []types.StreamResponse
}

func (m *openStreamChat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	return nil, nil
}

func (m *openStreamChat) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	ch := make(chan types.StreamResponse, len(m.chunks))
	for _, c := range m.chunks {
		ch <- c
	}
	return ch, nil // intentionally left open
}

func (m *openStreamChat) GetModelName() string { return "mock" }
func (m *openStreamChat) GetModelID() string   { return "mock" }

// stubModelService only needs GetChatModel; the rest is unused for this test.
type stubModelService struct {
	interfaces.ModelService
	model chat.Chat
}

func (s *stubModelService) GetChatModel(context.Context, string) (chat.Chat, error) {
	return s.model, nil
}

// TestStreamFlushesHeldAliasOnCancel verifies that when the request is cancelled
// mid-stream, the decoder's held-back alias suffix is flushed (emitted) rather
// than silently dropped. Without the ctx.Done() flush, "res://0" would be lost.
func TestStreamFlushesHeldAliasOnCancel(t *testing.T) {
	const ref = "resource://AbCdEfGhIjKlMnOpQrStUv"
	bus := &syncEventBus{}
	model := &openStreamChat{chunks: []types.StreamResponse{
		// Ends with a partial alias prefix ("res://0"), so the stream decoder
		// holds it back waiting for the rest that never arrives before cancel.
		{ResponseType: types.ResponseTypeAnswer, Content: "hello res://0"},
	}}

	chatManage := &types.ChatManage{}
	chatManage.SessionID = "sess-cancel"
	chatManage.UserContent = ref // seeds the registry so res://0001 becomes a known alias
	chatManage.EventBus = bus

	ctx, cancel := context.WithCancel(context.Background())
	plugin := &PluginChatCompletionStream{modelService: &stubModelService{model: model}}
	require.Nil(t, plugin.OnEvent(ctx, types.CHAT_COMPLETION_STREAM, chatManage, func() *PluginError { return nil }))

	// Wait until the pre-hold content has been emitted, then cancel.
	require.Eventually(t, func() bool {
		for _, c := range bus.finalAnswerContents() {
			if c == "hello " {
				return true
			}
		}
		return false
	}, 2*time.Second, 5*time.Millisecond)

	cancel()

	// After cancel, the held "res://0" suffix must be flushed as a final-answer chunk.
	require.Eventually(t, func() bool {
		for _, c := range bus.finalAnswerContents() {
			if c == "res://0" {
				return true
			}
		}
		return false
	}, 2*time.Second, 5*time.Millisecond)
}
