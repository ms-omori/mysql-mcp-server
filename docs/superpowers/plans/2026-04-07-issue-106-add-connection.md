# Runtime Connection Registration (add_connection) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the `add_connection` tool to allow registering and switching to new MySQL connections at runtime.

**Architecture:** We will define input/output types in `types.go`, implement the tool logic in `tools.go` (including security checks for root), wrap the tool with a custom closure to access the global config and connection manager, and register it in `main.go`.

**Tech Stack:** Go, standard library, `github.com/go-sql-driver/mysql`, `github.com/modelcontextprotocol/go-sdk/mcp`.

---

### Task 1: Define Structs in types.go

**Files:**
- Modify: `cmd/mysql-mcp-server/types.go`

- [ ] **Step 1: Write the structs**
Add `AddConnectionInput` and `AddConnectionOutput` to `types.go`.

```go
type AddConnectionInput struct {
	Name        string `json:"name" jsonschema:"unique name for the new connection"`
	DSN         string `json:"dsn" jsonschema:"MySQL DSN (user:pass@tcp(host:port)/db)"`
	Description string `json:"description,omitempty" jsonschema:"optional description of the connection"`
}

type AddConnectionOutput struct {
	Success bool   `json:"success" jsonschema:"true if connection was added and activated"`
	Active  string `json:"active" jsonschema:"name of the now-active connection"`
	Message string `json:"message" jsonschema:"status message"`
}
```

- [ ] **Step 2: Commit**
```bash
git add cmd/mysql-mcp-server/types.go
git commit -m "feat: add AddConnectionInput/Output structs to types.go"
```

---

### Task 2: Implement Tool Logic in tools.go

**Files:**
- Modify: `cmd/mysql-mcp-server/tools.go`

- [ ] **Step 1: Implement toolAddConnection**
Add the core logic for the tool. This function needs access to the `ConnectionManager` and `config.Config`. Since most tools use a specific signature for `wrapTool`, we will define this and later handle the dependency injection.

```go
func toolAddConnection(
	ctx context.Context,
	req *mcp.CallToolRequest,
	input AddConnectionInput,
	cm *ConnectionManager,
	cfg *config.Config,
) (*mcp.CallToolResult, AddConnectionOutput, error) {
	name := strings.TrimSpace(input.Name)
	dsn := strings.TrimSpace(input.DSN)
	if name == "" || dsn == "" {
		return nil, AddConnectionOutput{}, fmt.Errorf("name and dsn are required")
	}

	// 1. Check if name already exists
	conns := cm.List()
	for _, c := range conns {
		if c.Name == name {
			return nil, AddConnectionOutput{}, fmt.Errorf("connection '%s' already exists", name)
		}
	}

	// 2. Safety Check: Reject root user
	mysqlCfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, AddConnectionOutput{}, fmt.Errorf("invalid DSN: %w", err)
	}
	if mysqlCfg.User == "root" {
		return nil, AddConnectionOutput{}, fmt.Errorf("security policy: runtime registration of 'root' user is not allowed")
	}

	// 3. Add connection
	connCfg := config.ConnectionConfig{
		Name:        name,
		DSN:         dsn,
		Description: input.Description,
	}
	if err := cm.AddConnectionWithPoolConfig(connCfg, cfg); err != nil {
		return nil, AddConnectionOutput{}, fmt.Errorf("failed to add connection: %w", err)
	}

	// 4. Automatically switch to it
	if err := cm.SetActive(name); err != nil {
		return nil, AddConnectionOutput{}, fmt.Errorf("failed to activate connection: %w", err)
	}

	return mcp.NewToolResultText(fmt.Sprintf("Successfully added and switched to connection '%s'.", name)),
		AddConnectionOutput{
			Success: true,
			Active:  name,
			Message: fmt.Sprintf("Added and switched to connection '%s'", name),
		}, nil
}
```

- [ ] **Step 2: Commit**
```bash
git add cmd/mysql-mcp-server/tools.go
git commit -m "feat: implement toolAddConnection logic"
```

---

### Task 3: Wrap and Register the Tool

**Files:**
- Modify: `cmd/mysql-mcp-server/tool_wrappers.go`
- Modify: `cmd/mysql-mcp-server/main.go`

- [ ] **Step 1: Create a specialized wrapper in tool_wrappers.go**
Most tools are wrapped using `wrapTool`. `toolAddConnection` needs extra parameters. We will create a manual wrapper or extend `tool_wrappers.go`.

```go
// Add this to tool_wrappers.go
func wrapAddConnection(cm *ConnectionManager, cfg *config.Config) func(context.Context, *mcp.CallToolRequest, AddConnectionInput) (*mcp.CallToolResult, AddConnectionOutput, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input AddConnectionInput) (*mcp.CallToolResult, AddConnectionOutput, error) {
		return toolAddConnection(ctx, req, input, cm, cfg)
	}
}
```

- [ ] **Step 2: Register tool in main.go**
Locate the tool registration block in `main.go` and add `add_connection`.

```go
	// In main.go after other tools are registered
	mcp.AddTool(server, &mcp.Tool{
		Name:        "add_connection",
		Description: "Register and switch to a new MySQL connection at runtime.",
	}, wrapTool("add_connection", wrapAddConnection(connManager, cfg)))
```

- [ ] **Step 3: Run Build**
Run: `go build ./...`
Expected: PASS

- [ ] **Step 4: Commit**
```bash
git add cmd/mysql-mcp-server/tool_wrappers.go cmd/mysql-mcp-server/main.go
git commit -m "feat: wrap and register add_connection tool"
```

---

### Task 4: Verify with Unit Test

**Files:**
- Create: `cmd/mysql-mcp-server/connection_tool_test.go`

- [ ] **Step 1: Write test case**
Test that `root` is rejected and that a valid connection (mocked) is added and activated.

```go
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
	if err.Error() != "security policy: runtime registration of 'root' user is not allowed" {
		t.Errorf("unexpected error message: %v", err)
	}
}
```

- [ ] **Step 2: Run test**
Run: `go test -v ./cmd/mysql-mcp-server/...`
Expected: PASS

- [ ] **Step 3: Commit**
```bash
git add cmd/mysql-mcp-server/connection_tool_test.go
git commit -m "test: add security verification for add_connection tool"
```
