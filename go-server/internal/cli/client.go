package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	statusCodeClientErrorMin = 400
	statusCodeClientErrorMax = 499
	statusCodeServerErrorMin = 500
)

// Client is a thin HTTP client over the escrow HTTP API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// APIError wraps non-2xx API responses.
type APIError struct {
	StatusCode int
	Message    string
	Body       string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("api (%d): %s", e.StatusCode, e.Message)
	}
	if e.Body != "" {
		return fmt.Sprintf("api (%d): %s", e.StatusCode, e.Body)
	}
	return fmt.Sprintf("api (%d)", e.StatusCode)
}

// NewClient creates an API client with an optional timeout.
func NewClient(baseURL string, timeout time.Duration) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	httpClient := &http.Client{}
	if timeout > 0 {
		httpClient.Timeout = timeout
	}
	return &Client{baseURL: baseURL, httpClient: httpClient}
}

// Get executes a GET request and returns the raw JSON body.
func (c *Client) Get(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	return c.doJSON(ctx, http.MethodGet, path, query, nil)
}

// Post executes a POST request and returns the raw JSON body.
func (c *Client) Post(ctx context.Context, path string, body any) (json.RawMessage, error) {
	return c.doJSON(ctx, http.MethodPost, path, nil, body)
}

func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, body any) (json.RawMessage, error) {
	normalizedPath := path
	if normalizedPath != "" && !strings.HasPrefix(normalizedPath, "/") {
		normalizedPath = "/" + normalizedPath
	}
	fullURL := c.baseURL + normalizedPath
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}

	var bodyReader io.Reader = http.NoBody
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	// #nosec G704 -- This CLI intentionally connects to a user-configured API base URL.
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, parseAPIError(resp.StatusCode, respBody)
	}

	if len(bytes.TrimSpace(respBody)) == 0 {
		return json.RawMessage("{}"), nil
	}
	return json.RawMessage(respBody), nil
}

func parseAPIError(statusCode int, body []byte) error {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err == nil {
		if msg, ok := payload["error"].(string); ok && msg != "" {
			return &APIError{StatusCode: statusCode, Message: msg, Body: strings.TrimSpace(string(body))}
		}
	}
	return &APIError{StatusCode: statusCode, Body: strings.TrimSpace(string(body))}
}

// ExitCode maps known error types to CLI exit codes.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode >= statusCodeClientErrorMin && apiErr.StatusCode <= statusCodeClientErrorMax {
			return 1
		}
		if apiErr.StatusCode >= statusCodeServerErrorMin {
			return 2
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return 2
	}

	// URL/network errors indicate transport/server reachability issues.
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return 2
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return 2
	}

	return 1
}
