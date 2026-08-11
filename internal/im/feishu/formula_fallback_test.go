package feishu

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/im"
)

func TestDegradeFeishuMath_DoseBlock(t *testing.T) {
	input := "参考方案：\n\n\\[\n8\\text{mg/kg，静脉输注，每4周1次}\n\\]\n\n后续说明。"
	want := "参考方案：\n\n8 mg/kg，静脉输注，每4周1次\n\n后续说明。"

	if got := degradeFeishuMath(input); got != want {
		t.Fatalf("degraded dose = %q, want %q", got, want)
	}
}

func TestDegradeFeishuMath_ReportedMedicalExamples(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "body surface area dose",
			input: `\[375\text{mg/m}^2，\text{每周1次，共4次}\]`,
			want:  "375 mg/m^2，每周1次，共4次",
		},
		{
			name:  "fixed dose",
			input: `\[1000\text{mg，共2次，间隔2周}\]`,
			want:  "1000 mg，共2次，间隔2周",
		},
		{
			name:  "weight based dose",
			input: `\[0.4\sim2.0\text{g/kg，每4周1次}\]`,
			want:  "0.4 ～ 2.0 g/kg，每4周1次",
		},
		{
			name:  "drug name before dose",
			input: `\[\text{甲泼尼龙 }1000\text{mg/d，静脉滴注，连续3～5 d}\]`,
			want:  "甲泼尼龙 1000 mg/d，静脉滴注，连续3～5 d",
		},
		{
			name:  "plain daily dose",
			input: `\[60\text{mg/d}\]`,
			want:  "60 mg/d",
		},
		{
			name:  "weight based daily dose",
			input: `\[1\text{mg/kg/d}\]`,
			want:  "1 mg/kg/d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := degradeFeishuMath(tt.input); got != tt.want {
				t.Fatalf("degraded formula = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDegradeFeishuMath_FractionAndScripts(t *testing.T) {
	input := "\\[\n" +
		"P_{\\text{目标}}\n" +
		"=P_1+\n" +
		"\\frac{R_{\\text{目标}}-R_1}{R_2-R_1}\n" +
		"\\times(P_2-P_1)\n" +
		"\\]"
	want := "P_(目标) =P_1+ (R_(目标)-R_1) / (R_2-R_1) × (P_2-P_1)"

	if got := degradeFeishuMath(input); got != want {
		t.Fatalf("degraded fraction = %q, want %q", got, want)
	}
}

func TestDegradeFeishuMath_InlineAndDoubleDollar(t *testing.T) {
	input := "行内 \\(x^2 \\approx 4\\) 保留段落；块公式 $$60\\text{mg/d}$$ 结束。"
	want := "行内 x^2 ≈ 4 保留段落；块公式 60 mg/d 结束。"

	if got := degradeFeishuMath(input); got != want {
		t.Fatalf("degraded mixed math = %q, want %q", got, want)
	}
}

func TestDegradeFeishuMath_PreservesAllNonMathContent(t *testing.T) {
	input := "# 标题\n\n" +
		"- **重点**：价格从 $50 到 $100，环境变量是 $PATH。\n" +
		"> 引用内容\n\n" +
		"[链接](https://example.com/a_(b)/\\(raw\\))\n" +
		"![眼底图](img_test)\n" +
		"<kb doc=\"\\(literal\\).pdf\" chunk_id=\"chunk-1\" kb_id=\"kb-1\" />"

	if got := degradeFeishuMath(input); got != input {
		t.Fatalf("non-math content changed:\n got: %q\nwant: %q", got, input)
	}
}

func TestDegradeFeishuMath_ProtectsMarkdownCode(t *testing.T) {
	input := "示例 `\\(x^2\\)` 不转换。\n\n" +
		"```latex\n\\[\n\\frac{a}{b}\n\\]\n```\n\n" +
		"实际公式：\\(a \\times b\\)。"
	want := "示例 `\\(x^2\\)` 不转换。\n\n" +
		"```latex\n\\[\n\\frac{a}{b}\n\\]\n```\n\n" +
		"实际公式：a × b。"

	if got := degradeFeishuMath(input); got != want {
		t.Fatalf("code protection failed:\n got: %q\nwant: %q", got, want)
	}
}

func TestDegradeFeishuMath_LeavesIncompleteAndEscapedDelimitersUntouched(t *testing.T) {
	tests := []string{
		"尚未完成：\\[\\frac{a}{b}",
		`字面量：\\[not math\\]`,
		"未闭合代码 `\\(x\\) 后面的 \\(y\\) 也属于代码",
	}

	for _, input := range tests {
		if got := degradeFeishuMath(input); got != input {
			t.Errorf("protected input changed: got %q, want %q", got, input)
		}
	}
}

func TestDegradeFeishuMath_UnknownFormulaUsesLosslessCodeFallback(t *testing.T) {
	input := "前文\n\\[\n\\begin{cases}\nx & x > 0\\\\\n-x & x \\le 0\n\\end{cases}\n\\]\n后文"
	got := degradeFeishuMath(input)

	if !strings.HasPrefix(got, "前文\n```text\n") || !strings.HasSuffix(got, "\n```\n后文") {
		t.Fatalf("unknown block did not use a fenced fallback: %q", got)
	}
	if !strings.Contains(got, `\begin{cases}`) || !strings.Contains(got, `\end{cases}`) {
		t.Fatalf("unknown formula source was not preserved: %q", got)
	}
	if strings.Contains(got, `\[`) || strings.Contains(got, `\]`) {
		t.Fatalf("CardKit-breaking delimiters remain in fallback: %q", got)
	}
}

func TestDegradeFeishuMath_IsIdempotent(t *testing.T) {
	inputs := []string{
		"剂量：\\[8\\text{mg/kg}\\]。",
		"复杂：\\[\\begin{matrix}a&b\\end{matrix}\\]。",
		"没有公式。",
	}

	for _, input := range inputs {
		once := degradeFeishuMath(input)
		if twice := degradeFeishuMath(once); twice != once {
			t.Errorf("conversion is not idempotent: once=%q twice=%q", once, twice)
		}
	}
}

func TestSendReply_AppliesFormulaFallbackAtFeishuBoundary(t *testing.T) {
	oldClient := httpClient
	defer func() { httpClient = oldClient }()

	var sentPayload struct {
		MessageType string `json:"msg_type"`
		Content     string `json:"content"`
	}
	httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/open-apis/im/v1/messages/message_formula/reply" {
			t.Fatalf("unexpected request path: %s", req.URL.Path)
		}
		if err := json.NewDecoder(req.Body).Decode(&sentPayload); err != nil {
			t.Fatalf("decode reply payload: %v", err)
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
	incoming := &im.IncomingMessage{MessageID: "message_formula", UserID: "user_test", ChatType: im.ChatTypeDirect}
	original := "剂量：\\[8\\text{mg/kg}\\]，其他内容不变。"
	if err := adapter.SendReply(context.Background(), incoming, &im.ReplyMessage{Content: original, IsFinal: true}); err != nil {
		t.Fatalf("SendReply: %v", err)
	}

	if sentPayload.MessageType != "text" {
		t.Fatalf("message type = %q, want text", sentPayload.MessageType)
	}
	var textContent struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(sentPayload.Content), &textContent); err != nil {
		t.Fatalf("decode text content: %v", err)
	}
	if want := "剂量：8 mg/kg，其他内容不变。"; textContent.Text != want {
		t.Fatalf("sent text = %q, want %q", textContent.Text, want)
	}
}

