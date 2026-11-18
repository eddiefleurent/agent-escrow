package mcpserver

import (
	"testing"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDecompositionServiceWiring(t *testing.T) {
	t.Parallel()

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	s := &Server{db: db}
	svc := s.decompositionService()
	if svc == nil {
		t.Fatal("expected decomposition service")
	} else {
		if svc.DB != db {
			t.Fatal("expected decomposition service to use server DB dependency")
		}
		if svc.Bidding == nil {
			t.Fatal("expected decomposition service to wire bidding dependency")
		}
	}
}

func TestMCPEmergencyToolRegistration(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{name: "default"},
		{name: "http only", cfg: config.Config{EmergencyEnabled: true}},
		{name: "mcp only", cfg: config.Config{MCPEmergencyEnabled: true}, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			clientTransport, serverTransport := mcp.NewInMemoryTransports()
			server := mcp.NewServer(&mcp.Implementation{Name: "test server"}, nil)
			s := &Server{cfg: &tc.cfg}
			s.registerTools(server)
			serverSession, err := server.Connect(ctx, serverTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer serverSession.Close()
			client := mcp.NewClient(&mcp.Implementation{Name: "test client"}, nil)
			session, err := client.Connect(ctx, clientTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()

			tools, err := session.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			names := make(map[string]bool)
			for _, tool := range tools.Tools {
				names[tool.Name] = true
			}
			for _, name := range []string{
				"list_dct_audit", "emergency_override_dct", "freeze_address",
				"unfreeze_address", "freeze_escrow", "unfreeze_escrow", "emergency_resolve",
				"list_frozen_addresses", "list_emergency_actions",
			} {
				if names[name] != tc.want {
					t.Errorf("%s registered = %t, want %t", name, names[name], tc.want)
				}
			}
		})
	}
}
