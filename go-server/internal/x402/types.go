package x402

// PaymentPayload represents an EIP-3009 authorization payload for x402 settlement.
type PaymentPayload struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Value       string `json:"value"`
	ValidAfter  string `json:"validAfter"`
	ValidBefore string `json:"validBefore"`
	Nonce       string `json:"nonce"`
	V           uint8  `json:"v"`
	R           string `json:"r"`
	S           string `json:"s"`
	Token       string `json:"token"`
	ChainID     int64  `json:"chainId"`
}

// VerifyRequest is sent to POST /verify on the x402 facilitator.
type VerifyRequest struct {
	Payment PaymentPayload `json:"payment"`
}

// VerifyResponse is returned from the verify endpoint.
type VerifyResponse struct {
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
}

// SettleRequest is sent to POST /settle on the x402 facilitator.
type SettleRequest struct {
	Payment PaymentPayload `json:"payment"`
}

// SettleResponse is returned from the settle endpoint.
type SettleResponse struct {
	TxHash  string `json:"txHash"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}
