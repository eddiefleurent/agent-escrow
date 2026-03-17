package cli

import "github.com/spf13/cobra"

func testOptions() *Options {
	return &Options{
		ServerURL: "http://localhost:8080",
		Output:    outputJSON,
	}
}

func hasSubcommand(cmd *cobra.Command, name string) bool {
	for _, sub := range cmd.Commands() {
		if sub.Name() == name {
			return true
		}
	}
	return false
}
