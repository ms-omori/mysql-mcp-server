package main

import (
	"context"
	"strings"
	"testing"

	"github.com/askdba/mysql-mcp-server/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolAddConnectionValidation(t *testing.T) {
	tests := []struct {
		name        string
		input       AddConnectionInput
		wantErrSubs string
	}{
		{
			name:        "root user rejected",
			input:       AddConnectionInput{Name: "test-root", DSN: "root:secret@tcp(localhost:3306)/db"},
			wantErrSubs: "security policy: runtime registration of 'root' user is not allowed",
		},
		{
			name:        "empty name",
			input:       AddConnectionInput{Name: "", DSN: "user:pass@tcp(localhost:3306)/db"},
			wantErrSubs: "name and dsn are required",
		},
		{
			name:        "empty DSN",
			input:       AddConnectionInput{Name: "myconn", DSN: ""},
			wantErrSubs: "name and dsn are required",
		},
		{
			name:        "invalid DSN format",
			input:       AddConnectionInput{Name: "myconn", DSN: "not-a-valid-dsn"},
			wantErrSubs: "invalid DSN",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cm := NewConnectionManager()
			cfg := &config.Config{}
			ctx := context.Background()

			_, _, err := toolAddConnection(ctx, &mcp.CallToolRequest{}, tc.input, cm, cfg)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrSubs)
			}
			if !strings.Contains(err.Error(), tc.wantErrSubs) {
				t.Errorf("error %q does not contain expected substring %q", err.Error(), tc.wantErrSubs)
			}
		})
	}
}

func TestToolAddConnectionDuplicateName(t *testing.T) {
	cm := NewConnectionManager()
	cfg := &config.Config{}
	ctx := context.Background()

	// Manually pre-populate the connection manager with a mock entry
	// by injecting via configs map to simulate an existing named connection
	cm.mu.Lock()
	cm.configs["existing"] = config.ConnectionConfig{Name: "existing", DSN: "user:pass@tcp(localhost:3306)/db"}
	cm.mu.Unlock()

	input := AddConnectionInput{
		Name: "existing",
		DSN:  "appuser:pass@tcp(localhost:3306)/db",
	}

	_, _, err := toolAddConnection(ctx, &mcp.CallToolRequest{}, input, cm, cfg)
	if err == nil {
		t.Fatal("expected error for duplicate connection name, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("unexpected error: %v", err)
	}
}
