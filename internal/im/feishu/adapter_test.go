package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/im"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestDoFeishuRequest_RetriesTransientFailureWithReplayableBody(t *testing.T) {
	oldClient := httpClient
	oldDelay := feishuRetryBaseDelay
	defer func() {
		httpClient = oldClient
		feishuRetryBaseDelay = oldDelay
	}()
	feishuRetryBaseDelay = 0

	attempts := 0
	var bodies []string
	httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		bodies = append(bodies, string(body))
		if attempts < 3 {
			return nil, io.ErrUnexpectedEOF
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"code":0}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	req, err := http.NewRequest(http.MethodPost, "https://open.feishu.test/retry", strings.NewReader("same-body"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := doFeishuRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("doFeishuRequest: %v", err)
	}
	defer resp.Body.Close()

	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if err := resp.Request.Context().Err(); err != nil {
		t.Fatalf("successful response context was canceled before body read: %v", err)
	}
	for i, body := range bodies {
		if body != "same-body" {
			t.Errorf("body on attempt %d = %q", i+1, body)
		}
	}
}

func TestShouldUseFeishuStaticCard_DoesNotTreatPlainMathAsMarkdown(t *testing.T) {
	if shouldUseFeishuStaticCard("计算结果是 3*4=12") {
		t.Fatal("plain multiplication should remain a normal text message")
	}
}

func TestDoFeishuRequest_DoesNotRetryClientError(t *testing.T) {
	oldClient := httpClient
	oldDelay := feishuRetryBaseDelay
	defer func() {
		httpClient = oldClient
		feishuRetryBaseDelay = oldDelay
	}()
	feishuRetryBaseDelay = 0

	attempts := 0
	httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(`{"code":400}`)),
			Header:     make(http.Header),
		}, nil
	})}

	req, err := http.NewRequest(http.MethodGet, "https://open.feishu.test/no-retry", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := doFeishuRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("doFeishuRequest: %v", err)
	}
	defer resp.Body.Close()
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestFeishuThreadID_ThreadedReply(t *testing.T) {
	// Simulate: message is a reply in a thread (root_id is set)
	msg := &feishuMessage{
		MessageID: "msg-reply-1",
		RootID:    "msg-root-1",
		ParentID:  "msg-parent-1",
	}

	threadID := msg.RootID
	if threadID == "" {
		threadID = msg.MessageID
	}

	if threadID != "msg-root-1" {
		t.Errorf("threadID = %q, want %q", threadID, "msg-root-1")
	}
}

func TestFeishuThreadID_TopLevelMessage(t *testing.T) {
	// Simulate: top-level message (root_id is empty)
	msg := &feishuMessage{
		MessageID: "msg-top-1",
		RootID:    "",
		ParentID:  "",
	}

	threadID := msg.RootID
	if threadID == "" {
		threadID = msg.MessageID
	}

	if threadID != "msg-top-1" {
		t.Errorf("threadID = %q, want %q (should use MessageID as fallback)", threadID, "msg-top-1")
	}
}

func TestFeishuMessageStruct_JSONFields(t *testing.T) {
	// Verify the struct fields exist and have correct zero values
	msg := feishuMessage{}
	if msg.RootID != "" {
		t.Errorf("RootID zero value = %q, want empty", msg.RootID)
	}
	if msg.ParentID != "" {
		t.Errorf("ParentID zero value = %q, want empty", msg.ParentID)
	}
	if msg.MessageID != "" {
		t.Errorf("MessageID zero value = %q, want empty", msg.MessageID)
	}
}

