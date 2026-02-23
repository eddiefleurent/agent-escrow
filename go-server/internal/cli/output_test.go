package cli

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestWritePrettyJSON_WhitespacePayload(t *testing.T) {
	var out bytes.Buffer
	if err := writePrettyJSON(&out, json.RawMessage(" \n\t ")); err != nil {
		t.Fatalf("writePrettyJSON: %v", err)
	}
	if got := out.String(); got != "{}\n" {
		t.Fatalf("expected empty object output, got %q", got)
	}
}

func TestWriteText_WhitespacePayload(t *testing.T) {
	var out bytes.Buffer
	if err := writeText(&out, json.RawMessage(" \n\t ")); err != nil {
		t.Fatalf("writeText: %v", err)
	}
	if got := out.String(); got != "{}\n" {
		t.Fatalf("expected empty object output, got %q", got)
	}
}

func TestWriteText_TrimmedBeforeUnmarshal(t *testing.T) {
	var out bytes.Buffer
	if err := writeText(&out, json.RawMessage("\n {\"ok\":true} \n")); err != nil {
		t.Fatalf("writeText: %v", err)
	}
	if got := out.String(); got != "ok: true\n" {
		t.Fatalf("unexpected output %q", got)
	}
}
