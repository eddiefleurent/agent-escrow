package cli

import "github.com/spf13/cobra"

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
			path := "/api/v1/reputation/" + args[0]
			return runGet(cmd, opts, path, map[string]string{"role": role})
		},
	}
	getCmd.Flags().StringVar(&role, "role", "", "Role filter: buyer|worker")
	reputationCmd.AddCommand(getCmd)

	return reputationCmd
}
