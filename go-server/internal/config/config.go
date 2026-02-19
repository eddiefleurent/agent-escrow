package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	RPCURL         string
	PrivateKey     string
	ChainID        int64
	FactoryAddress string
	DatabaseURL    string
	Port           int
	MCPTransport   string
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

	return &Config{
		RPCURL:         os.Getenv("RPC_URL"),
		PrivateKey:     os.Getenv("PRIVATE_KEY"),
		ChainID:        chainID,
		FactoryAddress: os.Getenv("FACTORY_ADDRESS"),
		DatabaseURL:    dbURL,
		Port:           port,
		MCPTransport:   os.Getenv("MCP_TRANSPORT"),
	}, nil
}
