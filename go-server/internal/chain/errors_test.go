package chain

import (
	"errors"
	"strings"
	"testing"
)

func TestHumanizeErrorNil(t *testing.T) {
	t.Parallel()

	if got := HumanizeError(nil); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestHumanizeErrorNonceHint(t *testing.T) {
	t.Parallel()

	orig := errors.New("transaction failed: nonce too low")
	got := HumanizeError(orig)
	if got == nil {
		t.Fatal("expected non-nil error")
	}
	if !strings.Contains(got.Error(), "nonce cache is stale") {
		t.Fatalf("expected nonce hint, got %q", got.Error())
	}
	if !errors.Is(got, orig) {
		t.Fatal("expected wrapped original error")
	}
}

func TestHumanizeErrorKnownSelector(t *testing.T) {
	t.Parallel()

	orig := errors.New("execution reverted: 0x82b42900")
	got := HumanizeError(orig)
	if got == nil {
		t.Fatal("expected non-nil error")
	}
	if got.Error() != knownSelectors["82b42900"] {
		t.Fatalf("expected selector description %q, got %q", knownSelectors["82b42900"], got.Error())
	}
	if !errors.Is(got, orig) {
		t.Fatal("expected wrapped original error")
	}
}

func TestHumanizeErrorUnknownSelectorReturnsOriginal(t *testing.T) {
	t.Parallel()

	orig := errors.New("execution reverted: 0xdeadbeef")
	got := HumanizeError(orig)
	if !errors.Is(got, orig) {
		t.Fatal("expected original error to be returned for unknown selector")
	}
	if got.Error() != orig.Error() {
		t.Fatalf("expected original message %q, got %q", orig.Error(), got.Error())
	}
}
