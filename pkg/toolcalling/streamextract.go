package toolcalling

// ContentStreamExtractor buffers simulated chat-completion JSON until the
// final candidate selection is committed.
type ContentStreamExtractor struct {
	buffer string
	text   string
	done   bool
}

// Feed appends a raw response chunk. Content remains unpublished until Commit
// because a later JSON candidate can outrank an earlier complete candidate.
func (e *ContentStreamExtractor) Feed(chunk string) string {
	if e.done || chunk == "" {
		return ""
	}
	e.buffer += chunk
	return ""
}

// Commit selects the same final payload as ParseSimulatedResponse and returns
// its content exactly once.
func (e *ContentStreamExtractor) Commit(allowedToolNames []string) string {
	if e.done {
		return ""
	}
	result := ParseSimulatedResponse(e.buffer, allowedToolNames)
	e.text = result.Content
	e.done = true
	return e.text
}

// Text returns committed assistant content.
func (e *ContentStreamExtractor) Text() string {
	return e.text
}
