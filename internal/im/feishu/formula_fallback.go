package feishu

import (
	"strings"
	"unicode/utf8"
)

// degradeFeishuMath converts only explicit LaTeX math spans into readable text.
// Feishu CardKit markdown treats \[ and \] as escaped brackets instead of math
// delimiters. Everything outside a complete math span is copied byte-for-byte.
// Markdown code spans and fences are protected, and a malformed or incomplete
// math span is left unchanged.
func degradeFeishuMath(content string) string {
	if content == "" {
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
		if end, ok := feishuMarkdownDestinationEnd(content, i); ok {
			out.WriteString(content[i:end])
			i = end
			continue
		}
		if end, ok := feishuMarkdownAngleTagEnd(content, i); ok {
			out.WriteString(content[i:end])
			i = end
			continue
		}

		bodyStart, bodyEnd, spanEnd, block, ok := feishuMathSpanAt(content, i)
		if !ok {
			out.WriteByte(content[i])
			i++
			continue
		}

		body := content[bodyStart:bodyEnd]
		rendered, supported := renderFeishuMath(body)
		if supported {
			out.WriteString(rendered)
		} else {
			out.WriteString(feishuMathSourceFallback(body, block && feishuMathSpanIsStandalone(content, i, spanEnd)))
		}
		changed = true
		i = spanEnd
	}

	if !changed {
		return content
	}
	return out.String()
}

// feishuMarkdownDestinationEnd protects a link or image destination after ](.
// Formula markup in visible link labels may still be downgraded, while URLs are
// always copied exactly.
func feishuMarkdownDestinationEnd(content string, start int) (int, bool) {
	if start+1 >= len(content) || content[start] != ']' || content[start+1] != '(' || feishuEscapedAt(content, start) {
		return 0, false
	}

	depth := 1
	for i := start + 2; i < len(content); {
		switch content[i] {
		case '\\':
			if i+1 < len(content) {
				i += 2
			} else {
				i++
			}
		case '(':
			depth++
			i++
		case ')':
			depth--
			i++
			if depth == 0 {
				return i, true
			}
		default:
			i++
		}
	}
	return len(content), true
}

func feishuMarkdownAngleTagEnd(content string, start int) (int, bool) {
	if start+1 >= len(content) || content[start] != '<' {
		return 0, false
	}
	next := content[start+1]
	if !feishuASCIILetter(next) && next != '/' && next != '!' && next != '?' {
		return 0, false
	}
	for i := start + 2; i < len(content); i++ {
		if content[i] == '\n' || content[i] == '\r' {
			return 0, false
		}
		if content[i] == '>' {
			return i + 1, true
		}
	}
	return 0, false
}

func feishuMathSpanIsStandalone(content string, start int, end int) bool {
	lineStart := strings.LastIndexByte(content[:start], '\n') + 1
	lineEnd := len(content)
	if rel := strings.IndexByte(content[end:], '\n'); rel >= 0 {
		lineEnd = end + rel
	}
	return strings.TrimSpace(content[lineStart:start]) == "" && strings.TrimSpace(content[end:lineEnd]) == ""
}

// feishuMarkdownCodeEnd returns the end of a complete Markdown code span or
// fence that starts at start. An unclosed code span protects the rest of the
// content so formula-like examples are never rewritten.
func feishuMarkdownCodeEnd(content string, start int) (int, bool) {
	if start >= len(content) || (content[start] != '`' && content[start] != '~') {
		return 0, false
	}

	marker := content[start]
	run := 1
	for start+run < len(content) && content[start+run] == marker {
		run++
	}
	if marker == '~' {
		if run < 3 || !feishuMarkdownFenceStart(content, start) {
			return 0, false
		}
	}

	needle := strings.Repeat(string(marker), run)
	if rel := strings.Index(content[start+run:], needle); rel >= 0 {
		return start + run + rel + run, true
	}
	return len(content), true
}

