package telegram

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/im"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestParseTelegramMessage_ForumTopicThread(t *testing.T) {
	msg := &telegramMsg{
		MessageID:       100,
		MessageThreadID: 42, // Forum topic ID
		From:            &telegramUser{ID: 1001, FirstName: "Alice"},
		Chat:            telegramChat{ID: -9999, Type: "supergroup"},
		Text:            "hello",
	}

	incoming := parseTelegramMessage(msg)
	if incoming == nil {
		t.Fatal("expected non-nil message")
	}

	if incoming.ThreadID != "42" {
		t.Errorf("ThreadID = %q, want %q", incoming.ThreadID, "42")
	}
}

func TestParseTelegramMessage_NonForumGroup(t *testing.T) {
	msg := &telegramMsg{
		MessageID:       200,
		MessageThreadID: 0, // not a Forum group
		From:            &telegramUser{ID: 1002, FirstName: "Bob"},
		Chat:            telegramChat{ID: -8888, Type: "group"},
		Text:            "hello",
	}

	incoming := parseTelegramMessage(msg)
	if incoming == nil {
		t.Fatal("expected non-nil message")
	}

	// Non-Forum groups: ThreadID should be empty
	if incoming.ThreadID != "" {
		t.Errorf("ThreadID = %q, want empty for non-Forum group", incoming.ThreadID)
	}
}

func TestParseTelegramMessage_DirectMessage(t *testing.T) {
	msg := &telegramMsg{
		MessageID: 300,
		From:      &telegramUser{ID: 1003, FirstName: "Carol"},
		Chat:      telegramChat{ID: 1003, Type: "private"},
		Text:      "hi bot",
	}

	incoming := parseTelegramMessage(msg)
	if incoming == nil {
		t.Fatal("expected non-nil message")
	}

	if incoming.ThreadID != "" {
		t.Errorf("ThreadID = %q, want empty for DM", incoming.ThreadID)
	}
	if incoming.ChatType != im.ChatTypeDirect {
		t.Errorf("ChatType = %q, want %q", incoming.ChatType, im.ChatTypeDirect)
	}
}

func TestParseTelegramMessage_NilMessage(t *testing.T) {
	incoming := parseTelegramMessage(nil)
	if incoming != nil {
		t.Error("expected nil for nil message")
	}
}

func TestParseTelegramMessage_Document(t *testing.T) {
	msg := &telegramMsg{
		MessageID:       400,
		MessageThreadID: 7, // in a Forum topic
		From:            &telegramUser{ID: 1004, FirstName: "Dave"},
		Chat:            telegramChat{ID: -7777, Type: "supergroup"},
		Document: &telegramDoc{
			FileID:   "doc-123",
			FileName: "report.pdf",
			FileSize: 2048,
		},
	}

	incoming := parseTelegramMessage(msg)
	if incoming == nil {
		t.Fatal("expected non-nil message")
	}

	if incoming.ThreadID != "7" {
		t.Errorf("ThreadID = %q, want %q", incoming.ThreadID, "7")
	}
	if incoming.MessageType != im.MessageTypeFile {
		t.Errorf("MessageType = %q, want %q", incoming.MessageType, im.MessageTypeFile)
	}
}

func TestParseUpdate_NilMessage(t *testing.T) {
	update := &telegramUpdate{UpdateID: 1, Message: nil}
	incoming := parseUpdate(update)
	if incoming != nil {
		t.Error("expected nil for nil update.Message")
	}
}

func TestSendImage_MultipartPhotoUpload(t *testing.T) {
	oldClient := httpClient
	defer func() { httpClient = oldClient }()

	fields := map[string]string{}
	var photoData string
	var photoFileName string

	httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if req.URL.Path != "/bottest-token/sendPhoto" {
			t.Fatalf("path = %s", req.URL.Path)
		}

		mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("parse content type: %v", err)
		}
		if !strings.HasPrefix(mediaType, "multipart/") {
			t.Fatalf("mediaType = %s, want multipart", mediaType)
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
			if part.FormName() == "photo" {
				photoData = string(data)
				photoFileName = part.FileName()
			} else {
				fields[part.FormName()] = string(data)
			}
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{}}`)),
			Header:     make(http.Header),
		}, nil
	})}

	adapter := &Adapter{botToken: "test-token"}
	incoming := &im.IncomingMessage{
		UserID:   "42",
		ThreadID: "7",
	}
	err := adapter.SendImage(context.Background(), incoming, &im.OutboundImage{
		FileName: "figure.png",
		Caption:  "FIG. 14-7",
		Data:     []byte("png-bytes"),
	})
	if err != nil {
		t.Fatalf("SendImage error: %v", err)
	}

	if fields["chat_id"] != "42" {
		t.Errorf("chat_id = %q", fields["chat_id"])
	}
	if fields["message_thread_id"] != "7" {
		t.Errorf("message_thread_id = %q", fields["message_thread_id"])
	}
	if fields["caption"] != "FIG. 14-7" {
		t.Errorf("caption = %q", fields["caption"])
	}
	if photoFileName != "figure.png" {
		t.Errorf("photo filename = %q", photoFileName)
	}
	if photoData != "png-bytes" {
		t.Errorf("photo data = %q", photoData)
	}
}
