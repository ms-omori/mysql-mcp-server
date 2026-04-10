# Design Specification: Issue #106 - Runtime Connection Registration (add_connection)

## 1. Overview
The goal of this feature is to allow the LLM to register and switch to new MySQL/MariaDB database instances at runtime without requiring a server restart. This is particularly useful for environments where new databases are provisioned dynamically or for multi-tenant exploration.

## 2. Approach: Native ConnectionManager Integration
We will implement a new MCP tool `add_connection` that leverages the existing `ConnectionManager` logic. The tool will handle DSN validation, connectivity testing (via `Ping` inside the add path), and automatic switching of the **process-wide** active connection.

**Active connection scope:** The server uses a **single** `ConnectionManager` instance per process. `SetActive(name)` updates **`ConnectionManager.activeConn`**, which determines which `*sql.DB` pool **`getDB()`** / tools use. This is **global to the MCP server process**, not per MCP client session, HTTP request, or goroutine. Any client or tool that shares this server sees the same “active” connection after a successful `add_connection`.

## 3. Data Flow (`add_connection`)
1. The user/LLM calls `add_connection` with `name`, `dsn`, and an optional `description`.
2. The server validates that the `name` is unique and not already registered on the `ConnectionManager` (atomic check-and-add).
3. **Safety Check**: The server parses the DSN and rejects the connection if the user is `root` (to prevent high-privilege exploitation).
4. The server builds a `config.ConnectionConfig` for `name` / `dsn` / `description`.
5. **`ConnectionManager.AddConnectionIfAbsentWithPoolConfig`** (or equivalent) applies SSL/read-only defaults, may establish an SSH tunnel, opens the pool, and runs **`Ping()`** (with request-scoped context for timeouts/cancellation). On failure, nothing is registered.
6. If the pool is healthy, **`SetActive(name)`** sets the process-wide active connection to the new name (so subsequent tools use this `*sql.DB`).
7. **Rollback:** If **`SetActive(name)`** fails **after** a successful add (e.g. unexpected internal state), the implementation **must not** leave a fully registered pool without a consistent active selection. It should call **`RemoveConnection(name)`** to close the new pool, drop the `ConnectionConfig` entry, clear server-type metadata, tear down any per-name SSH tunnel, and clear **`activeConn`** if it still referenced `name`, then return an error to the caller. If rollback itself fails, the error should surface both activation and rollback failures.
8. On full success, the server returns a response naming the active connection.

**Note:** When this is the **first** connection in the manager, the add path may already set `activeConn` to the new name; `SetActive(name)` should still succeed and is idempotent for that case.

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
* **cmd/mysql-mcp-server/connection.go**: Expose **`AddConnectionIfAbsentWithPoolConfig`**, **`RemoveConnection(name)`** (rollback), and keep **`SetActive(name)`** / **`Ping`** behavior as today.
* **cmd/mysql-mcp-server/tools.go**: 
  * Implement `toolAddConnection`.
  * Parse DSN and reject `mysqlCfg.User == "root"`.
  * Call **`cm.AddConnectionIfAbsentWithPoolConfig(ctx, connCfg, cfg)`** (registers `ConnectionConfig`, pool, **`Ping`**).
  * Call **`cm.SetActive(name)`**; on error, **`cm.RemoveConnection(name)`** then return error.
* **cmd/mysql-mcp-server/tool_wrappers.go**: Wrap `toolAddConnection`.
* **cmd/mysql-mcp-server/main.go**: Register the tool when extended + opt-in env (see README).

## 6. Constraints & Edge Cases
* **Duplicate Names**: Returns an error if the name exists (prevents accidental overwrite).
* **Invalid DSN**: Returns an error if the DSN format is invalid or unreachable.
* **Privilege Guard**: Explicitly blocks `root` user DSNs as a baseline security measure for runtime registration.

