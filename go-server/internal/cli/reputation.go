package cli

import (
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
)

func newReputationCmd(opts *Options) *cobra.Command {
	reputationCmd := &cobra.Command{
		Use:   "reputation",
		Short: "Reputation commands",
	}

	var role string
	getCmd := &cobra.Command{
		Use:   "get <address>",
		Short: "Get reputation for an address",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if role != "" && role != "buyer" && role != "worker" {
				return fmt.Errorf("invalid --role value %q (expected buyer or worker)", role)
			}
			path := "/api/v1/reputation/" + url.PathEscape(args[0])
			query := map[string]string{}
			if role != "" {
				query["role"] = role
			}
			return runGet(cmd, opts, path, query)
		},
	}
	getCmd.Flags().StringVar(&role, "role", "", "Role filter: buyer|worker")
	reputationCmd.AddCommand(getCmd)

	return reputationCmd
}
