package im

import (
	"context"
	"io"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	maxIMOutboundImages     = 5
	maxIMOutboundImageBytes = 10 << 20
)

var imRemovedImageBlankLinesRe = regexp.MustCompile(`\n{3,}`)

type imMarkdownImageSpan struct {
	Start int
	End   int
	Alt   string
	Path  string
}

func (s *Service) prepareIMDisplayContent(
	ctx context.Context,
	display string,
	tenant *types.Tenant,
	includeImages bool,
) (string, []*OutboundImage) {
	content := stripImageXMLTags(display)

	var images []*OutboundImage
	if includeImages {
		content, images = s.extractIMOutboundImages(ctx, content, tenant)
	}

	content = stripIMCitationTags(content)
	resolver := newIMFileServiceResolver(tenant, s.defaultFileSvc)
	content = rewriteStorageURLs(ctx, content, resolver)
	return content, images
}

func (s *Service) extractIMOutboundImages(
	ctx context.Context,
	content string,
	tenant *types.Tenant,
) (string, []*OutboundImage) {
	spans := scanIMMarkdownImages(content)
	if len(spans) == 0 {
		return content, nil
	}

	resolver := newIMFileServiceResolver(tenant, s.defaultFileSvc)
	images := make([]*OutboundImage, 0, min(len(spans), maxIMOutboundImages))
	remove := make([]imMarkdownImageSpan, 0, len(spans))
	uploaded := make(map[string]bool)

	for _, span := range spans {
		if types.ParseProviderScheme(span.Path) == "" {
			continue
		}
		if uploaded[span.Path] {
			remove = append(remove, span)
			continue
		}
		if len(images) >= maxIMOutboundImages {
			logger.Warnf(ctx, "[IM] Too many outbound images; keeping image reference in text: %s", span.Path)
			continue
		}

		fileSvc := resolver.resolve(span.Path)
		if fileSvc == nil {
			logger.Warnf(ctx, "[IM] No file service for outbound image: %s", span.Path)
			continue
		}

		reader, err := fileSvc.GetFile(ctx, span.Path)
		if err != nil {
			logger.Warnf(ctx, "[IM] Failed to read outbound image: path=%s err=%v", span.Path, err)
			continue
		}
		if reader == nil {
			logger.Warnf(ctx, "[IM] File service returned empty reader for outbound image: %s", span.Path)
			continue
		}

		data, readErr := io.ReadAll(io.LimitReader(reader, maxIMOutboundImageBytes+1))
		closeErr := reader.Close()
		if readErr != nil {
			logger.Warnf(ctx, "[IM] Failed to load outbound image: path=%s err=%v", span.Path, readErr)
			continue
		}
		if closeErr != nil {
			logger.Warnf(ctx, "[IM] Failed to close outbound image reader: path=%s err=%v", span.Path, closeErr)
		}
		if len(data) == 0 {
			logger.Warnf(ctx, "[IM] Empty outbound image skipped: %s", span.Path)
			continue
		}
		if len(data) > maxIMOutboundImageBytes {
			logger.Warnf(ctx, "[IM] Outbound image exceeds %d bytes: %s", maxIMOutboundImageBytes, span.Path)
			continue
		}

		images = append(images, &OutboundImage{
			FileName: imOutboundImageFileName(span.Path),
			Caption:  strings.TrimSpace(span.Alt),
			Data:     data,
		})
		uploaded[span.Path] = true
		remove = append(remove, span)
	}

	return removeIMMarkdownImageSpans(content, remove), images
}

func sendIMOutboundImages(ctx context.Context, adapter Adapter, msg *IncomingMessage, images []*OutboundImage) {
	if len(images) == 0 {
		return
	}
	sender, ok := adapter.(ImageSender)
	if !ok {
		return
	}
	for _, image := range images {
		if image == nil || len(image.Data) == 0 {
			continue
		}
		if err := sender.SendImage(ctx, msg, image); err != nil {
			logger.Warnf(ctx, "[IM] Failed to send outbound image: file=%s err=%v", image.FileName, err)
		}
	}
}

func adapterSupportsImages(adapter Adapter) bool {
	_, ok := adapter.(ImageSender)
	return ok
}

func removeIMMarkdownImageSpans(content string, spans []imMarkdownImageSpan) string {
	if len(spans) == 0 {
		return content
	}

	var b strings.Builder
	last := 0
	for _, span := range spans {
		if span.Start < last || span.End > len(content) || span.Start >= span.End {
			continue
		}
		b.WriteString(content[last:span.Start])
		last = span.End
	}
	b.WriteString(content[last:])
	return strings.TrimSpace(imRemovedImageBlankLinesRe.ReplaceAllString(b.String(), "\n\n"))
}

