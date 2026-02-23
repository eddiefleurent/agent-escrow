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

func TestWriteOutput_InvalidFormat(t *testing.T) {
	var out bytes.Buffer
	err := WriteOutput(&out, "yaml", json.RawMessage("{}"))
	if err == nil {
		t.Fatal("expected invalid format error")
	}
}

func TestWriteOutput_TextObjectSortedKeys(t *testing.T) {
	var out bytes.Buffer
	if err := WriteOutput(&out, outputText, json.RawMessage(`{"b":1,"a":2}`)); err != nil {
		t.Fatalf("WriteOutput: %v", err)
	}
	if got := out.String(); got != "a: 2\nb: 1\n" {
		t.Fatalf("unexpected output %q", got)
	}
}

func TestWriteText_ObjectNullValue(t *testing.T) {
	var out bytes.Buffer
	if err := writeText(&out, json.RawMessage(`{"key":null}`)); err != nil {
		t.Fatalf("writeText: %v", err)
	}
	if got := out.String(); got != "key: null\n" {
		t.Fatalf("unexpected output %q", got)
	}
}

func TestWritePrettyJSON_ArrayFallback(t *testing.T) {
	var out bytes.Buffer
	if err := writePrettyJSON(&out, json.RawMessage(`[]`)); err != nil {
		t.Fatalf("writePrettyJSON: %v", err)
	}
	if got := out.String(); got != "[]\n" {
		t.Fatalf("unexpected output %q", got)
	}
}

func TestWriteOutput_TextArrayFallsBackToJSON(t *testing.T) {
	var out bytes.Buffer
	if err := WriteOutput(&out, outputText, json.RawMessage(`[]`)); err != nil {
		t.Fatalf("WriteOutput: %v", err)
	}
	if got := out.String(); got != "[]\n" {
		t.Fatalf("unexpected output %q", got)
	}
}

func TestWriteOutput_TextStringFallsBackToJSON(t *testing.T) {
	var out bytes.Buffer
	if err := WriteOutput(&out, outputText, json.RawMessage(`"raw"`)); err != nil {
		t.Fatalf("WriteOutput: %v", err)
	}
	if got := out.String(); got != "\"raw\"\n" {
		t.Fatalf("unexpected output %q", got)
	}
}

func TestWriteOutput_InvalidJSON(t *testing.T) {
	var out bytes.Buffer
	err := WriteOutput(&out, outputJSON, json.RawMessage("{"))
	if err == nil {
		t.Fatal("expected invalid json error")
	}
}

func TestWritePrettyJSON_InvalidJSON(t *testing.T) {
	var out bytes.Buffer
	err := writePrettyJSON(&out, json.RawMessage("{"))
	if err == nil {
		t.Fatal("expected invalid json error")
	}
}
