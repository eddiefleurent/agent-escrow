package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	RPCURL string
	// #nosec G117 -- Field name matches env var PRIVATE_KEY; value is loaded at runtime and validated in Validate().
	PrivateKey     string
	ChainID        int64
	FactoryAddress string
	DatabaseURL    string
	Port           int
	MCPTransport   string
	CORSOrigins    []string      // Allowed CORS origins; empty means allow all ("*")
	RequestTimeout time.Duration // Timeout for read-only requests (default 10s)
	TxTimeout      time.Duration // Timeout for chain transaction requests (default 90s)
	LogChunkSize   uint64        // Max block range per eth_getLogs request (default 2000)
	StartBlock     uint64        // Block to start indexing from (0 = use defaultLookback)
	// RebidCooldownSeconds enforces minimum delay between parent-linked RFQ creations.
	// 0 disables the cooldown gate.
	RebidCooldownSeconds int64
	// ReputationDampingFactor controls exponential decay for damped reputation
	// views derived from immutable reputation_events history. Must be > 0 and <= 1.
	ReputationDampingFactor float64

	// ComplexityFloor is the minimum escrow amount (in wei or smallest token unit)
	// to justify delegation overhead. Used for early rejection before sending
	// a transaction that would revert on-chain. Empty or "0" means disabled.
	ComplexityFloor string

	// CDP Webhook: if set, the server registers POST /webhooks/cdp to receive
	// real-time factory events (EscrowCreated, OutcomeRecorded) via push.
	// The secret is used for HMAC-SHA256 signature verification.
	// The polling indexer still runs for escrow-level events since each
	// TaskEscrow is a separate contract that can't be pre-subscribed.
	CDPWebhookSecret string

	// A2A settlement adapter configuration (paper §6: A2A Task object extension).
	A2AEnabled   bool   // Enable A2A adapter routes (default true)
	A2AAgentName string // Agent card display name
	A2AAgentURL  string // Agent card URL (default derived from PORT)

	// UCP fulfillment adapter configuration (roadmap item 22).
	UCPEnabled      bool   // Enable UCP REST/MCP/CLI surfaces (default false)
	UCPBaseURL      string // Public base URL used in /.well-known/ucp metadata
	UCPProviderName string // Human-readable provider name for UCP profile

	// x402 / AP2 mandate-to-escrow bridge (paper §6: AP2 stake-on-bid + conditional settlement).
	X402Enabled        bool   // Enable x402 gasless funding path (default false)
	X402FacilitatorURL string // CDP x402 facilitator endpoint

	// Real-time event subscriptions (paper §4.5: configurable granularity L0-L3).
	EventsEnabled           bool          // Enable SSE/WebSocket event streaming (default true)
	EventsBufferSize        int           // Per-subscriber channel buffer size (default 64)
	EventsHeartbeatInterval time.Duration // Heartbeat interval for L0 liveness (default 30s)

	// Emergency response protocol (paper §4.9: rapid incident response).
	EmergencyEnabled bool   // Enable emergency freeze/resolve endpoints (default true)
	OwnerAddress     string // Factory owner address for emergency override authorization (OWNER_ADDRESS)
}

// WebhookMode reports whether CDP webhook mode is enabled (secret is configured).
func (c *Config) WebhookMode() bool {
	return strings.TrimSpace(c.CDPWebhookSecret) != ""
}

