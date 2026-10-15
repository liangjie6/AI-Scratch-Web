package orchestration

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/ai-scratch-web/backend/internal/jsonx"
)

// StreamProtocolError reports a malformed tagged provider stream.
type StreamProtocolError struct{ Message string }

func (e *StreamProtocolError) Error() string { return e.Message }

func newStreamProtocolError(format string, args ...any) *StreamProtocolError {
	if len(args) == 0 {
		return &StreamProtocolError{Message: format}
	}
	return &StreamProtocolError{Message: fmt.Sprintf(format, args...)}
}

// StreamFrame is one parsed frame of the tagged provider stream.
type StreamFrame struct {
	Kind    string
	Content string
	Attrs   map[string]string
}

var rootTags = []string{"requirements", "task_spec", "blueprint", "plan", "manifest", "resource_op"}
var programChildTags = []string{"metadata", "shared_state"}

// UnifiedFrameParser parses the tagged provider stream without assuming provider
// chunk boundaries.
type UnifiedFrameParser struct {
	buffer        string
	insideProgram bool
	programAttrs  map[string]string
}

// NewUnifiedFrameParser builds an empty parser.
func NewUnifiedFrameParser() *UnifiedFrameParser {
	return &UnifiedFrameParser{programAttrs: map[string]string{}}
}

// Feed appends provider text and drains every complete frame.
func (p *UnifiedFrameParser) Feed(text string) ([]StreamFrame, error) {
	if text != "" {
		p.buffer += text
	}
	frames := []StreamFrame{}
	for {
		frame, err := p.drainOne()
		if err != nil {
			return frames, err
		}
		if frame == nil {
			return frames, nil
		}
		frames = append(frames, *frame)
	}
}

// Finish validates the tail of the stream and drains any remaining frame.
func (p *UnifiedFrameParser) Finish() ([]StreamFrame, error) {
	frames, err := p.Feed("")
	if err != nil {
		return frames, err
	}
	if p.insideProgram {
		return frames, newStreamProtocolError("program frame is incomplete")
	}
	if strings.TrimSpace(p.buffer) != "" {
		return frames, newStreamProtocolError("stream contains an incomplete or unexpected frame")
	}
	p.buffer = ""
	return frames, nil
}

func (p *UnifiedFrameParser) drainOne() (*StreamFrame, error) {
	p.buffer = strings.TrimLeft(p.buffer, " \t\r\n\v\f")
	if p.buffer == "" {
		return nil, nil
	}
	if p.insideProgram {
		return p.drainProgram()
	}
	if strings.HasPrefix(p.buffer, "</") {
		return nil, newStreamProtocolError("unexpected closing tag")
	}
	if strings.HasPrefix(p.buffer, "<program") {
		if !isTagPrefix(p.buffer, "program") {
			return nil, newStreamProtocolError("invalid program opening tag")
		}
		end := strings.IndexByte(p.buffer, '>')
		if end < 0 {
			return nil, nil
		}
		opening := p.buffer[:end+1]
		attrs, err := parseAttrs(opening[len("<program") : len(opening)-1])
		if err != nil {
			return nil, err
		}
		p.programAttrs = attrs
		p.buffer = p.buffer[end+1:]
		p.insideProgram = true
		return &StreamFrame{Kind: "program_start", Attrs: cloneAttrs(p.programAttrs)}, nil
	}
	for _, tag := range rootTags {
		opening := "<" + tag + ">"
		if strings.HasPrefix(p.buffer, opening) {
			closing := "</" + tag + ">"
			end := strings.Index(p.buffer[len(opening):], closing)
			if end < 0 {
				return nil, nil
			}
			content := p.buffer[len(opening) : len(opening)+end]
			p.buffer = p.buffer[len(opening)+end+len(closing):]
			return &StreamFrame{Kind: tag, Content: content}, nil
		}
	}
	if strings.HasPrefix(p.buffer, "<") && isIncompleteTagPrefix(p.buffer, append(append([]string{}, rootTags...), "program")) {
		return nil, nil
	}
	return nil, newStreamProtocolError("unexpected top-level output")
}

