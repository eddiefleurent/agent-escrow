package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/cobra"
)

func newEventsCmd(opts *Options) *cobra.Command {
	eventsCmd := &cobra.Command{
		Use:   "events",
		Short: "Event streaming commands",
	}

	var escrowID string
	var granularity string
	subscribeCmd := &cobra.Command{
		Use:   "subscribe",
		Short: "Subscribe to SSE events",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := "/api/v1/events"
			if escrowID != "" {
				path = fmt.Sprintf("/api/v1/escrows/%s/events", escrowID)
			}
			ctx, cancel := streamContext(opts)
			defer cancel()
			return streamSSE(ctx, cmd.OutOrStdout(), newClient(opts), path, map[string]string{"granularity": granularity})
		},
	}
	subscribeCmd.Flags().StringVar(&escrowID, "escrow-id", "", "Escrow id filter")
	subscribeCmd.Flags().StringVar(&granularity, "granularity", "L1", "Event granularity: L0|L1|L2|L3")
	eventsCmd.AddCommand(subscribeCmd)

	return eventsCmd
}

func streamContext(opts *Options) (context.Context, context.CancelFunc) {
	if opts.Timeout > 0 {
		return context.WithTimeout(context.Background(), opts.Timeout)
	}
	return context.WithCancel(context.Background())
}

func streamSSE(ctx context.Context, out io.Writer, client *Client, path string, query map[string]string) error {
	fullURL := client.baseURL + path
	q := buildQuery(query)
	if len(q) > 0 {
		fullURL += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, http.NoBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("read error response: %w", readErr)
		}
		return parseAPIError(resp.StatusCode, body)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
			return fmt.Errorf("write stream output: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("stream read error: %w", err)
	}
	return nil
}