func TestImageCacheKey_StripsQuery(t *testing.T) {
	cases := map[string]string{
		"https://host/a.png?sig=1&t=2": "https://host/a.png",
		"https://host/a.png":           "https://host/a.png",
	}
	for in, want := range cases {
		if got := imageCacheKey(in); got != want {
			t.Errorf("imageCacheKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveMarkdownImages_NoImageUnchanged(t *testing.T) {
	a := &Adapter{region: RegionFeishu}
	in := "hello **world** [link](https://example.com)"
	if got := a.resolveMarkdownImages(context.Background(), "tok", in); got != in {
		t.Errorf("content without image was modified: %q", got)
	}
}

func TestResolveMarkdownImages_FallbackToLinkOnFailure(t *testing.T) {
	a := &Adapter{region: RegionFeishu}
	// A direct-IP loopback URL fails SSRF validation before any network call,
	// so the image must degrade to a plain markdown link (never left as ![]()).
	in := "see ![diagram](http://127.0.0.1/x.png) here"
	got := a.resolveMarkdownImages(context.Background(), "tok", in)
	if strings.Contains(got, "![") {
		t.Errorf("failed image should not remain as image markdown: %q", got)
	}
	if !strings.Contains(got, "[diagram](http://127.0.0.1/x.png)") {
		t.Errorf("expected link fallback with alt text, got: %q", got)
	}
}

func TestUploadInlineImage_UploadsBytesAndCachesImageKey(t *testing.T) {
	oldClient := httpClient
	defer func() { httpClient = oldClient }()

	uploadCalls := 0
	var uploadedFileName string
	var uploadedData string
	httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/open-apis/im/v1/images" {
			t.Fatalf("path = %s", req.URL.Path)
		}
		if req.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("authorization = %q", req.Header.Get("Authorization"))
		}
		uploadCalls++

		mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("parse content type: %v", err)
		}
		if !strings.HasPrefix(mediaType, "multipart/") {
			t.Fatalf("media type = %s", mediaType)
		}
		reader := multipart.NewReader(req.Body, params["boundary"])
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("next part: %v", err)
			}
			data, err := io.ReadAll(part)
			if err != nil {
				t.Fatalf("read part: %v", err)
			}
			if part.FormName() == "image" {
				uploadedFileName = part.FileName()
				uploadedData = string(data)
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"code":0,"msg":"ok","data":{"image_key":"img_test_inline"}}`)),
			Header:     make(http.Header),
		}, nil
	})}

	adapter := &Adapter{
		appID:      "inline-image-test-app",
		tokenCache: "test-token",
		tokenExpAt: time.Now().Add(time.Hour),
	}
	image := &im.OutboundImage{FileName: "retina.jpg", Data: []byte("jpeg-bytes")}

	first, err := adapter.UploadInlineImage(context.Background(), &im.IncomingMessage{}, image)
	if err != nil {
		t.Fatalf("UploadInlineImage first call: %v", err)
	}
	second, err := adapter.UploadInlineImage(context.Background(), &im.IncomingMessage{}, image)
	if err != nil {
		t.Fatalf("UploadInlineImage cached call: %v", err)
	}

	if first != "img_test_inline" || second != first {
		t.Fatalf("image keys = %q, %q", first, second)
	}
	if uploadCalls != 1 {
		t.Fatalf("upload calls = %d, want 1", uploadCalls)
	}
	if uploadedFileName != "retina.jpg" {
		t.Errorf("uploaded filename = %q", uploadedFileName)
	}
	if uploadedData != "jpeg-bytes" {
		t.Errorf("uploaded data = %q", uploadedData)
	}
}

func TestBuildStaticCardJSON_ContainsInlineImage(t *testing.T) {
	card := buildStaticCardJSON("前文\n\n![眼底图](img_test_inline)\n\n后文")
	if !strings.Contains(card, `![眼底图](img_test_inline)`) {
		t.Fatalf("card does not preserve inline image: %s", card)
	}
}

func TestNormalizeFeishuImageCaptionSpacing(t *testing.T) {
	longAlt := "Fig. 32 [A] (continued) posterior membrane-like structure"
	input := "正文\n\n![" + longAlt + "](img_test_inline)\n\n图示病例表现为角膜水肿。\n\n## 下一节"
	want := "正文\n\n![" + longAlt + "](img_test_inline)\n图示病例表现为角膜水肿。\n\n## 下一节"

	if got := normalizeFeishuImageCaptionSpacing(input); got != want {
		t.Fatalf("normalized content = %q, want %q", got, want)
	}
}

func TestNormalizeFeishuImageCaptionSpacing_PreservesStructuralBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		nextLine string
	}{
		{name: "heading", nextLine: "## 下一节"},
		{name: "list", nextLine: "- 下一项"},
		{name: "numbered list", nextLine: "1. 下一项"},
		{name: "table", nextLine: "| 项目 | 内容 |"},
		{name: "code fence", nextLine: "```text"},
		{name: "another image", nextLine: "![另一张](img_second)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := "![第一张](img_first)\n\n" + tt.nextLine
			if got := normalizeFeishuImageCaptionSpacing(input); got != input {
				t.Fatalf("structural spacing changed: got %q, want %q", got, input)
			}
		})
	}
}