func Load() (*Config, error) {
	port := 8080
	if p := os.Getenv("PORT"); p != "" {
		var err error
		port, err = strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("invalid PORT: %w", err)
		}
	}

	chainID := int64(84532) // Base Sepolia default
	if cid := os.Getenv("CHAIN_ID"); cid != "" {
		var err error
		chainID, err = strconv.ParseInt(cid, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid CHAIN_ID: %w", err)
		}
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "delegation.db"
	}

	var corsOrigins []string
	if raw := os.Getenv("CORS_ORIGINS"); raw != "" {
		for o := range strings.SplitSeq(raw, ",") {
			if trimmed := strings.TrimSpace(o); trimmed != "" {
				corsOrigins = append(corsOrigins, trimmed)
			}
		}
	}

	requestTimeout := 10 * time.Second
	if raw := os.Getenv("REQUEST_TIMEOUT"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid REQUEST_TIMEOUT: %w", err)
		}
		requestTimeout = d
	}

	txTimeout := 90 * time.Second
	if raw := os.Getenv("TX_TIMEOUT"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid TX_TIMEOUT: %w", err)
		}
		txTimeout = d
	}

	logChunkSize := uint64(2000)
	if raw := os.Getenv("LOG_CHUNK_SIZE"); raw != "" {
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || v == 0 {
			return nil, errors.New("invalid LOG_CHUNK_SIZE: must be a positive integer")
		}
		logChunkSize = v
	}

	var startBlock uint64
	if raw := os.Getenv("START_BLOCK"); raw != "" {
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid START_BLOCK: %w", err)
		}
		startBlock = v
	}

	rebidCooldownSeconds := int64(0)
	if raw := os.Getenv("REBID_COOLDOWN_SECONDS"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			return nil, errors.New("invalid REBID_COOLDOWN_SECONDS: must be a non-negative integer")
		}
		rebidCooldownSeconds = v
	}

	reputationDampingFactor := 0.9
	if raw := os.Getenv("REPUTATION_DAMPING_FACTOR"); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid REPUTATION_DAMPING_FACTOR: %w", err)
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, errors.New("invalid REPUTATION_DAMPING_FACTOR: must be finite")
		}
		if v <= 0 || v > 1 {
			return nil, errors.New("invalid REPUTATION_DAMPING_FACTOR: must be > 0 and <= 1")
		}
		reputationDampingFactor = v
	}

	complexityFloor := os.Getenv("COMPLEXITY_FLOOR")
	if complexityFloor != "" {
		bi := new(big.Int)
		if _, ok := bi.SetString(complexityFloor, 10); !ok {
			return nil, errors.New("invalid COMPLEXITY_FLOOR: must be a non-negative integer")
		}
		if bi.Sign() < 0 {
			return nil, errors.New("invalid COMPLEXITY_FLOOR: must be a non-negative integer")
		}
	}

	a2aEnabled := true
	if raw := os.Getenv("A2A_ENABLED"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid A2A_ENABLED: %w", err)
		}
		a2aEnabled = v
	}

	a2aAgentName := os.Getenv("A2A_AGENT_NAME")
	if a2aAgentName == "" {
		a2aAgentName = "Escrow Settlement Agent"
	}

	a2aAgentURL := os.Getenv("A2A_AGENT_URL")
	if a2aAgentURL == "" {
		a2aAgentURL = fmt.Sprintf("http://localhost:%d", port)
	}

	ucpEnabled := false
	if raw := os.Getenv("UCP_ENABLED"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid UCP_ENABLED: %w", err)
		}
		ucpEnabled = v
	}

	ucpBaseURL := strings.TrimSpace(os.Getenv("UCP_BASE_URL"))
	if ucpBaseURL == "" {
		ucpBaseURL = fmt.Sprintf("http://localhost:%d", port)
	}

	ucpProviderName := strings.TrimSpace(os.Getenv("UCP_PROVIDER_NAME"))
	if ucpProviderName == "" {
		ucpProviderName = "Agent Escrow UCP Provider"
	}

	if ucpEnabled {
		if ucpProviderName == "" {
			return nil, errors.New("UCP_PROVIDER_NAME must be set when UCP is enabled")
		}
		parsed, err := parseURL(ucpBaseURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("UCP_BASE_URL must be a valid URL with scheme and host, got %q", ucpBaseURL)
		}
	}

	x402Enabled := false
	if raw := os.Getenv("X402_ENABLED"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid X402_ENABLED: %w", err)
		}
		x402Enabled = v
	}

	x402FacilitatorURL := os.Getenv("X402_FACILITATOR_URL")
	if x402FacilitatorURL == "" {
		x402FacilitatorURL = "https://api.developer.coinbase.com/v2/x402"
	}

	eventsEnabled := true
	if raw := os.Getenv("EVENTS_ENABLED"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid EVENTS_ENABLED: %w", err)
		}
		eventsEnabled = v
	}

	eventsBufferSize := 64
	if raw := os.Getenv("EVENTS_BUFFER_SIZE"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return nil, errors.New("invalid EVENTS_BUFFER_SIZE: must be a positive integer")
		}
		eventsBufferSize = v
	}

	emergencyEnabled := true
	if raw := os.Getenv("EMERGENCY_ENABLED"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid EMERGENCY_ENABLED: %w", err)
		}
		emergencyEnabled = v
	}

	eventsHeartbeatInterval := 30 * time.Second
	if raw := os.Getenv("EVENTS_HEARTBEAT_INTERVAL"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid EVENTS_HEARTBEAT_INTERVAL: %w", err)
		}
		if d <= 0 {
			return nil, errors.New("invalid EVENTS_HEARTBEAT_INTERVAL: must be positive")
		}
		eventsHeartbeatInterval = d
	}

	cfg := &Config{
		RPCURL:                  os.Getenv("RPC_URL"),
		PrivateKey:              os.Getenv("PRIVATE_KEY"),
		ChainID:                 chainID,
		FactoryAddress:          os.Getenv("FACTORY_ADDRESS"),
		DatabaseURL:             dbURL,
		Port:                    port,
		MCPTransport:            os.Getenv("MCP_TRANSPORT"),
		CORSOrigins:             corsOrigins,
		RequestTimeout:          requestTimeout,
		TxTimeout:               txTimeout,
		LogChunkSize:            logChunkSize,
		StartBlock:              startBlock,
		RebidCooldownSeconds:    rebidCooldownSeconds,
		ReputationDampingFactor: reputationDampingFactor,
		ComplexityFloor:         complexityFloor,
		CDPWebhookSecret:        os.Getenv("CDP_WEBHOOK_SECRET"),
		A2AEnabled:              a2aEnabled,
		A2AAgentName:            a2aAgentName,
		A2AAgentURL:             a2aAgentURL,
		UCPEnabled:              ucpEnabled,
		UCPBaseURL:              ucpBaseURL,
		UCPProviderName:         ucpProviderName,
		X402Enabled:             x402Enabled,
		X402FacilitatorURL:      x402FacilitatorURL,
		EventsEnabled:           eventsEnabled,
		EventsBufferSize:        eventsBufferSize,
		EventsHeartbeatInterval: eventsHeartbeatInterval,
		EmergencyEnabled:        emergencyEnabled,
		OwnerAddress:            strings.ToLower(strings.TrimSpace(os.Getenv("OWNER_ADDRESS"))),
	}

	result := cfg.Validate()
	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("config validation failed:\n  %s", strings.Join(result.Errors, "\n  "))
	}

	return cfg, nil
}

