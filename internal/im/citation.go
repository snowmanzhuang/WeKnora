package im

import (
	"fmt"
	"html"
	"path"
	"regexp"
	"strings"
)

// imCitationTagRe matches inline citation tags produced by the agent pipeline.
// The web frontend renders these tags interactively. IM output handles them in
// two different ways: intermediate updates hide them, while the final update
// converts knowledge citations into compact numbered book references.
var imCitationTagRe = regexp.MustCompile(`(?is)<(?:kb|web)\b[^>]*/?>`)

var imCitationAttributeRe = regexp.MustCompile(`([\w-]+)\s*=\s*(?:"([^"]*)"|'([^']*)')`)

// stripIMCitationTags removes <kb .../> and <web .../> inline citation tags.
// It remains the right behavior for partial streaming updates, where a stable
// final source order is not known yet.
func stripIMCitationTags(s string) string {
	return imCitationTagRe.ReplaceAllString(s, "")
}

// formatIMCitationTags converts final-answer knowledge citations to numbered
// book markers and appends a compact source list. Numbering is book-based, not
// chunk-based, so multiple cited chunks from one book share the same marker.
// Web citations keep their existing IM behavior and are removed.
func formatIMCitationTags(s string) string {
	locations := imCitationTagRe.FindAllStringIndex(s, -1)
	if len(locations) == 0 {
		return s
	}

	sources := make([]string, 0, len(locations))
	sourceNumbers := make(map[string]int, len(locations))
	var output strings.Builder
	output.Grow(len(s))

	lastEnd := 0
	lastWasCitation := false
	lastCitationNumber := 0

	for _, location := range locations {
		between := s[lastEnd:location[0]]
		whitespaceBridge := lastWasCitation && strings.TrimSpace(between) == ""
		if !whitespaceBridge {
			output.WriteString(between)
			lastWasCitation = false
		}

		tag := s[location[0]:location[1]]
		if isIMKnowledgeCitationTag(tag) {
			attributes := parseIMCitationAttributes(tag)
			rawTitle := attributes["doc"]
			if rawTitle == "" {
				rawTitle = attributes["title"]
			}
			title := cleanIMCitationBookTitle(rawTitle)
			if title != "" {
				key := strings.ToLower(title)
				number, exists := sourceNumbers[key]
				if !exists {
					number = len(sources) + 1
					sourceNumbers[key] = number
					sources = append(sources, title)
				}

				// Consecutive references form one compact marker cluster. Repeated
				// chunks from the same book collapse to one marker.
				if !lastWasCitation {
					output.WriteString(fmt.Sprintf("[%d]", number))
				} else if number != lastCitationNumber {
					output.WriteString(fmt.Sprintf(" [%d]", number))
				}
				lastWasCitation = true
				lastCitationNumber = number
			}
		}
		lastEnd = location[1]
	}

	output.WriteString(s[lastEnd:])
	result := output.String()
	if len(sources) == 0 {
		return result
	}

	result = strings.TrimRight(result, " \t\r\n")
	var footer strings.Builder
	footer.Grow(len(sources) * 48)
	if result != "" {
		footer.WriteString(result)
		footer.WriteString("\n\n")
	}
	footer.WriteString("**参考资料**")
	for number, title := range sources {
		footer.WriteString(fmt.Sprintf("\n[%d] 《%s》", number+1, escapeIMMarkdownInline(title)))
	}
	return footer.String()
}

func isIMKnowledgeCitationTag(tag string) bool {
	trimmed := strings.TrimSpace(tag)
	return len(trimmed) >= 3 && strings.EqualFold(trimmed[:3], "<kb")
}

func parseIMCitationAttributes(tag string) map[string]string {
	attributes := make(map[string]string)
	for _, match := range imCitationAttributeRe.FindAllStringSubmatch(tag, -1) {
		value := match[2]
		if value == "" {
			value = match[3]
		}
		attributes[strings.ToLower(match[1])] = html.UnescapeString(value)
	}
	return attributes
}

func cleanIMCitationBookTitle(raw string) string {
	title := strings.TrimSpace(html.UnescapeString(raw))
	if title == "" {
		return ""
	}

	// Citation tags normally contain a file name, but tolerate either slash so
	// an accidental path never leaks into the IM source list.
	title = path.Base(strings.ReplaceAll(title, `\`, "/"))
	lower := strings.ToLower(title)
	for _, extension := range []string{
		".mhtml", ".mht", ".markdown", ".md", ".pdf", ".epub", ".docx", ".doc", ".pptx", ".ppt", ".txt",
	} {
		if strings.HasSuffix(lower, extension) {
			title = title[:len(title)-len(extension)]
			break
		}
	}

	lower = strings.ToLower(title)
	for _, suffix := range []string{"_weknora", "-weknora"} {
		if strings.HasSuffix(lower, suffix) {
			title = title[:len(title)-len(suffix)]
			break
		}
	}

	title = strings.ReplaceAll(title, "_", " ")
	return strings.Join(strings.Fields(title), " ")
}

func escapeIMMarkdownInline(s string) string {
	return strings.NewReplacer(
		`\`, `\\`,
		`[`, `\[`,
		`]`, `\]`,
		`*`, `\*`,
		"`", "\\`",
		`<`, `＜`,
		`>`, `＞`,
	).Replace(s)
}
