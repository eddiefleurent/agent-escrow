package x402

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client communicates with the CDP x402 facilitator API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates an x402 facilitator client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Verify validates an EIP-3009 authorization payload without settling.
func (c *Client) Verify(ctx context.Context, payload PaymentPayload) (*VerifyResponse, error) {
	body, err := json.Marshal(VerifyRequest{Payment: payload})
	if err != nil {
		return nil, fmt.Errorf("marshal verify request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/verify", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create verify request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("verify request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read verify response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("verify failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result VerifyResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal verify response: %w", err)
	}
	return &result, nil
}

// Settle submits an EIP-3009 authorization for on-chain settlement.
func (c *Client) Settle(ctx context.Context, payload PaymentPayload) (*SettleResponse, error) {
	body, err := json.Marshal(SettleRequest{Payment: payload})
	if err != nil {
		return nil, fmt.Errorf("marshal settle request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/settle", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create settle request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("settle request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read settle response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("settle failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result SettleResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal settle response: %w", err)
	}
	return &result, nil
}
