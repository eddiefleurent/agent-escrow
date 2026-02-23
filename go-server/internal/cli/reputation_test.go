package cli

import (
	"strings"
	"testing"
)

func TestReputationGetRejectsInvalidRole(t *testing.T) {
	cmd := newReputationCmd(&Options{
		ServerURL: "http://127.0.0.1:1",
		Output:    outputJSON,
	})
	cmd.SetArgs([]string{"get", "0x1111111111111111111111111111111111111111", "--role", "admin"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected invalid role error")
	}
	if !strings.Contains(err.Error(), `invalid --role value "admin" (expected buyer or worker)`) {
		t.Fatalf("unexpected error: %v", err)
	}
}
