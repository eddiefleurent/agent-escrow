package chain

import (
	"encoding/hex"
	"strings"
)

// knownSelectors maps 4-byte custom error selectors to human-readable descriptions.
// These are extracted from the TaskEscrow and TaskEscrowFactory contracts.
var knownSelectors = map[string]string{
	"197b7dc9": "RolesNotDistinct: buyer, worker, verifier, and arbitrator must all be different addresses",
	"82b42900": "Unauthorized: caller is not authorized for this action",
	"a1b2e8d7": "InvalidState: the escrow is not in the required state for this operation",
	"e4b18d2c": "DeadlinePassed: the submission deadline has passed",
	"d4e4b9a0": "DeadlineNotPassed: the deadline has not yet passed",
	"f4d678b8": "InsufficientFunds: insufficient funds sent with the transaction",
	"8baa579f": "InvalidAmount: the amount is invalid (zero or mismatched)",
	"1f2a2005": "AlreadyFunded: the escrow is already funded",
	"b5a09db9": "NotFunded: the escrow has not been funded yet",
	"e6c4247b": "InvalidAddress: an invalid address was provided",
}

// HumanizeError inspects an error message for known Solidity custom error selectors
// and replaces opaque hex selectors with human-readable descriptions.
func HumanizeError(err error) error {
	if err == nil {
		return nil
	}

	msg := err.Error()

	// Look for "execution reverted" pattern which often contains a selector.
	if !strings.Contains(msg, "execution reverted") {
		// Check for "nonce too low" for better messaging.
		if strings.Contains(msg, "nonce too low") {
			return &humanError{
				original: err,
				message:  msg + " (hint: the server's nonce cache is stale — restart the server or wait for the pending transaction to confirm)",
			}
		}
		return err
	}

	// Try to find a 4-byte selector in the error message.
	for selector, description := range knownSelectors {
		if strings.Contains(msg, selector) {
			return &humanError{
				original: err,
				message:  description,
			}
		}
	}

	// Try to extract and decode any 4-byte selector from hex data in the message.
	if idx := strings.Index(msg, "0x"); idx >= 0 {
		hexPart := msg[idx+2:]
		// Take first 8 hex chars (4 bytes) if available.
		if len(hexPart) >= 8 {
			selectorHex := hexPart[:8]
			if _, decErr := hex.DecodeString(selectorHex); decErr == nil {
				if desc, ok := knownSelectors[selectorHex]; ok {
					return &humanError{
						original: err,
						message:  desc,
					}
				}
			}
		}
	}

	return err
}

type humanError struct {
	original error
	message  string
}

func (e *humanError) Error() string {
	return e.message
}

func (e *humanError) Unwrap() error {
	return e.original
}
