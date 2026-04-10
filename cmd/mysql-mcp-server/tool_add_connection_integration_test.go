//go:build integration

package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/askdba/mysql-mcp-server/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestToolAddConnectionIntegrationAddsAndActivates exercises the success path against a real
// MySQL when MYSQL_TEST_DSN or MYSQL_DSN is set (see Makefile test-integration).
func TestToolAddConnectionIntegrationAddsAndActivates(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		dsn = os.Getenv("MYSQL_DSN")
	}
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN or MYSQL_DSN not set")
	}

	cfg := &config.Config{
		QueryTimeout:    time.Duration(config.DefaultQueryTimeoutSecs) * time.Second,
		PingTimeout:     time.Duration(config.DefaultPingTimeoutSecs) * time.Second,
		MaxOpenConns:    config.DefaultMaxOpenConns,
		MaxIdleConns:    config.DefaultMaxIdleConns,
		ConnMaxLifetime: time.Duration(config.DefaultConnMaxLifetimeMins) * time.Minute,
		ConnMaxIdleTime: time.Duration(config.DefaultConnMaxIdleTimeMins) * time.Minute,
	}

	cm := NewConnectionManager()
	name := fmt.Sprintf("add_conn_integ_%d", time.Now().UnixNano())
	input := AddConnectionInput{
		Name:        name,
		DSN:         dsn,
		Description: "integration test",
	}

	_, out, err := toolAddConnection(context.Background(), &mcp.CallToolRequest{}, input, cm, cfg)
	if err != nil {
		t.Fatalf("toolAddConnection: %v", err)
	}
	if !out.Success {
		t.Fatalf("expected success, got %+v", out)
	}
	if out.Active != name {
		t.Fatalf("active: want %q, got %q", name, out.Active)
	}
	db, active := cm.GetActive()
	if active != name || db == nil {
		t.Fatalf("GetActive: name=%q db_nil=%v", active, db == nil)
	}
	cm.Close()
}
