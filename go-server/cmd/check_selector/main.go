package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

func main() {
	rpcURL := os.Getenv("RPC_URL")
	if rpcURL == "" {
		log.Fatal("RPC_URL environment variable is required")
	}

	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		log.Fatalf("failed to connect to RPC: %v", err)
	}

	code, err := fetchCode(client)
	if err != nil {
		client.Close()
		log.Fatal(err)
	}
	client.Close()

	selectors := map[string]string{
		"c229b1e9": "createEscrow (current ABI w/ milestones)",
		"362c3f42": "createEscrow (no milestones)",
		"b5bf3272": "createEscrow (simple/old)",
		"52ba9d0e": "complexityFloor()",
		"89cb29dd": "nextEscrowId()",
	}
	for _, sig := range []string{
		"createEscrow((address,address,address,address,uint256,uint256,uint64,uint64,uint64,bytes32,uint64,address,address,uint64,(uint256,uint64)[]))",
		"createEscrow((address,address,address,address,uint256,uint256,uint64,uint64,uint64,bytes32,uint64,address,address,uint64))",
		"createEscrow((address,address,address,address,uint256,uint64,uint64,uint64,bytes32,uint64))",
	} {
		sel := hex.EncodeToString(crypto.Keccak256([]byte(sig))[:4])
		selectors[sel] = sig
	}

	fmt.Printf("Code length: %d bytes\n", len(code))
	for sel, name := range selectors {
		selectorBytes, err := hex.DecodeString(sel)
		if err != nil {
			log.Fatalf("invalid selector hex %q: %v", sel, err)
		}
		// EVM uses PUSH4 (0x63) to load 4-byte selectors for dispatch
		pattern := append([]byte{0x63}, selectorBytes...)
		found := bytes.Contains(code, pattern)
		fmt.Printf("  %s (%s): %v\n", sel, name, found)
	}
}

func fetchCode(client *ethclient.Client) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	factory := common.HexToAddress("0x7006930a9d309ca476b5538800da16525ecb191d")
	code, err := client.CodeAt(ctx, factory, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch code at %s: %w", factory.Hex(), err)
	}
	return code, nil
}
