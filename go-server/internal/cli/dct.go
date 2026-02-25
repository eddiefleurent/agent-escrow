package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

func newDCTCmd(opts *Options) *cobra.Command {
	dctCmd := &cobra.Command{Use: "dct", Short: "Delegation Capability Token commands"}
	dctCmd.AddCommand(newDCTPostCmd(opts, "mint", "Mint a DCT", "/api/v1/dcts/mint", true))
	dctCmd.AddCommand(newDCTPostCmd(opts, "delegate", "Delegate a DCT", "/api/v1/dcts/delegate", true))
	dctCmd.AddCommand(newDCTPostCmd(opts, "introspect", "Introspect a DCT", "/api/v1/dcts/introspect", true))
	dctCmd.AddCommand(newDCTPostCmd(opts, "revoke", "Revoke a DCT", "/api/v1/dcts/revoke", true))
	dctCmd.AddCommand(newDCTEscrowListCmd(opts))
	return dctCmd
}

func newDCTPostCmd(opts *Options, use, short, path string, required bool) *cobra.Command {
	pf := payloadFlags{}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := payloadFromFlags(pf, required)
			if err != nil {
				return err
			}
			return runPost(cmd, opts, path, body)
		},
	}
	attachPayloadFlags(cmd, &pf)
	return cmd
}

func newDCTEscrowListCmd(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "list-escrow <escrow-id>",
		Short: "List DCTs for an escrow",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			escrowID, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil || escrowID <= 0 {
				return fmt.Errorf("escrow-id must be a positive integer")
			}
			return runGet(cmd, opts, "/api/v1/escrows/"+strconv.FormatInt(escrowID, 10)+"/dcts", nil)
		},
	}
}
