package main

import (
	"context"
	"testing"
	"github.com/askdba/mysql-mcp-server/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolAddConnectionSecurity(t *testing.T) {
	cm := NewConnectionManager()
	cfg := &config.Config{}
	ctx := context.Background()

	input := AddConnectionInput{
		Name: "test-root",
		DSN:  "root:secret@tcp(localhost:3306)/db",
	}

	_, _, err := toolAddConnection(ctx, &mcp.CallToolRequest{}, input, cm, cfg)
	if err == nil {
		t.Fatal("expected error when adding root user, got nil")
	}
	expectedErr := "security policy: runtime registration of 'root' user is not allowed"
	if err.Error() != expectedErr {
		t.Errorf("unexpected error message: %v, expected %v", err, expectedErr)
	}
}

