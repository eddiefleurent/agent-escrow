package mcpserver

import "testing"

func TestFlexibleStringUnmarshalJSON(t *testing.T) {
	t.Parallel()

	var fs FlexibleString
	if err := fs.UnmarshalJSON([]byte(`"3600"`)); err != nil {
		t.Fatalf("string unmarshal failed: %v", err)
	}
	if fs.String() != "3600" {
		t.Fatalf("expected 3600, got %q", fs.String())
	}

	if err := fs.UnmarshalJSON([]byte(`3600`)); err != nil {
		t.Fatalf("number unmarshal failed: %v", err)
	}
	if fs.String() != "3600" {
		t.Fatalf("expected numeric normalization to 3600, got %q", fs.String())
	}

	if err := fs.UnmarshalJSON([]byte(`{"bad":true}`)); err == nil {
		t.Fatal("expected error for unsupported JSON type")
	}
}
