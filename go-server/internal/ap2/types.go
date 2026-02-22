package ap2

import "time"

// MandateType identifies the AP2 mandate variant.
type MandateType string

const (
	MandateTypeIntent  MandateType = "intent"
	MandateTypeCart    MandateType = "cart"
	MandateTypePayment MandateType = "payment"
)

// IntentMandate represents a user's signed spending intent with budget constraints.
type IntentMandate struct {
	Signer         string `json:"signer"`
	BudgetAmount   string `json:"budget_amount"`
	BudgetCurrency string `json:"budget_currency"`
	TTLSeconds     int64  `json:"ttl_seconds"`
	Description    string `json:"description,omitempty"`
}

// CartMandate represents exact transaction details signed by user and merchant.
type CartMandate struct {
	Signer     string `json:"signer"`
	Merchant   string `json:"merchant"`
	Amount     string `json:"amount"`
	Currency   string `json:"currency"`
	ItemsHash  string `json:"items_hash"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

// PaymentMandate is a compact credential for payment ecosystem visibility.
type PaymentMandate struct {
	Signer    string `json:"signer"`
	Amount    string `json:"amount"`
	Currency  string `json:"currency"`
	Recipient string `json:"recipient"`
	Nonce     string `json:"nonce"`
}

// EIP3009Authorization contains the EIP-3009 signature fields for gasless funding.
type EIP3009Authorization struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Value       string `json:"value"`
	ValidAfter  string `json:"valid_after"`
	ValidBefore string `json:"valid_before"`
	Nonce       string `json:"nonce"`
	V           uint8  `json:"v"`
	R           string `json:"r"`
	S           string `json:"s"`
}

// MandateEnvelope wraps a mandate with its type, raw payload, and cryptographic signature.
type MandateEnvelope struct {
	Type          MandateType          `json:"type"`
	Payload       map[string]any       `json:"payload"`
	Signature     string               `json:"signature"`
	SignerAddress string               `json:"signer_address"`
	Authorization EIP3009Authorization `json:"authorization"`
}

// EscrowBinding links a mandate to an escrow.
type EscrowBinding struct {
	MandateID     string `json:"mandate_id"`
	MandateHash   string `json:"mandate_hash"`
	EscrowID      int64  `json:"escrow_id"`
	FundingTxHash string `json:"funding_tx_hash,omitempty"`
	Status        string `json:"status"`
}

// Mandate is the database model for ap2_mandates.
type Mandate struct {
	ID             string      `json:"id"`
	MandateType    MandateType `json:"mandate_type"`
	MandateHash    string      `json:"mandate_hash"`
	SignerAddress  string      `json:"signer_address"`
	BudgetAmount   string      `json:"budget_amount,omitempty"`
	BudgetCurrency string      `json:"budget_currency,omitempty"`
	ExpiresAt      *time.Time  `json:"expires_at,omitempty"`
	EscrowID       *int64      `json:"escrow_id,omitempty"`
	FundingTxHash  string      `json:"funding_tx_hash,omitempty"`
	Status         string      `json:"status"`
	RawPayload     string      `json:"raw_payload"`
	CreatedAt      time.Time   `json:"created_at"`
}

// FundViaMandateRequest is the HTTP/MCP request body for funding via AP2 mandate.
type FundViaMandateRequest struct {
	EscrowID        string          `json:"escrow_id"`
	MandateEnvelope MandateEnvelope `json:"mandate_envelope"`
}

// FundViaMandateResponse is returned after successful mandate-based funding.
type FundViaMandateResponse struct {
	TxHash    string `json:"tx_hash"`
	EscrowID  int64  `json:"escrow_id"`
	MandateID string `json:"mandate_id"`
	Status    string `json:"status"`
}

// ValidateMandateRequest is the HTTP request for dry-run validation.
type ValidateMandateRequest struct {
	MandateEnvelope MandateEnvelope `json:"mandate_envelope"`
}

// ValidateMandateResponse is returned from the validate endpoint.
type ValidateMandateResponse struct {
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
}
