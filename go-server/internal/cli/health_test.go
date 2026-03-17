package cli

import "testing"

func TestHealthCommandShape(t *testing.T) {
	t.Parallel()

	cmd := newHealthCmd(testOptions())
	if cmd.Use != "health" {
		t.Fatalf("expected use=health, got %q", cmd.Use)
	}
	if cmd.RunE == nil {
		t.Fatal("expected health command to define RunE")
	}
}