func (p *UnifiedFrameParser) drainProgram() (*StreamFrame, error) {
	p.buffer = strings.TrimLeft(p.buffer, " \t\r\n\v\f")
	if strings.HasPrefix(p.buffer, "</program>") {
		p.buffer = p.buffer[len("</program>"):]
		attrs := p.programAttrs
		p.programAttrs = map[string]string{}
		p.insideProgram = false
		return &StreamFrame{Kind: "program_end", Attrs: cloneAttrs(attrs)}, nil
	}
	for _, tag := range programChildTags {
		opening := "<" + tag + ">"
		if strings.HasPrefix(p.buffer, opening) {
			closing := "</" + tag + ">"
			end := strings.Index(p.buffer[len(opening):], closing)
			if end < 0 {
				return nil, nil
			}
			content := p.buffer[len(opening) : len(opening)+end]
			p.buffer = p.buffer[len(opening)+end+len(closing):]
			return &StreamFrame{Kind: tag, Content: content, Attrs: cloneAttrs(p.programAttrs)}, nil
		}
	}
	if strings.HasPrefix(p.buffer, "<script") {
		if !isTagPrefix(p.buffer, "script") {
			return nil, newStreamProtocolError("invalid script opening tag")
		}
		openingEnd := strings.IndexByte(p.buffer, '>')
		if openingEnd < 0 {
			return nil, nil
		}
		opening := p.buffer[:openingEnd+1]
		attrs, err := parseAttrs(opening[len("<script") : len(opening)-1])
		if err != nil {
			return nil, err
		}
		if len(attrs) != 2 || !hasKey(attrs, "index") || !hasKey(attrs, "total") {
			return nil, newStreamProtocolError("script opening requires only index and total")
		}
		closing := "</script>"
		end := strings.Index(p.buffer[openingEnd+1:], closing)
		if end < 0 {
			return nil, nil
		}
		content := p.buffer[openingEnd+1 : openingEnd+1+end]
		p.buffer = p.buffer[openingEnd+1+end+len(closing):]
		merged := cloneAttrs(p.programAttrs)
		for key, value := range attrs {
			merged[key] = value
		}
		return &StreamFrame{Kind: "script", Content: content, Attrs: merged}, nil
	}
	if strings.HasPrefix(p.buffer, "<") && isIncompleteTagPrefix(p.buffer, append(append([]string{}, programChildTags...), "script", "/program")) {
		return nil, nil
	}
	return nil, newStreamProtocolError("unexpected program child")
}

func hasKey(attrs map[string]string, key string) bool {
	_, ok := attrs[key]
	return ok
}

func cloneAttrs(attrs map[string]string) map[string]string {
	result := make(map[string]string, len(attrs))
	for key, value := range attrs {
		result[key] = value
	}
	return result
}

func isTagPrefix(value, tag string) bool {
	prefix := "<" + tag
	if value == prefix || value == prefix+">" {
		return true
	}
	if !strings.HasPrefix(value, prefix) || len(value) <= len(prefix) {
		return false
	}
	switch value[len(prefix)] {
	case ' ', '\t', '\r', '\n':
		return true
	}
	return false
}

func isIncompleteTagPrefix(value string, tags []string) bool {
	if strings.Contains(value, ">") || !strings.HasPrefix(value, "<") {
		return false
	}
	for _, tag := range tags {
		if strings.HasPrefix(tag, value[1:]) {
			return true
		}
	}
	return false
}

var attrPattern = regexp.MustCompile("([A-Za-z_][A-Za-z0-9_-]*)\\s*=\\s*(?:\"([^\"]*)\"|'([^']*)'|([^\\s]+))")