func feishuMarkdownFenceStart(content string, start int) bool {
	lineStart := strings.LastIndexByte(content[:start], '\n') + 1
	prefix := content[lineStart:start]
	return len(prefix) <= 3 && strings.Trim(prefix, " ") == ""
}

func feishuMathSpanAt(content string, start int) (
	bodyStart int,
	bodyEnd int,
	spanEnd int,
	block bool,
	ok bool,
) {
	if start+2 <= len(content) && content[start] == '\\' && !feishuEscapedAt(content, start) {
		var close byte
		switch content[start+1] {
		case '[':
			close = ']'
			block = true
		case '(':
			close = ')'
		default:
			return 0, 0, 0, false, false
		}

		for i := start + 2; i+1 < len(content); i++ {
			if content[i] == '\\' && content[i+1] == close && !feishuEscapedAt(content, i) {
				return start + 2, i, i + 2, block, true
			}
		}
		return 0, 0, 0, false, false
	}

	if !feishuDoubleDollarAt(content, start) {
		return 0, 0, 0, false, false
	}
	for i := start + 2; i+1 < len(content); i++ {
		if feishuDoubleDollarAt(content, i) {
			return start + 2, i, i + 2, true, true
		}
	}
	return 0, 0, 0, false, false
}

func feishuDoubleDollarAt(content string, pos int) bool {
	if pos < 0 || pos+1 >= len(content) || content[pos:pos+2] != "$$" || feishuEscapedAt(content, pos) {
		return false
	}
	return (pos == 0 || content[pos-1] != '$') && (pos+2 == len(content) || content[pos+2] != '$')
}

