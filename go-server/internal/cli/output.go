package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

const (
	outputText = "text"
	outputJSON = "json"
)

// WriteOutput renders response payload in either text or JSON format.
func WriteOutput(w io.Writer, format string, payload json.RawMessage) error {
	switch format {
	case outputJSON:
		return writePrettyJSON(w, payload)
	case outputText:
		return writeText(w, payload)
	default:
		return fmt.Errorf("invalid output format %q (expected text or json)", format)
	}
}

func writePrettyJSON(w io.Writer, payload json.RawMessage) error {
	payloadTrim := bytes.TrimSpace(payload)
	if len(payloadTrim) == 0 {
		_, err := fmt.Fprintln(w, "{}")
		return err
	}
	var prettyJSON bytes.Buffer
	if err := json.Indent(&prettyJSON, payloadTrim, "", "  "); err != nil {
		return fmt.Errorf("invalid response json: %w", err)
	}
	_, err := fmt.Fprintln(w, prettyJSON.String())
	return err
}

func writeText(w io.Writer, payload json.RawMessage) error {
	payloadTrim := bytes.TrimSpace(payload)
	if len(payloadTrim) == 0 {
		_, err := io.WriteString(w, "{}\n")
		return err
	}

	var v any
	if err := json.Unmarshal(payloadTrim, &v); err != nil {
		return fmt.Errorf("invalid response json: %w", err)
	}

	obj, ok := v.(map[string]any)
	if !ok {
		return writePrettyJSON(w, payloadTrim)
	}

	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		_, err := io.WriteString(w, "{}\n")
		return err
	}

	var primitiveOutput bytes.Buffer
	for _, key := range keys {
		switch val := obj[key].(type) {
		case string, float64, bool, nil:
			if val == nil {
				if _, err := fmt.Fprintf(&primitiveOutput, "%s: null\n", key); err != nil {
					return err
				}
				continue
			}
			if _, err := fmt.Fprintf(&primitiveOutput, "%s: %v\n", key, val); err != nil {
				return err
			}
		default:
			return writePrettyJSON(w, payloadTrim)
		}
	}
	_, err := io.Copy(w, &primitiveOutput)
	return err
}
