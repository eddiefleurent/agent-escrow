package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

func newDecompositionCmd(opts *Options) *cobra.Command {
	decompositionCmd := &cobra.Command{
		Use:   "decomposition",
		Short: "Contract-first decomposition commands",
	}

	decompositionCmd.AddCommand(newDecompositionCreateCmd(opts))
	decompositionCmd.AddCommand(newDecompositionListCmd(opts))
	decompositionCmd.AddCommand(newDecompositionGetCmd(opts))
	decompositionCmd.AddCommand(newDecompositionFinalizeCmd(opts))

	return decompositionCmd
}

func newDecompositionCreateCmd(opts *Options) *cobra.Command {
	var jsonBody string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a decomposition proposal",
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := payloadFromJSONString(jsonBody, true)
			if err != nil {
				return err
			}
			return runPost(cmd, opts, "/api/v1/decompositions", body)
		},
	}
	cmd.Flags().StringVar(&jsonBody, "json", "", "Inline JSON request body")
	return cmd
}

func newDecompositionListCmd(opts *Options) *cobra.Command {
	var buyer string
	var status string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List decomposition proposals",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGet(cmd, opts, "/api/v1/decompositions", map[string]string{
				"buyer":  buyer,
				"status": status,
			})
		},
	}
	cmd.Flags().StringVar(&buyer, "buyer", "", "Filter by buyer address")
	cmd.Flags().StringVar(&status, "status", "", "Filter by status (draft|valid|finalized)")
	return cmd
}

func newDecompositionGetCmd(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get decomposition by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGet(cmd, opts, "/api/v1/decompositions/"+args[0], nil)
		},
	}
}

func newDecompositionFinalizeCmd(opts *Options) *cobra.Command {
	var jsonBody string
	cmd := &cobra.Command{
		Use:   "finalize <id>",
		Short: "Finalize decomposition into RFQs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := payloadFromJSONString(jsonBody, true)
			if err != nil {
				return err
			}
			return runPost(cmd, opts, "/api/v1/decompositions/"+args[0]+"/finalize", body)
		},
	}
	cmd.Flags().StringVar(&jsonBody, "json", "", "Inline JSON request body")
	return cmd
}

func payloadFromJSONString(raw string, required bool) (any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if required {
			return nil, errors.New("request body required (--json)")
		}
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("parse request JSON: %w", err)
	}
	if err := decoder.Decode(new(struct{})); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("parse request JSON: trailing data after first JSON value")
		}
		return nil, fmt.Errorf("parse request JSON: %w", err)
	}
	return payload, nil
}
