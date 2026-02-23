package cli

import (
	"net/url"

	"github.com/spf13/cobra"
)

func newAP2Cmd(opts *Options) *cobra.Command {
	ap2Cmd := &cobra.Command{
		Use:   "ap2",
		Short: "AP2 mandate commands",
	}

	ap2Cmd.AddCommand(newAP2PostCmd(opts, "fund", "Fund escrow via AP2 mandate", "/api/v1/ap2/fund"))
	ap2Cmd.AddCommand(newAP2PostCmd(opts, "validate", "Validate AP2 mandate", "/api/v1/ap2/validate"))
	ap2Cmd.AddCommand(newAP2MandateCmd(opts))

	return ap2Cmd
}

func newAP2PostCmd(opts *Options, use, short, path string) *cobra.Command {
	pf := payloadFlags{}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := payloadFromFlags(pf, true)
			if err != nil {
				return err
			}
			return runPost(cmd, opts, path, body)
		},
	}
	attachPayloadFlags(cmd, &pf)
	return cmd
}

func newAP2MandateCmd(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "mandate <id>",
		Short: "Get AP2 mandate details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/api/v1/ap2/mandates/" + url.PathEscape(args[0])
			return runGet(cmd, opts, path, nil)
		},
	}
}
