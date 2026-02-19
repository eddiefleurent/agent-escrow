package chain

import (
	"bytes"
	"encoding/json"
	"fmt"

	abidata "github.com/eddiefleurent/agent-escrow/go-server/abi"
	"github.com/ethereum/go-ethereum/accounts/abi"
)

type foundryArtifact struct {
	ABI json.RawMessage `json:"abi"`
}

func parseABI(raw []byte) (abi.ABI, error) {
	var artifact foundryArtifact
	if err := json.Unmarshal(raw, &artifact); err != nil {
		return abi.ABI{}, fmt.Errorf("unmarshal artifact: %w", err)
	}

	parsed, err := abi.JSON(bytes.NewReader(artifact.ABI))
	if err != nil {
		return abi.ABI{}, fmt.Errorf("parse abi: %w", err)
	}
	return parsed, nil
}

var (
	FactoryABI abi.ABI
	EscrowABI  abi.ABI
)

func init() {
	var err error
	FactoryABI, err = parseABI(abidata.FactoryJSON)
	if err != nil {
		panic(fmt.Sprintf("factory abi: %v", err))
	}
	EscrowABI, err = parseABI(abidata.EscrowJSON)
	if err != nil {
		panic(fmt.Sprintf("escrow abi: %v", err))
	}
}
