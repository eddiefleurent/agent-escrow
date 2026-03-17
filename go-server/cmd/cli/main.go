package main

import (
	"fmt"
	"io"
	"os"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/cli"
)

func main() {
	if err := run(os.Stdout, os.Stderr, os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(cli.ExitCode(err))
	}
}

func run(stdout io.Writer, stderr io.Writer, args []string) error {
	cmd := cli.NewRootCmd(stdout, stderr)
	cmd.SetArgs(args)
	return cmd.Execute()
}