func TestNormalizeFeishuImageCaptionSpacing_LeavesNonFeishuImageUntouched(t *testing.T) {
	input := "![图](resource://example)\n\n图注"
	if got := normalizeFeishuImageCaptionSpacing(input); got != input {
		t.Fatalf("unresolved image changed: got %q, want %q", got, input)
	}
}

func TestNormalizeFeishuImageCaptionSpacing_HandlesAdjacentImagesIndependently(t *testing.T) {
	input := "![第一张](img_first)\n\n![第二张](img_second)\n\n第二张的图注"
	want := "![第一张](img_first)\n\n![第二张](img_second)\n第二张的图注"
	if got := normalizeFeishuImageCaptionSpacing(input); got != want {
		t.Fatalf("normalized content = %q, want %q", got, want)
	}
}

func TestSendReply_WithInlineImageSendsStaticCard(t *testing.T) {
	oldClient := httpClient
	defer func() { httpClient = oldClient }()

	var paths []string
	var sentPayload map[string]interface{}
	httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.URL.Path)
		switch req.URL.Path {
		case "/open-apis/cardkit/v1/cards":
			var createPayload struct {
				Data string `json:"data"`
			}
			if err := json.NewDecoder(req.Body).Decode(&createPayload); err != nil {
				t.Fatalf("decode card create payload: %v", err)
			}
			var card struct {
				Body struct {
					Elements []struct {
						Content string `json:"content"`
					} `json:"elements"`
				} `json:"body"`
			}
			if err := json.Unmarshal([]byte(createPayload.Data), &card); err != nil {
				t.Fatalf("decode card JSON: %v", err)
			}
			if len(card.Body.Elements) != 1 {
				t.Fatalf("card elements = %d, want 1", len(card.Body.Elements))
			}
			wantContent := "前文\n\n![眼底图](img_test_inline)\n后文"
			if card.Body.Elements[0].Content != wantContent {
				t.Fatalf("card content = %q, want %q", card.Body.Elements[0].Content, wantContent)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"code":0,"msg":"ok","data":{"card_id":"card_test"}}`)),
				Header:     make(http.Header),
			}, nil
		case "/open-apis/im/v1/messages/message_test/reply":
			if err := json.NewDecoder(req.Body).Decode(&sentPayload); err != nil {
				t.Fatalf("decode send payload: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"code":0,"msg":"ok"}`)),
				Header:     make(http.Header),
			}, nil
		default:
			t.Fatalf("unexpected request path: %s", req.URL.Path)
			return nil, nil
		}
	})}

	adapter := &Adapter{
		appID:      "static-card-test-app",
		tokenCache: "test-token",
		tokenExpAt: time.Now().Add(time.Hour),
	}
	incoming := &im.IncomingMessage{
		MessageID: "message_test",
		UserID:    "user_test",
		ChatType:  im.ChatTypeDirect,
	}
	err := adapter.SendReply(context.Background(), incoming, &im.ReplyMessage{
		Content: "前文\n\n![眼底图](img_test_inline)\n\n后文",
		IsFinal: true,
	})
	if err != nil {
		t.Fatalf("SendReply: %v", err)
	}

	if len(paths) != 2 {
		t.Fatalf("request paths = %v", paths)
	}
	if sentPayload["msg_type"] != "interactive" {
		t.Fatalf("msg_type = %v", sentPayload["msg_type"])
	}
}

