package feishu

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// normalizeFeishuMarkdownCompatibility applies only compatibility rewrites
// needed by Feishu/Lark CardKit. The original model answer remains unchanged
// in storage and other output channels.
func normalizeFeishuMarkdownCompatibility(content string) string {
	content = degradeFeishuMath(content)
	return normalizeFeishuBoldLabelSpacing(content)
}

// normalizeFeishuBoldLabelSpacing makes line-leading strong spans that end in
// punctuation, such as **Phase:**text or **Summary.**text, unambiguous to
// CardKit by inserting one space after the closing marker. Other emphasis,
// prose, links, and Markdown code are untouched.
func normalizeFeishuBoldLabelSpacing(content string) string {
	if content == "" || !strings.Contains(content, "**") {
		return content
	}

	var out strings.Builder
	out.Grow(len(content))
	changed := false

	for i := 0; i < len(content); {
		if end, ok := feishuMarkdownCodeEnd(content, i); ok {
			out.WriteString(content[i:end])
			i = end
			continue
		}

		if (i == 0 || content[i-1] == '\n') && i < len(content) {
			if afterMarker, ok := feishuBoldLabelWithoutSpacing(content, i); ok {
				out.WriteString(content[i:afterMarker])
				out.WriteByte(' ')
				i = afterMarker
				changed = true
				continue
			}
		}

		out.WriteByte(content[i])
		i++
	}

	if !changed {
		return content
	}
	return out.String()
}

func feishuBoldLabelWithoutSpacing(content string, lineStart int) (int, bool) {
	lineEnd := len(content)
	if rel := strings.IndexByte(content[lineStart:], '\n'); rel >= 0 {
		lineEnd = lineStart + rel
	}

	pos := lineStart
	indent := 0
	for pos < lineEnd && content[pos] == ' ' && indent < 3 {
		pos++
		indent++
	}
	if pos < lineEnd && content[pos] == ' ' {
		return 0, false
	}

	pos = feishuSkipOptionalListMarker(content, pos, lineEnd)
	if pos+2 > lineEnd || content[pos:pos+2] != "**" {
		return 0, false
	}

	labelStart := pos + 2
	relClose := strings.Index(content[labelStart:lineEnd], "**")
	if relClose < 0 {
		return 0, false
	}
	closeStart := labelStart + relClose
	label := content[labelStart:closeStart]
	if label == "" || strings.ContainsAny(label, "*\r\n") {
		return 0, false
	}
	lastLabelRune, _ := utf8.DecodeLastRuneInString(strings.TrimSpace(label))
	if !unicode.IsPunct(lastLabelRune) {
		return 0, false
	}

	afterMarker := closeStart + 2
	if afterMarker >= lineEnd || content[afterMarker] == '*' {
		return 0, false
	}
	next, _ := utf8.DecodeRuneInString(content[afterMarker:lineEnd])
	if unicode.IsSpace(next) {
		return 0, false
	}
	return afterMarker, true
}

func feishuSkipOptionalListMarker(content string, pos int, lineEnd int) int {
	if pos >= lineEnd {
		return pos
	}

	if (content[pos] == '-' || content[pos] == '+' || content[pos] == '*') &&
		pos+1 < lineEnd && (content[pos+1] == ' ' || content[pos+1] == '\t') {
		pos += 2
		for pos < lineEnd && (content[pos] == ' ' || content[pos] == '\t') {
			pos++
		}
		return pos
	}

	digitStart := pos
	for pos < lineEnd && pos-digitStart < 9 && content[pos] >= '0' && content[pos] <= '9' {
		pos++
	}
	if pos == digitStart || pos >= lineEnd || (content[pos] != '.' && content[pos] != ')') {
		return digitStart
	}
	pos++
	if pos >= lineEnd || (content[pos] != ' ' && content[pos] != '\t') {
		return digitStart
	}
	for pos < lineEnd && (content[pos] == ' ' || content[pos] == '\t') {
		pos++
	}
	return pos
}
