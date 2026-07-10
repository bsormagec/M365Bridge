package toolcalling

import (
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// ContentStreamExtractor incrementally extracts choices[0].message.content
// from the simulated chat-completion JSON envelope.
type ContentStreamExtractor struct {
	buffer string
	text   string
	done   bool
}

// Feed appends a raw response chunk and returns only newly decoded content.
func (e *ContentStreamExtractor) Feed(chunk string) string {
	if e.done || chunk == "" {
		return ""
	}
	e.buffer += chunk
	decoded, found, complete := decodeSimulatedContentPrefix(e.buffer)
	if !found || len(decoded) < len(e.text) {
		return ""
	}
	delta := decoded[len(e.text):]
	e.text = decoded
	e.done = complete
	return delta
}

// Text returns all decoded assistant content seen so far.
func (e *ContentStreamExtractor) Text() string {
	return e.text
}

func decodeSimulatedContentPrefix(raw string) (string, bool, bool) {
	choices := strings.Index(raw, `"choices"`)
	if choices < 0 {
		return "", false, false
	}
	messageOffset := strings.Index(raw[choices:], `"message"`)
	if messageOffset < 0 {
		return "", false, false
	}
	message := choices + messageOffset
	contentOffset := strings.Index(raw[message:], `"content"`)
	if contentOffset < 0 {
		return "", false, false
	}
	content := message + contentOffset + len(`"content"`)
	for content < len(raw) && isJSONSpace(raw[content]) {
		content++
	}
	if content >= len(raw) || raw[content] != ':' {
		return "", false, false
	}
	content++
	for content < len(raw) && isJSONSpace(raw[content]) {
		content++
	}
	if content >= len(raw) {
		return "", true, false
	}
	if strings.HasPrefix(raw[content:], "null") {
		return "", true, true
	}
	if raw[content] != '"' {
		return "", false, false
	}

	var decoded strings.Builder
	for index := content + 1; index < len(raw); {
		switch raw[index] {
		case '"':
			return decoded.String(), true, true
		case '\\':
			value, consumed, complete := decodeJSONEscape(raw[index:])
			if !complete {
				return decoded.String(), true, false
			}
			decoded.WriteString(value)
			index += consumed
		default:
			decoded.WriteByte(raw[index])
			index++
		}
	}
	return decoded.String(), true, false
}

func decodeJSONEscape(raw string) (string, int, bool) {
	if len(raw) < 2 || raw[0] != '\\' {
		return "", 0, false
	}
	switch raw[1] {
	case '"', '\\', '/':
		return string(raw[1]), 2, true
	case 'b':
		return "\b", 2, true
	case 'f':
		return "\f", 2, true
	case 'n':
		return "\n", 2, true
	case 'r':
		return "\r", 2, true
	case 't':
		return "\t", 2, true
	case 'u':
		first, ok := decodeHexRune(raw, 2)
		if !ok {
			return "", 0, false
		}
		consumed := 6
		if utf16.IsSurrogate(first) {
			if !utf16.IsSurrogate(first) || first < 0xD800 || first > 0xDBFF {
				return string(utf8.RuneError), consumed, true
			}
			if len(raw) < 12 || raw[6:8] != `\u` {
				return "", 0, false
			}
			second, secondOK := decodeHexRune(raw, 8)
			if !secondOK {
				return "", 0, false
			}
			combined := utf16.DecodeRune(first, second)
			if combined == utf8.RuneError {
				return string(utf8.RuneError), 12, true
			}
			return string(combined), 12, true
		}
		return string(first), consumed, true
	default:
		return "", 0, false
	}
}

func decodeHexRune(raw string, offset int) (rune, bool) {
	if len(raw) < offset+4 {
		return 0, false
	}
	value, err := strconv.ParseUint(raw[offset:offset+4], 16, 16)
	if err != nil {
		return 0, false
	}
	return rune(value), true
}

func isJSONSpace(value byte) bool {
	switch value {
	case ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}
