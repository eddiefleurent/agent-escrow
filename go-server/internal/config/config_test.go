package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// validPrivateKey is a 32-byte hex key for testing (not a real key).
const validPrivateKey = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

// validFactoryAddress is a well-formed 20-byte hex address.
const validFactoryAddress = "0x5FbDB2315678afecb367f032d93F642f64180aa3"

func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"RPC_URL", "PRIVATE_KEY", "FACTORY_ADDRESS",
		"CHAIN_ID", "DATABASE_URL", "PORT",
		"MCP_TRANSPORT", "CORS_ORIGINS",
		"REQUEST_TIMEOUT", "TX_TIMEOUT",
	} {
		os.Unsetenv(key)
	}
}

func TestLoad_OfflineMode(t *testing.T) {
	clearEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error for offline mode, got: %v", err)
	}
	if cfg.RPCURL != "" {
		t.Errorf("expected empty RPC_URL, got %q", cfg.RPCURL)
	}
	if cfg.Online() {
		t.Error("expected Online() == false in offline mode")
	}
}

func TestLoad_OnlineMode_AllRequired(t *testing.T) {
	clearEnv(t)
	t.Setenv("RPC_URL", "https://sepolia.base.org")
	t.Setenv("PRIVATE_KEY", validPrivateKey)
	t.Setenv("FACTORY_ADDRESS", validFactoryAddress)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !cfg.Online() {
		t.Error("expected Online() == true")
	}
}

func TestLoad_OnlineMode_MissingPrivateKey(t *testing.T) {
	clearEnv(t)
	t.Setenv("RPC_URL", "https://sepolia.base.org")
	t.Setenv("FACTORY_ADDRESS", validFactoryAddress)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when PRIVATE_KEY is missing with RPC_URL set")
	}
	if !strings.Contains(err.Error(), "PRIVATE_KEY is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoad_OnlineMode_MissingFactoryAddress(t *testing.T) {
	clearEnv(t)
	t.Setenv("RPC_URL", "https://sepolia.base.org")
	t.Setenv("PRIVATE_KEY", validPrivateKey)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when FACTORY_ADDRESS is missing with RPC_URL set")
	}
	if !strings.Contains(err.Error(), "FACTORY_ADDRESS is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoad_OnlineMode_MissingBothKeys(t *testing.T) {
	clearEnv(t)
	t.Setenv("RPC_URL", "https://sepolia.base.org")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when both PRIVATE_KEY and FACTORY_ADDRESS are missing")
	}
	if !strings.Contains(err.Error(), "PRIVATE_KEY is required") {
		t.Errorf("expected PRIVATE_KEY error in: %v", err)
	}
	if !strings.Contains(err.Error(), "FACTORY_ADDRESS is required") {
		t.Errorf("expected FACTORY_ADDRESS error in: %v", err)
	}
}

