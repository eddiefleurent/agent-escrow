package main

import (
	"fmt"
	"os"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/cli"
)

func main() {
	cmd := cli.NewRootCmd(os.Stdout, os.Stderr)
	if err := cmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(cli.ExitCode(err))
	}
}