func TestSendReply_WithMarkdownSendsStaticCardWithoutImage(t *testing.T) {
	oldClient := httpClient
	defer func() { httpClient = oldClient }()

	var cardBody []byte
	var sentPayload map[string]interface{}
	httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/open-apis/cardkit/v1/cards":
			var err error
			cardBody, err = io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read card body: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"code":0,"msg":"ok","data":{"card_id":"card_markdown"}}`)),
				Header:     make(http.Header),
			}, nil
		case "/open-apis/im/v1/messages/message_markdown/reply":
			if err := json.NewDecoder(req.Body).Decode(&sentPayload); err != nil {
				t.Fatalf("decode send payload: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"code":0,"msg":"ok"}`)),
				Header:     make(http.Header),
			}, nil
		default:
			t.Fatalf("unexpected request path: %s", req.URL.Path)
			return nil, nil
		}
	})}

	adapter := &Adapter{
		appID:      "markdown-card-test-app",
		tokenCache: "test-token",
		tokenExpAt: time.Now().Add(time.Hour),
	}
	incoming := &im.IncomingMessage{MessageID: "message_markdown", UserID: "user_test", ChatType: im.ChatTypeDirect}
	content := "## 结论\n\n- **要点一**\n- 要点二"
	if err := adapter.SendReply(context.Background(), incoming, &im.ReplyMessage{Content: content, IsFinal: true}); err != nil {
		t.Fatalf("SendReply: %v", err)
	}

	if !bytes.Contains(cardBody, []byte(`## 结论`)) {
		t.Fatalf("card body does not contain markdown: %s", cardBody)
	}
	if sentPayload["msg_type"] != "interactive" {
		t.Fatalf("msg_type = %v, want interactive", sentPayload["msg_type"])
	}
}

func TestSendReply_StaticCardFailureFallsBackToReadableText(t *testing.T) {
	oldClient := httpClient
	defer func() { httpClient = oldClient }()

	var sentPayload map[string]interface{}
	httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/open-apis/cardkit/v1/cards":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"code":999,"msg":"card unavailable"}`)),
				Header:     make(http.Header),
			}, nil
		case "/open-apis/im/v1/messages/message_fallback/reply":
			if err := json.NewDecoder(req.Body).Decode(&sentPayload); err != nil {
				t.Fatalf("decode send payload: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"code":0,"msg":"ok"}`)),
				Header:     make(http.Header),
			}, nil
		default:
			t.Fatalf("unexpected request path: %s", req.URL.Path)
			return nil, nil
		}
	})}

	adapter := &Adapter{
		appID:      "markdown-fallback-test-app",
		tokenCache: "test-token",
		tokenExpAt: time.Now().Add(time.Hour),
	}
	incoming := &im.IncomingMessage{MessageID: "message_fallback", UserID: "user_test", ChatType: im.ChatTypeDirect}
	content := "## 结论\n\n- **要点**\n\n![" + strings.Repeat("很长的说明", 100) + "](img_failed)"
	if err := adapter.SendReply(context.Background(), incoming, &im.ReplyMessage{Content: content, IsFinal: true}); err != nil {
		t.Fatalf("SendReply: %v", err)
	}

	if sentPayload["msg_type"] != "text" {
		t.Fatalf("msg_type = %v, want text fallback", sentPayload["msg_type"])
	}
	var textContent struct {
		Text string `json:"text"`
	}
	raw, _ := sentPayload["content"].(string)
	if err := json.Unmarshal([]byte(raw), &textContent); err != nil {
		t.Fatalf("decode text content: %v", err)
	}
	if strings.Contains(textContent.Text, "##") || strings.Contains(textContent.Text, "**") || strings.Contains(textContent.Text, "img_failed") {
		t.Fatalf("fallback still contains raw markdown: %q", textContent.Text)
	}
	if !strings.Contains(textContent.Text, "结论") || !strings.Contains(textContent.Text, "- 要点") || !strings.Contains(textContent.Text, "图片暂时无法显示") {
		t.Fatalf("fallback lost readable content: %q", textContent.Text)
	}
	if len(textContent.Text) > 100 {
		t.Fatalf("fallback retained the long image alt: length=%d text=%q", len(textContent.Text), textContent.Text)
	}
}