func TestLoad_InvalidPrivateKey_BadHex(t *testing.T) {
	clearEnv(t)
	t.Setenv("RPC_URL", "https://sepolia.base.org")
	t.Setenv("PRIVATE_KEY", "not-hex-at-all")
	t.Setenv("FACTORY_ADDRESS", validFactoryAddress)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid private key")
	}
	if !strings.Contains(err.Error(), "PRIVATE_KEY is invalid") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoad_InvalidPrivateKey_WrongLength(t *testing.T) {
	clearEnv(t)
	t.Setenv("RPC_URL", "https://sepolia.base.org")
	t.Setenv("PRIVATE_KEY", "0xdeadbeef")
	t.Setenv("FACTORY_ADDRESS", validFactoryAddress)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for wrong-length private key")
	}
	if !strings.Contains(err.Error(), "expected 32 bytes") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoad_InvalidFactoryAddress_NoPrefix(t *testing.T) {
	clearEnv(t)
	t.Setenv("RPC_URL", "https://sepolia.base.org")
	t.Setenv("PRIVATE_KEY", validPrivateKey)
	t.Setenv("FACTORY_ADDRESS", "5FbDB2315678afecb367f032d93F642f64180aa3")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for factory address without 0x prefix")
	}
	if !strings.Contains(err.Error(), "must start with 0x") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoad_InvalidFactoryAddress_WrongLength(t *testing.T) {
	clearEnv(t)
	t.Setenv("RPC_URL", "https://sepolia.base.org")
	t.Setenv("PRIVATE_KEY", validPrivateKey)
	t.Setenv("FACTORY_ADDRESS", "0xdead")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for short factory address")
	}
	if !strings.Contains(err.Error(), "expected 20 bytes") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoad_InvalidFactoryAddress_BadHex(t *testing.T) {
	clearEnv(t)
	t.Setenv("RPC_URL", "https://sepolia.base.org")
	t.Setenv("PRIVATE_KEY", validPrivateKey)
	t.Setenv("FACTORY_ADDRESS", "0xZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for non-hex factory address")
	}
	if !strings.Contains(err.Error(), "FACTORY_ADDRESS is invalid") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoad_Defaults(t *testing.T) {
	clearEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ChainID != 84532 {
		t.Errorf("expected default chain ID 84532, got %d", cfg.ChainID)
	}
	if cfg.Port != 8080 {
		t.Errorf("expected default port 8080, got %d", cfg.Port)
	}
	if cfg.DatabaseURL != "delegation.db" {
		t.Errorf("expected default database URL, got %q", cfg.DatabaseURL)
	}
	if cfg.RequestTimeout != 10*time.Second {
		t.Errorf("expected 10s request timeout, got %v", cfg.RequestTimeout)
	}
	if cfg.TxTimeout != 90*time.Second {
		t.Errorf("expected 90s tx timeout, got %v", cfg.TxTimeout)
	}
}

func TestLoad_CustomPort(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORT", "3000")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != 3000 {
		t.Errorf("expected port 3000, got %d", cfg.Port)
	}
}

func TestLoad_InvalidPort(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORT", "not-a-number")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid port")
	}
	if !strings.Contains(err.Error(), "invalid PORT") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoad_CORSOrigins(t *testing.T) {
	clearEnv(t)
	t.Setenv("CORS_ORIGINS", "https://example.com, https://other.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.CORSOrigins) != 2 {
		t.Fatalf("expected 2 CORS origins, got %d", len(cfg.CORSOrigins))
	}
	if cfg.CORSOrigins[0] != "https://example.com" {
		t.Errorf("expected first origin https://example.com, got %q", cfg.CORSOrigins[0])
	}
}

func TestValidate_OfflineWarning(t *testing.T) {
	cfg := &Config{
		Port:           8080,
		RequestTimeout: 10 * time.Second,
		TxTimeout:      90 * time.Second,
	}
	r := cfg.Validate()
	if len(r.Errors) != 0 {
		t.Errorf("expected no errors, got: %v", r.Errors)
	}
	if len(r.Warnings) == 0 {
		t.Error("expected offline mode warning")
	}
	found := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "offline mode") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning about offline mode, got: %v", r.Warnings)
	}
}

func TestValidate_OnlineNoWarnings(t *testing.T) {
	cfg := &Config{
		RPCURL:         "https://sepolia.base.org",
		PrivateKey:     validPrivateKey,
		FactoryAddress: validFactoryAddress,
		Port:           8080,
		RequestTimeout: 10 * time.Second,
		TxTimeout:      90 * time.Second,
	}
	r := cfg.Validate()
	if len(r.Errors) != 0 {
		t.Errorf("expected no errors, got: %v", r.Errors)
	}
	if len(r.Warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", r.Warnings)
	}
}

func TestValidate_InvalidPort_Zero(t *testing.T) {
	cfg := &Config{
		Port:           0,
		RequestTimeout: 10 * time.Second,
		TxTimeout:      90 * time.Second,
	}
	r := cfg.Validate()
	found := false
	for _, e := range r.Errors {
		if strings.Contains(e, "PORT must be between") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected port validation error, got: %v", r.Errors)
	}
}

