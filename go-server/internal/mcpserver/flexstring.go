package mcpserver

import (
	"encoding/json"
	"fmt"
)

// FlexibleString accepts both JSON strings ("3600") and JSON numbers (3600),
// normalizing to a Go string. MCP tool callers (LLMs) often send numbers as
// bare integers; the strict `string` type rejects those with an opaque
// "invalid json" error. This type eliminates that friction.
type FlexibleString string

func (f *FlexibleString) UnmarshalJSON(data []byte) error {
	// Try string first (most common for well-formed callers).
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*f = FlexibleString(s)
		return nil
	}

	// Try number (LLMs often send bare integers).
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		*f = FlexibleString(n.String())
		return nil
	}

	return fmt.Errorf("expected string or number, got %s", string(data))
}

func (f FlexibleString) String() string {
	return string(f)
}
