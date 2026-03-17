package cli

import "testing"

func TestDecompositionCommandStructure(t *testing.T) {
	t.Parallel()

	cmd := newDecompositionCmd(testOptions())
	for _, name := range []string{"create", "list", "get", "finalize"} {
		if !hasSubcommand(cmd, name) {
			t.Fatalf("expected decomposition subcommand %q", name)
		}
	}
}

func TestPayloadFromJSONString(t *testing.T) {
	t.Parallel()

	if _, err := payloadFromJSONString("", true); err == nil {
		t.Fatal("expected required json body error")
	}

	got, err := payloadFromJSONString(`{"name":"task"}`, true)
	if err != nil {
		t.Fatalf("expected valid json payload, got %v", err)
	}
	obj, ok := got.(map[string]any)
	if !ok || obj["name"] != "task" {
		t.Fatalf("expected map payload with name=task, got %#v", got)
	}

	if _, err := payloadFromJSONString(`{"a":1} {"b":2}`, true); err == nil {
		t.Fatal("expected trailing json validation error")
	}
}
