package chain

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// domainSeparatorSelector is the calldata for DOMAIN_SEPARATOR().
// EIP-3009 tokens (including USDC) expose the separator they use for
// receiveWithAuthorization; callers must not reconstruct it.
var domainSeparatorSelector = crypto.Keccak256([]byte("DOMAIN_SEPARATOR()"))[:4]

// DomainSeparator reads DOMAIN_SEPARATOR() from an EIP-712 token contract.
func (c *Client) DomainSeparator(ctx context.Context, token common.Address) (common.Hash, error) {
	out, err := c.CallContract(ctx, token, domainSeparatorSelector)
	if err != nil {
		return common.Hash{}, fmt.Errorf("DOMAIN_SEPARATOR call: %w", err)
	}
	if len(out) < 32 {
		return common.Hash{}, fmt.Errorf("DOMAIN_SEPARATOR returned %d bytes", len(out))
	}
	return common.BytesToHash(out[:32]), nil
}