func TestUpdateStreamContent_AppliesFormulaFallback(t *testing.T) {
	oldClient := httpClient
	defer func() { httpClient = oldClient }()

	const streamID = "card_formula"
	feishuStreamsMu.Lock()
	feishuStreams[streamID] = &feishuStreamState{createdAt: time.Now()}
	feishuStreamsMu.Unlock()
	t.Cleanup(func() {
		feishuStreamsMu.Lock()
		delete(feishuStreams, streamID)
		feishuStreamsMu.Unlock()
	})

	var updatePayload struct {
		Content  string `json:"content"`
		Sequence int    `json:"sequence"`
	}
	httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		wantPath := "/open-apis/cardkit/v1/cards/card_formula/elements/streaming_content/content"
		if req.URL.Path != wantPath {
			t.Fatalf("request path = %s, want %s", req.URL.Path, wantPath)
		}
		if err := json.NewDecoder(req.Body).Decode(&updatePayload); err != nil {
			t.Fatalf("decode update payload: %v", err)
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
	content := "## 方案\n\n**成人剂量：**\\[8\\text{mg/kg，静脉输注}\\]\n\n正文保持不变。"
	if err := adapter.UpdateStreamContent(context.Background(), &im.IncomingMessage{}, streamID, content); err != nil {
		t.Fatalf("UpdateStreamContent: %v", err)
	}

	want := "## 方案\n\n**成人剂量：** 8 mg/kg，静脉输注\n\n正文保持不变。"
	if updatePayload.Content != want {
		t.Fatalf("stream content = %q, want %q", updatePayload.Content, want)
	}
	if updatePayload.Sequence != 1 {
		t.Fatalf("sequence = %d, want 1", updatePayload.Sequence)
	}
}