func parseAttrs(value string) (map[string]string, error) {
	attrs := map[string]string{}
	remaining := strings.TrimSpace(value)
	for remaining != "" {
		match := attrPattern.FindStringSubmatchIndex(remaining)
		if match == nil || match[0] != 0 {
			return nil, newStreamProtocolError("invalid tag attribute syntax")
		}
		key := remaining[match[2]:match[3]]
		if _, exists := attrs[key]; exists {
			return nil, newStreamProtocolError("duplicate tag attribute: %s", key)
		}
		chosen := ""
		// The three value alternatives are groups 2, 3 and 4 (match[4..9]);
		// a double-quoted value only exists in group 2, so every alternative
		// must be inspected without reading past the match slice.
		for group := 0; group < 3; group++ {
			start := match[4+group*2]
			end := match[5+group*2]
			if start >= 0 {
				chosen = remaining[start:end]
				break
			}
		}
		attrs[key] = chosen
		remaining = strings.TrimLeft(remaining[match[1]:], " \t\r\n\v\f")
	}
	return attrs, nil
}

var missingBracesPattern = regexp.MustCompile("(\\}{2,})(\\s*,\\s*\"(?:substacks|blocks|scripts|inputs|fields)\")")

// RobustJSONParse parses frame JSON with self-healing for common LLM token-level
// errors: missing closing braces before nested keys, and truncated output.
func RobustJSONParse(content string, maxPasses int) (map[string]any, error) {
	if maxPasses == 0 {
		maxPasses = 5
	}
	value := strings.TrimSpace(content)
	parsed, err := jsonx.Parse([]byte(value))
	if err == nil {
		object, ok := parsed.(map[string]any)
		if !ok {
			return nil, newStreamProtocolError("frame JSON must be an object")
		}
		return object, nil
	}
	initialErr := err
	currentText := value
	currentErr := err

	for pass := 0; pass < maxPasses; pass++ {
		errPos := syntaxErrorOffset(currentErr, len(currentText))
		matches := missingBracesPattern.FindAllStringSubmatchIndex(currentText, -1)
		candidates := make([][]int, 0, len(matches))
		for _, match := range matches {
			if match[0] < errPos {
				candidates = append(candidates, match)
			}
		}
		if len(candidates) == 0 {
			break
		}
		var bestCandidate string
		var bestErr error
		found := false
		for index := len(candidates) - 1; index >= 0; index-- {
			match := candidates[index]
			insertAt := match[4] // start of group 2
			for _, addCount := range []int{1, 2} {
				candidate := currentText[:insertAt] + strings.Repeat("}", addCount) + currentText[insertAt:]
				parsed, err := jsonx.Parse([]byte(candidate))
				if err == nil {
					object, ok := parsed.(map[string]any)
					if !ok {
						continue
					}
					return object, nil
				}
				if syntaxErrorOffset(err, len(candidate)) > errPos {
					bestCandidate = candidate
					bestErr = err
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if bestCandidate != "" && bestErr != nil {
			currentText = bestCandidate
			currentErr = bestErr
		} else {
			break
		}
	}

	// Strategy 2: complete trailing unclosed brackets and braces.
	stack := []byte{}
	inString := false
	escaped := false
	for index := 0; index < len(currentText); index++ {
		ch := currentText[index]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch ch {
		case '{', '[':
			stack = append(stack, ch)
		case '}', ']':
			if len(stack) > 0 {
				last := stack[len(stack)-1]
				if (ch == '}' && last == '{') || (ch == ']' && last == '[') {
					stack = stack[:len(stack)-1]
				}
			}
		}
	}
	if len(stack) > 0 {
		var tail strings.Builder
		for index := len(stack) - 1; index >= 0; index-- {
			if stack[index] == '{' {
				tail.WriteByte('}')
			} else {
				tail.WriteByte(']')
			}
		}
		parsed, err := jsonx.Parse([]byte(currentText + tail.String()))
		if err == nil {
			if object, ok := parsed.(map[string]any); ok {
				return object, nil
			}
		}
	}

	return nil, newStreamProtocolError("frame does not contain valid JSON: %s", initialErr.Error())
}

func syntaxErrorOffset(err error, fallback int) int {
	var syntaxError *json.SyntaxError
	if errors.As(err, &syntaxError) {
		return int(syntaxError.Offset)
	}
	return fallback
}

// ParseJSON mirrors parse_json in the Python reference.
func ParseJSON(content string) (any, error) {
	return RobustJSONParse(content, 5)
}
