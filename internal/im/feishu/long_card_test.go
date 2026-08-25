package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/im"
)

func TestSplitFeishuCardMarkdown_ShortContentUnchanged(t *testing.T) {
	input := "## 结论\n\n- **要点：**正文"
	parts := splitFeishuCardMarkdown(input, 1000)
	if len(parts) != 1 || parts[0] != input {
		t.Fatalf("short content changed: %#v", parts)
	}
}

func TestSplitFeishuCardMarkdown_PreservesAllContentAndStructuralBlocks(t *testing.T) {
	blocks := []string{
		"## 第一节\n\n",
		"- **诊断：**" + strings.Repeat("角膜水肿。", 30) + "\n\n",
		"## 第二节\n\n",
		"$$\\frac{1}{2}$$\n\n",
		"```markdown\n**这里不是加粗边界**\n```\n\n",
		"结尾内容完整。",
	}
	input := strings.Join(blocks, "")
	parts := splitFeishuCardMarkdown(input, 300)
	if len(parts) < 2 {
		t.Fatalf("long content was not split: %d part(s)", len(parts))
	}
	if got := strings.Join(parts, ""); got != input {
		t.Fatalf("split lost or changed content\n got: %q\nwant: %q", got, input)
	}
	for i, part := range parts {
		if !utf8.ValidString(part) {
			t.Fatalf("part %d is invalid UTF-8", i)
		}
		if strings.Count(part, "```")%2 != 0 {
			t.Fatalf("part %d split a fenced code block: %q", i, part)
		}
	}
}

func TestSplitFeishuCardMarkdown_DoesNotCutAtomicMarkdown(t *testing.T) {
	input := "[" + strings.Repeat("很长的链接标题", 80) + "](https://example.com/resource)"
	parts := splitFeishuCardMarkdown(input, 100)
	if len(parts) != 1 || parts[0] != input {
		t.Fatalf("atomic Markdown construct was cut: %#v", parts)
	}
}

func TestFeishuCardPartContentAddsOnlyOrderingHeader(t *testing.T) {
	input := "## 原标题\n\n正文"
	got := feishuCardPartContent(input, 2, 3)
	want := "**第 2/3 部分**\n\n" + input
	if got != want {
		t.Fatalf("part content = %q, want %q", got, want)
	}
}

func TestSendReply_LongMarkdownSendsOrderedCards(t *testing.T) {
	oldClient := httpClient
	defer func() { httpClient = oldClient }()

	var cards []string
	var sent int
	httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/open-apis/cardkit/v1/cards":
			var payload struct {
				Data string `json:"data"`
			}
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode card payload: %v", err)
			}
			var card struct {
				Body struct {
					Elements []struct {
						Content string `json:"content"`
					} `json:"elements"`
				} `json:"body"`
			}
			if err := json.Unmarshal([]byte(payload.Data), &card); err != nil {
				t.Fatalf("decode card JSON: %v", err)
			}
			cards = append(cards, card.Body.Elements[0].Content)
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
				`{"code":0,"msg":"ok","data":{"card_id":"card_part"}}`)), Header: make(http.Header)}, nil
		case strings.HasSuffix(req.URL.Path, "/reply"):
			sent++
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
				`{"code":0,"msg":"ok"}`)), Header: make(http.Header)}, nil
		default:
			t.Fatalf("unexpected request: %s", req.URL.Path)
			return nil, nil
		}
	})}

	adapter := &Adapter{tokenCache: "token", tokenExpAt: time.Now().Add(time.Hour), region: RegionFeishu}
	paragraph := "## 小节\n\n" + strings.Repeat("完整内容。", 1400) + "\n\n"
	content := paragraph + paragraph
	err := adapter.SendReply(context.Background(), &im.IncomingMessage{
		MessageID: "long_reply", UserID: "user", ChatType: im.ChatTypeDirect,
	}, &im.ReplyMessage{Content: content, IsFinal: true})
	if err != nil {
		t.Fatalf("SendReply: %v", err)
	}
	if len(cards) < 2 || sent != len(cards) {
		t.Fatalf("cards=%d sent=%d, want multiple matching sends", len(cards), sent)
	}
	for i, card := range cards {
		wantHeader := fmt.Sprintf("**第 %d/%d 部分**", i+1, len(cards))
		if !strings.HasPrefix(card, wantHeader) {
			t.Fatalf("card %d missing order header: %q", i, card[:min(len(card), 80)])
		}
	}
}
