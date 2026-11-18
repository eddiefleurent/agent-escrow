package cli

import (
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

var requiredPlaceholder = regexp.MustCompile(`<[^>]+>`)

// TestRequiredPositionalArgsAreValidated walks the whole command tree and
// checks that every command declaring required positionals in its Use string
// (e.g. "commit <rfq-id>") rejects missing args and accepts the declared count.
// Handlers index args[i] directly, so a missing Args validator turns a usage
// error into an index-out-of-range panic.
func TestRequiredPositionalArgsAreValidated(t *testing.T) {
	t.Parallel()

	checked := 0
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
		required := len(requiredPlaceholder.FindAllString(cmd.Use, -1))
		if required == 0 {
			return
		}
		checked++
		path := cmd.CommandPath()
		if cmd.Args == nil {
			t.Errorf("%s: declares %d required arg(s) but has no Args validator", path, required)
			return
		}
		if err := cmd.Args(cmd, nil); err == nil {
			t.Errorf("%s: expected validation failure with no args", path)
		}
		args := strings.Fields(strings.Repeat("x ", required))
		if err := cmd.Args(cmd, args); err != nil {
			t.Errorf("%s: expected %d arg(s) to pass validation, got %v", path, required, err)
		}
	}
	walk(NewRootCmd(io.Discard, io.Discard))

	if checked == 0 {
		t.Fatal("no commands with required positional args found; Use-string convention changed?")
	}
}
