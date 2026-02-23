package cli

import (
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
)

func newBidCmd(opts *Options) *cobra.Command {
	bidCmd := &cobra.Command{
		Use:   "bid",
		Short: "Bid commands",
	}

	bidCmd.AddCommand(newBidPlaceCmd(opts))
	bidCmd.AddCommand(newBidListCmd(opts))
	bidCmd.AddCommand(newBidAcceptCmd(opts))

	return bidCmd
}

func newBidPlaceCmd(opts *Options) *cobra.Command {
	pf := payloadFlags{}
	cmd := &cobra.Command{
		Use:   "place <rfq-id>",
		Short: "Place a bid on an RFQ",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := payloadFromFlags(pf, true)
			if err != nil {
				return err
			}
			rfqID := url.PathEscape(args[0])
			return runPost(cmd, opts, fmt.Sprintf("/api/v1/rfqs/%s/bids", rfqID), body)
		},
	}
	attachPayloadFlags(cmd, &pf)
	return cmd
}

func newBidListCmd(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "list <rfq-id>",
		Short: "List bids for an RFQ",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rfqID := url.PathEscape(args[0])
			return runGet(cmd, opts, fmt.Sprintf("/api/v1/rfqs/%s/bids", rfqID), nil)
		},
	}
}

func newBidAcceptCmd(opts *Options) *cobra.Command {
	pf := payloadFlags{}
	cmd := &cobra.Command{
		Use:   "accept <rfq-id>",
		Short: "Accept bid for an RFQ",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := payloadFromFlags(pf, true)
			if err != nil {
				return err
			}
			rfqID := url.PathEscape(args[0])
			return runPost(cmd, opts, fmt.Sprintf("/api/v1/rfqs/%s/accept", rfqID), body)
		},
	}
	attachPayloadFlags(cmd, &pf)
	return cmd
}