// ValidationResult holds validation errors and warnings separately.
// Errors prevent startup; warnings are informational (e.g. offline mode).
type ValidationResult struct {
	Errors   []string
	Warnings []string
}

// Online reports whether the config has a non-empty RPC URL,
// indicating the server should connect to a live chain.
func (c *Config) Online() bool {
	return c.RPCURL != ""
}

// Validate checks config field constraints and returns errors and warnings.
// An empty RPC_URL is valid (offline mode) but produces a warning.
// When RPC_URL is set, PRIVATE_KEY and FACTORY_ADDRESS are required.
func (c *Config) Validate() ValidationResult {
	var r ValidationResult

	if c.RPCURL == "" {
		r.Warnings = append(r.Warnings, "RPC_URL is empty: running in offline mode (chain operations will fail)")
	} else {
		if c.PrivateKey == "" {
			r.Errors = append(r.Errors, "PRIVATE_KEY is required when RPC_URL is set")
		}
		if c.FactoryAddress == "" {
			r.Errors = append(r.Errors, "FACTORY_ADDRESS is required when RPC_URL is set")
		}
	}

	if c.PrivateKey != "" {
		if err := validateHexKey(c.PrivateKey); err != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("PRIVATE_KEY is invalid: %v", err))
		}
	}

	if c.FactoryAddress != "" {
		if err := validateEthAddress(c.FactoryAddress); err != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("FACTORY_ADDRESS is invalid: %v", err))
		}
	}

	if c.Port < 1 || c.Port > 65535 {
		r.Errors = append(r.Errors, fmt.Sprintf("PORT must be between 1 and 65535, got %d", c.Port))
	}

	if c.RequestTimeout <= 0 {
		r.Errors = append(r.Errors, "REQUEST_TIMEOUT must be positive")
	}

	if c.TxTimeout <= 0 {
		r.Errors = append(r.Errors, "TX_TIMEOUT must be positive")
	}

	if c.RebidCooldownSeconds < 0 {
		r.Errors = append(r.Errors, "REBID_COOLDOWN_SECONDS must be >= 0")
	}

	if math.IsNaN(c.ReputationDampingFactor) || math.IsInf(c.ReputationDampingFactor, 0) || c.ReputationDampingFactor <= 0 || c.ReputationDampingFactor > 1 {
		r.Errors = append(r.Errors, "REPUTATION_DAMPING_FACTOR must be > 0 and <= 1")
	}

	if c.EventsEnabled {
		if c.EventsBufferSize <= 0 {
			r.Errors = append(r.Errors, "EVENTS_BUFFER_SIZE must be positive when events are enabled")
		}
		if c.EventsHeartbeatInterval <= 0 {
			r.Errors = append(r.Errors, "EVENTS_HEARTBEAT_INTERVAL must be positive when events are enabled")
		}
	}

	if c.EmergencyEnabled && strings.TrimSpace(c.OwnerAddress) == "" {
		r.Warnings = append(r.Warnings, "EMERGENCY_ENABLED is true but OWNER_ADDRESS is not set: emergency override authorization will always fail")
	}

	return r
}

// validateHexKey checks that s is a valid 32-byte hex-encoded private key,
// with an optional 0x prefix.
func validateHexKey(s string) error {
	s = strings.TrimPrefix(s, "0x")
	b, err := hex.DecodeString(s)
	if err != nil {
		return fmt.Errorf("not valid hex: %w", err)
	}
	if len(b) != 32 {
		return fmt.Errorf("expected 32 bytes, got %d", len(b))
	}
	return nil
}

// validateEthAddress checks that s looks like a 20-byte hex Ethereum address
// with a 0x prefix.
func validateEthAddress(s string) error {
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return errors.New("must start with 0x prefix")
	}
	b, err := hex.DecodeString(s[2:])
	if err != nil {
		return fmt.Errorf("not valid hex: %w", err)
	}
	if len(b) != 20 {
		return fmt.Errorf("expected 20 bytes (40 hex chars), got %d bytes", len(b))
	}
	return nil
}

func parseURL(raw string) (*url.URL, error) {
	return url.Parse(raw)
}
