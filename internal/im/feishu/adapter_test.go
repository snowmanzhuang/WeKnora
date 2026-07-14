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
	a := &Adapter{}
	in := "hello **world** [link](https://example.com)"
	if got := a.resolveMarkdownImages(context.Background(), "tok", in); got != in {
		t.Errorf("content without image was modified: %q", got)
	}
}

func TestResolveMarkdownImages_FallbackToLinkOnFailure(t *testing.T) {
	a := &Adapter{}
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

func TestSendReply_WithInlineImageSendsStaticCard(t *testing.T) {
	oldClient := httpClient
	defer func() { httpClient = oldClient }()

	var paths []string
	var sentPayload map[string]interface{}
	httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.URL.Path)
		switch req.URL.Path {
		case "/open-apis/cardkit/v1/cards":
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read card body: %v", err)
			}
			if !bytes.Contains(body, []byte(`![眼底图](img_test_inline)`)) {
				t.Fatalf("card body does not contain inline image: %s", body)
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
