package main

import (
	"bytes"
	"io"
	"testing"
)

func TestRunHelpSucceeds(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	if err := run(io.Discard, &stderr, []string{"--help"}); err != nil {
		t.Fatalf("expected help to succeed, got %v", err)
	}
}

func TestRunInvalidCommandReturnsError(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	err := run(io.Discard, &stderr, []string{"definitely-not-a-command"})
	if err == nil {
		t.Fatal("expected error for invalid command")
	}
}
