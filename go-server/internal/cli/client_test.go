package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNewClientTrimsBaseURLAndSetsTimeout(t *testing.T) {
	t.Parallel()

	c := NewClient("http://localhost:8080///", 3*time.Second)
	if c.baseURL != "http://localhost:8080" {
		t.Fatalf("expected trimmed baseURL, got %q", c.baseURL)
	}
	if c.httpClient.Timeout != 3*time.Second {
		t.Fatalf("expected timeout 3s, got %s", c.httpClient.Timeout)
	}
}

func TestClientGetAndPost(t *testing.T) {
	t.Parallel()

	errCh := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" && r.Method == http.MethodGet {
			if r.URL.RawQuery != "detail=full" {
				errCh <- fmt.Errorf("expected query detail=full, got %q", r.URL.RawQuery)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
			errCh <- nil
			return
		}
		if r.URL.Path == "/submit" && r.Method == http.MethodPost {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				errCh <- fmt.Errorf("decode request body: %w", err)
				return
			}
			if body["value"] != "x" {
				errCh <- fmt.Errorf("expected value x, got %v", body["value"])
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"accepted": true})
			errCh <- nil
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, 0)

	getPayload, err := client.Get(context.Background(), "health", url.Values{"detail": []string{"full"}})
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	if !strings.Contains(string(getPayload), `"status":"ok"`) {
		t.Fatalf("unexpected GET payload: %s", getPayload)
	}
	if handlerErr := <-errCh; handlerErr != nil {
		t.Fatalf("GET handler error: %v", handlerErr)
	}

	postPayload, err := client.Post(context.Background(), "submit", map[string]string{"value": "x"})
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	if !strings.Contains(string(postPayload), `"accepted":true`) {
		t.Fatalf("unexpected POST payload: %s", postPayload)
	}
	if handlerErr := <-errCh; handlerErr != nil {
		t.Fatalf("POST handler error: %v", handlerErr)
	}
}

func TestClientParsesAPIError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad input"}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, 0)
	_, err := client.Get(context.Background(), "/anything", nil)
	if err == nil {
		t.Fatal("expected API error")
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T (%v)", err, err)
	}
	if apiErr.StatusCode != http.StatusBadRequest || apiErr.Message != "bad input" {
		t.Fatalf("unexpected APIError: %+v", apiErr)
	}
}
