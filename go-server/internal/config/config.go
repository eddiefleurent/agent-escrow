package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
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
	CORSOrigins    []string      // Allowed HTTP CORS origins; empty omits the allow-origin header
	RequestTimeout time.Duration // Timeout for read-only requests (default 10s)
	TxTimeout      time.Duration // Timeout for chain transaction requests (default 90s)
	// MaxBodyBytes caps each HTTP request body. Zero selects DefaultMaxBodyBytes
	// at the edge. A negative value fails validation.
	MaxBodyBytes int64
	// RateLimitRPS is the sustained per-client-IP rate for POST, PUT, PATCH,
	// and DELETE. Zero disables the limiter.
	RateLimitRPS float64
	// RateLimitBurst is the token-bucket size. Zero disables the limiter.
	RateLimitBurst int
	// TrustedProxies lists CIDRs allowed to supply X-Forwarded-For. Empty
	// means the rate limiter keys on RemoteAddr and ignores that header.
	TrustedProxies []*net.IPNet
	LogChunkSize   uint64 // Max block range per eth_getLogs request (default 2000)
	StartBlock     uint64 // Block to start indexing from (0 = use defaultLookback)
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
	EmergencyEnabled    bool // Enable HTTP emergency endpoints (default false)
	MCPEmergencyEnabled bool // Enable emergency and admin audit tools over local MCP stdio (default false)
	// #nosec G117 -- Loaded from ADMIN_TOKEN and used only for bearer authentication.
	AdminToken   string
	OwnerAddress string // Factory owner address for emergency override authorization (OWNER_ADDRESS)
}

const (
	// DefaultMaxBodyBytes is the request body cap when MAX_BODY_BYTES is unset (1 MiB).
	DefaultMaxBodyBytes int64 = 1 << 20
	// DefaultRateLimitRPS is the per-client rate for mutating requests.
	DefaultRateLimitRPS float64 = 10
	// DefaultRateLimitBurst is the per-client token bucket size.
	DefaultRateLimitBurst = 20
)

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

	emergencyEnabled := false
	if raw := os.Getenv("EMERGENCY_ENABLED"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid EMERGENCY_ENABLED: %w", err)
		}
		emergencyEnabled = v
	}

	mcpEmergencyEnabled := false
	if raw := os.Getenv("MCP_EMERGENCY_ENABLED"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid MCP_EMERGENCY_ENABLED: %w", err)
		}
		mcpEmergencyEnabled = v
	}

	maxBodyBytes, rateLimitRPS, rateLimitBurst, trustedProxies, err := loadEdgeLimits()
	if err != nil {
		return nil, err
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
		MaxBodyBytes:            maxBodyBytes,
		RateLimitRPS:            rateLimitRPS,
		RateLimitBurst:          rateLimitBurst,
		TrustedProxies:          trustedProxies,
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
		MCPEmergencyEnabled:     mcpEmergencyEnabled,
		AdminToken:              strings.TrimSpace(os.Getenv("ADMIN_TOKEN")),
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

	if c.MaxBodyBytes < 0 {
		r.Errors = append(r.Errors, "MAX_BODY_BYTES must be positive")
	}

	if math.IsNaN(c.RateLimitRPS) || math.IsInf(c.RateLimitRPS, 0) || c.RateLimitRPS < 0 {
		r.Errors = append(r.Errors, "RATE_LIMIT_RPS must be a non-negative number")
	}

	if c.RateLimitBurst < 0 {
		r.Errors = append(r.Errors, "RATE_LIMIT_BURST must be a non-negative integer")
	}

	for _, network := range c.TrustedProxies {
		if network == nil {
			r.Errors = append(r.Errors, "TRUSTED_PROXIES contains an invalid CIDR")
			break
		}
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

	// ADMIN_TOKEN also guards always-registered admin routes (e.g. DCT audit),
	// so the minimum applies whenever it is set, not only with emergency on.
	if (c.EmergencyEnabled || c.AdminToken != "") && utf8.RuneCountInString(c.AdminToken) < 32 {
		r.Errors = append(r.Errors, "ADMIN_TOKEN must be at least 32 characters (required when EMERGENCY_ENABLED is true)")
	}

	if (c.EmergencyEnabled || c.MCPEmergencyEnabled) && strings.TrimSpace(c.OwnerAddress) == "" {
		r.Warnings = append(r.Warnings, "OWNER_ADDRESS is not set: DCT emergency override authorization will always fail")
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

// loadEdgeLimits reads the HTTP body cap, rate limit, and trusted-proxy CIDRs.
func loadEdgeLimits() (int64, float64, int, []*net.IPNet, error) {
	maxBody := DefaultMaxBodyBytes
	if raw := strings.TrimSpace(os.Getenv("MAX_BODY_BYTES")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v <= 0 {
			return 0, 0, 0, nil, errors.New("invalid MAX_BODY_BYTES: must be a positive integer")
		}
		maxBody = v
	}

	rps := DefaultRateLimitRPS
	if raw := strings.TrimSpace(os.Getenv("RATE_LIMIT_RPS")); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return 0, 0, 0, nil, errors.New("invalid RATE_LIMIT_RPS: must be a non-negative number")
		}
		rps = v
	}

	burst := DefaultRateLimitBurst
	if raw := strings.TrimSpace(os.Getenv("RATE_LIMIT_BURST")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			return 0, 0, 0, nil, errors.New("invalid RATE_LIMIT_BURST: must be a non-negative integer")
		}
		burst = v
	}

	trusted, err := parseTrustedProxies(os.Getenv("TRUSTED_PROXIES"))
	if err != nil {
		return 0, 0, 0, nil, err
	}
	return maxBody, rps, burst, trusted, nil
}

// parseTrustedProxies splits a comma-separated CIDR list. An empty value
// means no proxy is trusted.
func parseTrustedProxies(raw string) ([]*net.IPNet, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var nets []*net.IPNet
	for part := range strings.SplitSeq(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		_, network, err := net.ParseCIDR(part)
		if err != nil {
			return nil, fmt.Errorf("invalid TRUSTED_PROXIES entry %q: must be a CIDR", part)
		}
		nets = append(nets, network)
	}
	return nets, nil
}
