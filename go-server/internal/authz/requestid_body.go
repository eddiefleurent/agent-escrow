package authz

import "net/http"

// RequestIDHeader is the correlation header echoed on every HTTP response.
const RequestIDHeader = "X-Request-ID"

// WithRequestIDField copies the response's X-Request-ID into a JSON error
// object so clients can correlate the body with the header. Only maps that
// carry an "error" key and no request_id yet are changed; other values are
// returned as is.
func WithRequestIDField(h http.Header, v any) any {
	id := h.Get(RequestIDHeader)
	if id == "" {
		return v
	}
	switch body := v.(type) {
	case map[string]string:
		if _, ok := body["error"]; ok && body["request_id"] == "" {
			body["request_id"] = id
		}
	case map[string]any:
		if _, ok := body["error"]; ok {
			if _, exists := body["request_id"]; !exists {
				body["request_id"] = id
			}
		}
	}
	return v
}
