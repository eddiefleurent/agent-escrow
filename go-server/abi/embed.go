package abi

import _ "embed"

//go:embed TaskEscrowFactory.json
var FactoryJSON []byte

//go:embed TaskEscrow.json
var EscrowJSON []byte