// An image with no alt text falls back to the region's own label, so Lark users
// do not get a Chinese link label.
func TestResolveMarkdownImages_FallbackLabelFollowsRegion(t *testing.T) {
	for _, region := range []Region{RegionFeishu, RegionLark} {
		a := &Adapter{region: region}
		got := a.resolveMarkdownImages(context.Background(), "tok", "![](http://127.0.0.1/x.png)")
		want := "[" + region.ImageFallbackLabel + "](http://127.0.0.1/x.png)"
		if got != want {
			t.Errorf("%s fallback = %q, want %q", region.Label, got, want)
		}
	}
}

func TestEndStreamDisablesStreamingAndUpdatesSummary(t *testing.T) {
	oldClient := httpClient
	defer func() { httpClient = oldClient }()

	const streamID = "card_complete"
	feishuStreamsMu.Lock()
	feishuStreams[streamID] = &feishuStreamState{createdAt: time.Now()}
	feishuStreamsMu.Unlock()
	t.Cleanup(func() {
		feishuStreamsMu.Lock()
		delete(feishuStreams, streamID)
		feishuStreamsMu.Unlock()
	})

	var gotPayload struct {
		Settings string `json:"settings"`
		Sequence int    `json:"sequence"`
	}
	httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPatch || req.URL.Path != "/open-apis/cardkit/v1/cards/card_complete/settings" {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode settings payload: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"code":0,"msg":"ok"}`)),
			Header:     make(http.Header),
		}, nil
	})}

	adapter := &Adapter{
		region:     RegionFeishu,
		tokenCache: "test-token",
		tokenExpAt: time.Now().Add(time.Hour),
	}
	if err := adapter.EndStream(context.Background(), &im.IncomingMessage{}, streamID); err != nil {
		t.Fatalf("EndStream: %v", err)
	}

	var settings struct {
		Config struct {
			StreamingMode *bool `json:"streaming_mode"`
			Summary       struct {
				Content string `json:"content"`
			} `json:"summary"`
		} `json:"config"`
	}
	if err := json.Unmarshal([]byte(gotPayload.Settings), &settings); err != nil {
		t.Fatalf("decode card settings: %v", err)
	}
	if settings.Config.StreamingMode == nil || *settings.Config.StreamingMode {
		t.Fatalf("streaming_mode = %v, want false", settings.Config.StreamingMode)
	}
	if settings.Config.Summary.Content != RegionFeishu.CompletedText {
		t.Fatalf("summary = %q, want %q", settings.Config.Summary.Content, RegionFeishu.CompletedText)
	}
	if gotPayload.Sequence != 1 {
		t.Fatalf("sequence = %d, want 1", gotPayload.Sequence)
	}

	feishuStreamsMu.Lock()
	_, stillTracked := feishuStreams[streamID]
	feishuStreamsMu.Unlock()
	if stillTracked {
		t.Fatal("completed stream was not removed from local state")
	}
}

func TestEndStreamReturnsSettingsFailureAndKeepsState(t *testing.T) {
	oldClient := httpClient
	defer func() { httpClient = oldClient }()

	const streamID = "card_settings_failure"
	feishuStreamsMu.Lock()
	feishuStreams[streamID] = &feishuStreamState{createdAt: time.Now()}
	feishuStreamsMu.Unlock()
	t.Cleanup(func() {
		feishuStreamsMu.Lock()
		delete(feishuStreams, streamID)
		feishuStreamsMu.Unlock()
	})

	httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"code":999,"msg":"settings rejected"}`)),
			Header:     make(http.Header),
		}, nil
	})}

	adapter := &Adapter{
		region:     RegionFeishu,
		tokenCache: "test-token",
		tokenExpAt: time.Now().Add(time.Hour),
	}
	if err := adapter.EndStream(context.Background(), &im.IncomingMessage{}, streamID); err == nil {
		t.Fatal("EndStream returned nil for rejected settings update")
	}

	feishuStreamsMu.Lock()
	_, stillTracked := feishuStreams[streamID]
	feishuStreamsMu.Unlock()
	if !stillTracked {
		t.Fatal("failed stream was removed before a retry could occur")
	}
}
