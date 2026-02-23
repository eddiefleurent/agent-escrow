package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

func main() {
	client, _ := ethclient.Dial(os.Getenv("RPC_URL"))
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	factory := common.HexToAddress("0x7006930a9d309ca476b5538800da16525ecb191d")
	code, _ := client.CodeAt(ctx, factory, nil)
	codeHex := hex.EncodeToString(code)

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
		found := strings.Contains(codeHex, sel)
		fmt.Printf("  %s (%s): %v\n", sel, name, found)
	}
}
