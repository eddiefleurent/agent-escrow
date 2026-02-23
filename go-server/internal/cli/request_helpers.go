package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

type payloadFlags struct {
	inline string
	file   string
}

func attachPayloadFlags(cmd *cobra.Command, pf *payloadFlags) {
	cmd.Flags().StringVar(&pf.inline, "data", "", "Inline JSON request body")
	cmd.Flags().StringVar(&pf.file, "data-file", "", "Path to JSON request body file")
}

func payloadFromFlags(pf payloadFlags, required bool) (any, error) {
	if pf.inline != "" && pf.file != "" {
		return nil, errors.New("use only one of --data or --data-file")
	}
	if pf.inline == "" && pf.file == "" {
		if required {
			return nil, errors.New("request body required (--data or --data-file)")
		}
		return nil, nil
	}

	var raw string
	if pf.inline != "" {
		raw = pf.inline
	} else {
		data, err := os.ReadFile(pf.file)
		if err != nil {
			return nil, fmt.Errorf("read data file: %w", err)
		}
		raw = string(data)
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

func buildQuery(values map[string]string) url.Values {
	query := make(url.Values)
	for k, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			query.Set(k, v)
		}
	}
	return query
}
