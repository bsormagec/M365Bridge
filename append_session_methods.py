import sys

addition = """
// sessionIDForMessages returns an explicit session ID from the request headers.
// It returns the X-Session-Id header value when present, and an empty string
// otherwise. Implicit (hash-derived) session IDs are intentionally not
// returned here; callers that want a fallback should use getSessionID instead.
func (api *APIServer) sessionIDForMessages(r *http.Request, _ []payload.Message) string {
\treturn r.Header.Get("X-Session-Id")
}

// sessionIDForRequest resolves a session ID from explicit sources only.
// Priority: body session_id > body user field > X-Session-Id header.
// Returns an empty string when none of the explicit sources are set.
func (api *APIServer) sessionIDForRequest(r *http.Request, sessionID, userID string, messages []payload.Message) string {
\tif sessionID != "" {
\t\treturn sessionID
\t}
\tif userID != "" {
\t\treturn userID
\t}
\treturn api.sessionIDForMessages(r, messages)
}
"""

path = "pkg/servers/api.go"
content = open(path).read()
with open(path, "w") as f:
    f.write(content + addition)
lines = len((content + addition).splitlines())
print(f"done, total lines: {lines}")