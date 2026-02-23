package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	defaultServerURL = "http://localhost:8080"
)

// Options are global CLI runtime options.
type Options struct {
	ServerURL string
	Output    string
	Timeout   time.Duration
}

// NewRootCmd creates the CLI root command.
func NewRootCmd(stdout, stderr io.Writer) *cobra.Command {
	opts := &Options{}

	rootCmd := &cobra.Command{
		Use:           "escrow-cli",
		Short:         "CLI for the escrow marketplace HTTP API",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if opts.ServerURL == "" {
				opts.ServerURL = defaultServerURL
			}
			if opts.Output != outputText && opts.Output != outputJSON {
				return fmt.Errorf("invalid --output value %q (expected text or json)", opts.Output)
			}
			opts.ServerURL = strings.TrimRight(opts.ServerURL, "/")
			return nil
		},
	}

	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)

	rootCmd.PersistentFlags().StringVar(&opts.ServerURL, "server", serverURLDefault(), "Escrow server URL")
	rootCmd.PersistentFlags().StringVar(&opts.Output, "output", outputText, "Output format: text|json")
	rootCmd.PersistentFlags().DurationVar(&opts.Timeout, "timeout", 0, "Per-request timeout (0 disables timeout)")

	rootCmd.AddCommand(newHealthCmd(opts))
	rootCmd.AddCommand(newEscrowCmd(opts))
	rootCmd.AddCommand(newRFQCmd(opts))
	rootCmd.AddCommand(newBidCmd(opts))
	rootCmd.AddCommand(newReputationCmd(opts))
	rootCmd.AddCommand(newEventsCmd(opts))
	rootCmd.AddCommand(newEmergencyCmd(opts))
	rootCmd.AddCommand(newAP2Cmd(opts))

	return rootCmd
}

func serverURLDefault() string {
	return firstNonEmpty(strings.TrimSpace(os.Getenv("ESCROW_SERVER_URL")), defaultServerURL)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return defaultServerURL
}

func newClient(opts *Options) *Client {
	serverURL := opts.ServerURL
	if serverURL == "" {
		serverURL = defaultServerURL
	}
	return NewClient(serverURL, opts.Timeout)
}

func commandContext(opts *Options) (context.Context, context.CancelFunc) {
	if opts.Timeout <= 0 {
		return context.Background(), func() {}
	}
	return context.WithTimeout(context.Background(), opts.Timeout)
}

func runGet(cmd *cobra.Command, opts *Options, path string, query map[string]string) error {
	ctx, cancel := commandContext(opts)
	defer cancel()

	payload, err := newClient(opts).Get(ctx, path, buildQuery(query))
	if err != nil {
		return err
	}
	return WriteOutput(cmd.OutOrStdout(), opts.Output, payload)
}

func runPost(cmd *cobra.Command, opts *Options, path string, body any) error {
	ctx, cancel := commandContext(opts)
	defer cancel()

	payload, err := newClient(opts).Post(ctx, path, body)
	if err != nil {
		return err
	}
	return WriteOutput(cmd.OutOrStdout(), opts.Output, payload)
}