func TestValidate_InvalidPort_TooHigh(t *testing.T) {
	cfg := &Config{
		Port:           70000,
		RequestTimeout: 10 * time.Second,
		TxTimeout:      90 * time.Second,
	}
	r := cfg.Validate()
	found := false
	for _, e := range r.Errors {
		if strings.Contains(e, "PORT must be between") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected port validation error, got: %v", r.Errors)
	}
}

func TestValidate_NegativeTimeout(t *testing.T) {
	cfg := &Config{
		Port:           8080,
		RequestTimeout: -1 * time.Second,
		TxTimeout:      90 * time.Second,
	}
	r := cfg.Validate()
	found := false
	for _, e := range r.Errors {
		if strings.Contains(e, "REQUEST_TIMEOUT must be positive") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected timeout validation error, got: %v", r.Errors)
	}
}

func TestValidate_NegativeTxTimeout(t *testing.T) {
	cfg := &Config{
		Port:           8080,
		RequestTimeout: 10 * time.Second,
		TxTimeout:      -1 * time.Second,
	}
	r := cfg.Validate()
	found := false
	for _, e := range r.Errors {
		if strings.Contains(e, "TX_TIMEOUT must be positive") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected timeout validation error, got: %v", r.Errors)
	}
}

func TestValidate_PrivateKeyWithout0xPrefix(t *testing.T) {
	cfg := &Config{
		RPCURL:         "https://sepolia.base.org",
		PrivateKey:     "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80",
		FactoryAddress: validFactoryAddress,
		Port:           8080,
		RequestTimeout: 10 * time.Second,
		TxTimeout:      90 * time.Second,
	}
	r := cfg.Validate()
	if len(r.Errors) != 0 {
		t.Errorf("private key without 0x prefix should be valid, got errors: %v", r.Errors)
	}
}

func TestValidateHexKey(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr bool
		errMsg  string
	}{
		{"valid with 0x", validPrivateKey, false, ""},
		{"valid without 0x", "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80", false, ""},
		{"too short", "0xdeadbeef", true, "expected 32 bytes"},
		{"bad hex", "0xnothex", true, "not valid hex"},
		{"empty", "", true, "expected 32 bytes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateHexKey(tt.key)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
			if tt.wantErr && err != nil && tt.errMsg != "" {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got: %v", tt.errMsg, err)
				}
			}
		})
	}
}

func TestValidateEthAddress(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		wantErr bool
		errMsg  string
	}{
		{"valid", validFactoryAddress, false, ""},
		{"valid lowercase", "0x5fbdb2315678afecb367f032d93f642f64180aa3", false, ""},
		{"no prefix", "5FbDB2315678afecb367f032d93F642f64180aa3", true, "must start with 0x"},
		{"too short", "0xdead", true, "expected 20 bytes"},
		{"too long", "0x5FbDB2315678afecb367f032d93F642f64180aa300", true, "expected 20 bytes"},
		{"bad hex", "0xZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ", true, "not valid hex"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEthAddress(tt.addr)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
			if tt.wantErr && err != nil && tt.errMsg != "" {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got: %v", tt.errMsg, err)
				}
			}
		})
	}
}

func TestOnline(t *testing.T) {
	offline := &Config{}
	if offline.Online() {
		t.Error("expected Online() == false for empty RPC_URL")
	}

	online := &Config{RPCURL: "https://sepolia.base.org"}
	if !online.Online() {
		t.Error("expected Online() == true for non-empty RPC_URL")
	}
}

func TestLoad_MultipleValidationErrors(t *testing.T) {
	clearEnv(t)
	t.Setenv("RPC_URL", "https://sepolia.base.org")
	t.Setenv("PRIVATE_KEY", "0xbad")
	t.Setenv("FACTORY_ADDRESS", "0xbad")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for multiple invalid fields")
	}
	if !strings.Contains(err.Error(), "PRIVATE_KEY is invalid") {
		t.Errorf("expected PRIVATE_KEY error in: %v", err)
	}
	if !strings.Contains(err.Error(), "FACTORY_ADDRESS is invalid") {
		t.Errorf("expected FACTORY_ADDRESS error in: %v", err)
	}
}
