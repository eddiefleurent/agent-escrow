package cli

import "github.com/spf13/cobra"

func newHealthCmd(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Check server health",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGet(cmd, opts, "/api/v1/health", nil)
		},
	}
}
