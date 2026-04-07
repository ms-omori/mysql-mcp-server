# Design Specification: Issue #106 - Runtime Connection Registration (add_connection)

## 1. Overview
The goal of this feature is to allow the LLM to register and switch to new MySQL/MariaDB database instances at runtime without requiring a server restart. This is particularly useful for environments where new databases are provisioned dynamically or for multi-tenant exploration.

## 2. Approach: Native ConnectionManager Integration
We will implement a new MCP tool `add_connection` that leverages the existing `ConnectionManager` logic. The tool will handle DSN validation, connectivity testing (via Ping), and automatic session switching.

## 3. Data Flow
1. The user/LLM calls `add_connection` with `name`, `dsn`, and an optional `description`.
2. The server validates that the `name` is unique and not already in use.
3. **Safety Check**: The server parses the DSN and rejects the connection if the user is `root` (to prevent high-privilege exploitation).
4. The server creates a new `config.ConnectionConfig`.
5. The `ConnectionManager` initializes the connection pool and performs a `Ping()`.
6. If the connection is successful, the server calls `SetActive(name)` to switch the current session context.
7. The server returns a success response including the new active connection name.

## 4. Struct Definitions
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

## 5. Implementation Details
* **cmd/mysql-mcp-server/types.go**: Add `AddConnectionInput` and `AddConnectionOutput`.
* **cmd/mysql-mcp-server/tools.go**: 
  * Implement `toolAddConnection`.
  * Add logic to parse DSN and verify `mysqlCfg.User != "root"`.
  * Call `cm.AddConnectionWithPoolConfig`.
  * Call `cm.SetActive`.
* **cmd/mysql-mcp-server/tool_wrappers.go**: Add `toolAddConnectionWrapped`.
* **cmd/mysql-mcp-server/main.go**: Register the tool in the MCP server.

## 6. Constraints & Edge Cases
* **Duplicate Names**: Returns an error if the name exists (prevents accidental overwrite).
* **Invalid DSN**: Returns an error if the DSN format is invalid or unreachable.
* **Privilege Guard**: Explicitly blocks `root` user DSNs as a baseline security measure for runtime registration.