func feishuEscapedAt(content string, pos int) bool {
	backslashes := 0
	for i := pos - 1; i >= 0 && content[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

type feishuMathParser struct {
	input     string
	pos       int
	supported bool
}

type feishuMathToken struct {
	text        string
	textCommand bool
}

func renderFeishuMath(body string) (string, bool) {
	p := &feishuMathParser{input: body, supported: true}
	rendered, closed := p.render(0)
	if !closed || !p.supported {
		return "", false
	}
	return strings.Join(strings.Fields(rendered), " "), true
}

// render parses until stop. A zero stop parses to EOF. This is intentionally a
// small, conservative subset rather than a general TeX parser.
func (p *feishuMathParser) render(stop byte) (string, bool) {
	var out strings.Builder

	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		if stop != 0 && ch == stop {
			p.pos++
			return out.String(), true
		}

		switch ch {
		case '\\':
			token := p.command()
			if token.textCommand && feishuMathNeedsSpace(out.String(), token.text) {
				out.WriteByte(' ')
			}
			out.WriteString(token.text)
		case '{':
			group, ok := p.group()
			if !ok {
				return "", false
			}
			out.WriteByte('(')
			out.WriteString(group)
			out.WriteByte(')')
		case '}':
			return "", false
		case '_', '^':
			p.pos++
			arg, ok := p.scriptArgument()
			if !ok {
				return "", false
			}
			out.WriteByte(ch)
			if utf8.RuneCountInString(arg) == 1 && !strings.ContainsAny(arg, " \t\r\n") {
				out.WriteString(arg)
			} else {
				out.WriteByte('(')
				out.WriteString(arg)
				out.WriteByte(')')
			}
		case '~':
			out.WriteByte(' ')
			p.pos++
		case '&':
			p.supported = false
			out.WriteByte(ch)
			p.pos++
		default:
			_, size := utf8.DecodeRuneInString(p.input[p.pos:])
			out.WriteString(p.input[p.pos : p.pos+size])
			p.pos += size
		}
	}

	return out.String(), stop == 0
}

func (p *feishuMathParser) group() (string, bool) {
	if p.pos >= len(p.input) || p.input[p.pos] != '{' {
		return "", false
	}
	p.pos++
	return p.render('}')
}

func (p *feishuMathParser) rawTextGroup() (string, bool) {
	if p.pos >= len(p.input) || p.input[p.pos] != '{' {
		return "", false
	}
	p.pos++
	start := p.pos
	depth := 1
	for p.pos < len(p.input) {
		switch p.input[p.pos] {
		case '\\':
			p.pos++
			if p.pos < len(p.input) {
				p.pos++
			}
		case '{':
			depth++
			p.pos++
		case '}':
			depth--
			if depth == 0 {
				raw := p.input[start:p.pos]
				p.pos++
				return unescapeFeishuMathText(raw), true
			}
			p.pos++
		default:
			_, size := utf8.DecodeRuneInString(p.input[p.pos:])
			p.pos += size
		}
	}
	return "", false
}

func unescapeFeishuMathText(text string) string {
	return strings.NewReplacer(
		`\%`, `%`,
		`\_`, `_`,
		`\#`, `#`,
		`\$`, `$`,
		`\&`, `&`,
		`\{`, `{`,
		`\}`, `}`,
		`\\`, `\`,
	).Replace(text)
}

func (p *feishuMathParser) scriptArgument() (string, bool) {
	if p.pos >= len(p.input) {
		return "", false
	}
	if p.input[p.pos] == '{' {
		return p.group()
	}
	if p.input[p.pos] == '\\' {
		token := p.command()
		return token.text, p.supported
	}
	_, size := utf8.DecodeRuneInString(p.input[p.pos:])
	value := p.input[p.pos : p.pos+size]
	p.pos += size
	return value, true
}

func (p *feishuMathParser) command() feishuMathToken {
	start := p.pos
	p.pos++
	if p.pos >= len(p.input) {
		p.supported = false
		return feishuMathToken{text: `\`}
	}

	if !feishuASCIILetter(p.input[p.pos]) {
		ch := p.input[p.pos]
		p.pos++
		switch ch {
		case '\\':
			return feishuMathToken{text: " "}
		case ',', ';', ':', ' ':
			return feishuMathToken{text: " "}
		case '!':
			return feishuMathToken{}
		case '%', '_', '#', '$', '&', '{', '}':
			return feishuMathToken{text: string(ch)}
		default:
			p.supported = false
			return feishuMathToken{text: p.input[start:p.pos]}
		}
	}

	nameStart := p.pos
	for p.pos < len(p.input) && feishuASCIILetter(p.input[p.pos]) {
		p.pos++
	}
	name := p.input[nameStart:p.pos]

	if symbol, ok := feishuMathSymbols[name]; ok {
		return feishuMathToken{text: symbol}
	}

	switch name {
	case "text", "textrm", "textnormal", "operatorname":
		value, ok := p.rawTextGroup()
		if !ok {
			p.supported = false
			return feishuMathToken{text: p.input[start:p.pos]}
		}
		return feishuMathToken{text: value, textCommand: true}
	case "mathrm", "mathbf", "mathit", "mathsf", "mathtt", "mathcal", "boldsymbol":
		value, ok := p.group()
		if !ok {
			p.supported = false
			return feishuMathToken{text: p.input[start:p.pos]}
		}
		return feishuMathToken{text: value}
	case "frac", "dfrac", "tfrac":
		numerator, okNumerator := p.group()
		denominator, okDenominator := p.group()
		if !okNumerator || !okDenominator {
			p.supported = false
			return feishuMathToken{text: p.input[start:p.pos]}
		}
		return feishuMathToken{text: "(" + numerator + ") / (" + denominator + ")"}
	case "sqrt":
		index := ""
		if p.pos < len(p.input) && p.input[p.pos] == '[' {
			end := strings.IndexByte(p.input[p.pos+1:], ']')
			if end < 0 {
				p.supported = false
				return feishuMathToken{text: p.input[start:p.pos]}
			}
			index = p.input[p.pos+1 : p.pos+1+end]
			p.pos += end + 2
		}
		value, ok := p.group()
		if !ok {
			p.supported = false
			return feishuMathToken{text: p.input[start:p.pos]}
		}
		if index != "" {
			return feishuMathToken{text: index + "√(" + value + ")"}
		}
		return feishuMathToken{text: "√(" + value + ")"}
	case "left", "right":
		return p.delimiterAfterSizingCommand(start)
	case "quad", "qquad", "enspace", "thinspace", "medspace", "thickspace":
		return feishuMathToken{text: " "}
	case "displaystyle", "textstyle", "scriptstyle", "scriptscriptstyle":
		return feishuMathToken{}
	default:
		p.supported = false
		return feishuMathToken{text: p.input[start:p.pos]}
	}
}

func (p *feishuMathParser) delimiterAfterSizingCommand(start int) feishuMathToken {
	if p.pos >= len(p.input) {
		p.supported = false
		return feishuMathToken{text: p.input[start:p.pos]}
	}
	if p.input[p.pos] == '.' {
		p.pos++
		return feishuMathToken{}
	}
	if p.input[p.pos] == '\\' {
		p.pos++
		if p.pos >= len(p.input) {
			p.supported = false
			return feishuMathToken{text: p.input[start:p.pos]}
		}
	}
	_, size := utf8.DecodeRuneInString(p.input[p.pos:])
	value := p.input[p.pos : p.pos+size]
	p.pos += size
	return feishuMathToken{text: value}
}

func feishuASCIILetter(ch byte) bool {
	return ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z'
}

func feishuMathNeedsSpace(existing string, text string) bool {
	if existing == "" || text == "" {
		return false
	}
	last := existing[len(existing)-1]
	first := text[0]
	return (last >= '0' && last <= '9' || feishuASCIILetter(last) || last == ')' || last == ']') &&
		(first >= '0' && first <= '9' || feishuASCIILetter(first))
}

var feishuMathSymbols = map[string]string{
	"alpha":      "α",
	"beta":       "β",
	"gamma":      "γ",
	"delta":      "δ",
	"epsilon":    "ε",
	"theta":      "θ",
	"lambda":     "λ",
	"mu":         "μ",
	"pi":         "π",
	"rho":        "ρ",
	"sigma":      "σ",
	"tau":        "τ",
	"phi":        "φ",
	"omega":      "ω",
	"Gamma":      "Γ",
	"Delta":      "Δ",
	"Theta":      "Θ",
	"Lambda":     "Λ",
	"Pi":         "Π",
	"Sigma":      "Σ",
	"Phi":        "Φ",
	"Omega":      "Ω",
	"times":      " × ",
	"cdot":       " · ",
	"approx":     " ≈ ",
	"sim":        " ～ ",
	"pm":         " ± ",
	"mp":         " ∓ ",
	"le":         " ≤ ",
	"leq":        " ≤ ",
	"ge":         " ≥ ",
	"geq":        " ≥ ",
	"ne":         " ≠ ",
	"neq":        " ≠ ",
	"infty":      "∞",
	"circ":       "°",
	"degree":     "°",
	"rightarrow": " → ",
	"to":         " → ",
	"leftarrow":  " ← ",
}

func feishuMathSourceFallback(body string, block bool) string {
	if block || strings.ContainsAny(body, "\r\n") {
		fence := feishuMathBacktickFence(body)
		return fence + "text\n" + strings.TrimSpace(body) + "\n" + fence
	}

	fence := feishuMathBacktickFence(body)
	value := body
	if strings.HasPrefix(value, "`") || strings.HasSuffix(value, "`") {
		value = " " + value + " "
	}
	return fence + value + fence
}

func feishuMathBacktickFence(content string) string {
	longest := 0
	for i := 0; i < len(content); {
		if content[i] != '`' {
			i++
			continue
		}
		run := 1
		for i+run < len(content) && content[i+run] == '`' {
			run++
		}
		if run > longest {
			longest = run
		}
		i += run
	}
	if longest < 2 {
		return "```"
	}
	return strings.Repeat("`", longest+1)
}