func scanIMMarkdownImages(markdown string) []imMarkdownImageSpan {
	var spans []imMarkdownImageSpan
	for i := 0; i+1 < len(markdown); i++ {
		if markdown[i] != '!' || markdown[i+1] != '[' || imMarkdownEscaped(markdown, i) {
			continue
		}

		altEnd := findIMMarkdownImageAltEnd(markdown, i+2)
		if altEnd == -1 {
			continue
		}

		targetStart := altEnd + 2
		targetEnd, ok := findIMMarkdownImageTargetEnd(markdown, targetStart)
		if !ok {
			i = altEnd
			continue
		}

		rawTarget := markdown[targetStart:targetEnd]
		path := parseIMMarkdownImageTarget(rawTarget)
		if path != "" {
			spans = append(spans, imMarkdownImageSpan{
				Start: i,
				End:   targetEnd + 1,
				Alt:   markdown[i+2 : altEnd],
				Path:  path,
			})
		}
		i = targetEnd
	}
	return spans
}

func findIMMarkdownImageAltEnd(markdown string, start int) int {
	for i := start; i+1 < len(markdown); i++ {
		if markdown[i] == ']' && markdown[i+1] == '(' && !imMarkdownEscaped(markdown, i) {
			return i
		}
	}
	return -1
}

func findIMMarkdownImageTargetEnd(markdown string, start int) (int, bool) {
	parenDepth := 1
	inAngleDestination := false
	seenNonSpace := false
	var inQuote byte

	for i := start; i < len(markdown); i++ {
		ch := markdown[i]
		if ch == '\\' {
			i++
			continue
		}

		if !seenNonSpace && !imMarkdownSpace(ch) {
			seenNonSpace = true
			if ch == '<' {
				inAngleDestination = true
				continue
			}
		}

		if inAngleDestination {
			if ch == '>' {
				inAngleDestination = false
			}
			continue
		}

		if inQuote != 0 {
			if ch == inQuote {
				inQuote = 0
			}
			continue
		}

		if (ch == '"' || ch == '\'') && i > start && imMarkdownSpace(markdown[i-1]) {
			inQuote = ch
			continue
		}

		switch ch {
		case '(':
			parenDepth++
		case ')':
			parenDepth--
			if parenDepth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

func parseIMMarkdownImageTarget(raw string) string {
	start, end := trimIMMarkdownSpaceBounds(raw, 0, len(raw))
	if start >= end {
		return ""
	}

	trimmed := raw[start:end]
	if trimmed[0] == '<' {
		if closeIdx := strings.IndexByte(trimmed, '>'); closeIdx > 0 {
			return strings.TrimSpace(trimmed[1:closeIdx])
		}
	}

	if titleStart, ok := parseIMMarkdownImageTitleSuffix(trimmed); ok {
		candidate := strings.TrimSpace(trimmed[:titleStart])
		if candidate != "" {
			return candidate
		}
	}
	return trimmed
}

func parseIMMarkdownImageTitleSuffix(raw string) (titleStart int, ok bool) {
	_, end := trimIMMarkdownSpaceBounds(raw, 0, len(raw))
	if end == 0 {
		return 0, false
	}

	switch raw[end-1] {
	case '"', '\'':
		quote := raw[end-1]
		for i := end - 2; i >= 0; i-- {
			if raw[i] != quote || imMarkdownEscaped(raw, i) {
				continue
			}
			if i == 0 || !imMarkdownSpace(raw[i-1]) {
				return 0, false
			}
			return i, true
		}
	case ')':
		depth := 0
		for i := end - 2; i >= 0; i-- {
			if imMarkdownEscaped(raw, i) {
				continue
			}
			switch raw[i] {
			case ')':
				depth++
			case '(':
				if depth == 0 {
					if i == 0 || !imMarkdownSpace(raw[i-1]) {
						return 0, false
					}
					return i, true
				}
				depth--
			}
		}
	}
	return 0, false
}

func trimIMMarkdownSpaceBounds(raw string, start int, end int) (int, int) {
	for start < end && imMarkdownSpace(raw[start]) {
		start++
	}
	for end > start && imMarkdownSpace(raw[end-1]) {
		end--
	}
	return start, end
}

func imMarkdownSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func imMarkdownEscaped(s string, pos int) bool {
	backslashes := 0
	for i := pos - 1; i >= 0 && s[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func imOutboundImageFileName(filePath string) string {
	end := len(filePath)
	if idx := strings.IndexAny(filePath, "?#"); idx >= 0 {
		end = idx
	}
	name := strings.TrimSpace(filePath[:end])
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	name = strings.Trim(name, "<>")
	if name == "" {
		return "image.png"
	}
	if !strings.Contains(name, ".") {
		return name + ".png"
	}
	return name
}
