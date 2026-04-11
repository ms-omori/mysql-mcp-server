# Design Specification: Issue #106 - Runtime Connection Registration (add_connection)

## 1. Overview
The goal of this feature is to allow the LLM to register and switch to new MySQL/MariaDB database instances at runtime without requiring a server restart. This is particularly useful for environments where new databases are provisioned dynamically or for multi-tenant exploration.

## 2. Approach: Native ConnectionManager Integration
We will implement a new MCP tool `add_connection` that leverages the existing `ConnectionManager` logic. The tool will handle DSN validation, connectivity testing (via `Ping` inside the add path), and automatic switching of the **process-wide** active connection.

**Active connection scope:** The server uses a **single** `ConnectionManager` instance per process. `SetActive(name)` updates **`ConnectionManager.activeConn`**, which determines which `*sql.DB` pool **`getDB()`** / tools use. This is **global to the MCP server process**, not per MCP client session, HTTP request, or goroutine. Any client or tool that shares this server sees the same “active” connection after a successful `add_connection`.

**Concurrency:** `ConnectionManager` uses a **`sync.RWMutex`** (`mu`). Writers take **`Lock`** for mutations (registering/removing pools, `activeConn`, tunnel metadata, and **`pendingAdds`** reservations for in-flight add-if-absent); readers take **`RLock`** in **`GetActive`**, **`GetActiveDB`**, **`List`**, **`GetServerType`**, etc. For **add-if-absent**, the implementation reserves the name under **`Lock`** in **`pendingAdds`** before any **`Ping()`** / tunnel work, so a concurrent second caller for the same name fails with “already exists” without duplicate network I/O (avoids TOCTOU between an early existence check and the final insert). The add path holds the mutex only for short check-and-register critical sections; **`Ping()`**, SSH tunnel setup, and server-type detection run **without** holding `mu` so concurrent readers are not stalled for network I/O. **`getDB()`** resolves the active pool under **`RLock`** via **`GetActiveDB`**. Do not call other lock-taking `ConnectionManager` methods while already holding **`Lock`**.

## 3. Data Flow (`add_connection`)
1. The user/LLM calls `add_connection` with `name`, `dsn`, and an optional `description`.
2. The server validates that the name is unique: not already registered and not reserved in **`pendingAdds`** for an in-flight add-if-absent; reservation and final insert use **`Lock`** (see §2 Concurrency).
3. **Safety Check (DSN-only):** The server parses the DSN and rejects the connection if the MySQL user is **`root`** (case-insensitive), as a baseline guard for runtime registration. **This does not inspect MySQL grants:** detecting `SUPER`, `ALL PRIVILEGES`, or role membership requires a live session and privilege tables; the server does not do that here. **Host/port policy** (allowlist/denylist, blocking RFC1918, localhost, etc.) is **not** enforced in code by default—operators should combine network policy, firewall rules, and MCP client access control. **Caller authentication, authorization, and rate limiting** for `add_connection` are **not** implemented in this tool: MCP stdio access is assumed to be a single trusted operator boundary; throttle or gate at the host application if required.
4. The server builds a `config.ConnectionConfig` for `name` / `dsn` / `description`.
5. **`ConnectionManager.AddConnectionIfAbsentWithPoolConfig(ctx, connCfg, cfg)`** applies SSL/read-only defaults, may establish an SSH tunnel, opens the pool, and runs **`PingContext`** with a timeout derived from **`ctx`** and **`cfg.PingTimeout`**. On **any** failure before registration (including **context cancellation or deadline** on ping), the implementation **closes** the partially opened **`*sql.DB`**, **closes** any SSH tunnel started for this attempt, and **does not** insert **`ConnectionConfig`** or register the pool—no half-registered state. The caller may retry with a new request. See **`cleanupLocal`** / error returns in **`addConnectionWithPoolConfig`** in **`connection.go`**.
6. If the pool is healthy, **`SetActive(name)`** sets the process-wide active connection to the new name (so subsequent tools use this `*sql.DB`).
7. **Rollback:** If **`SetActive(name)`** fails **after** a successful add (e.g. unexpected internal state), the implementation **must not** leave a fully registered pool without a consistent active selection. It should call **`RemoveConnection(name)`** to close the new pool, drop the `ConnectionConfig` entry, clear server-type metadata, tear down any per-name SSH tunnel, and clear **`activeConn`** if it still referenced `name`, then return an error to the caller. **`RemoveConnection`** is best-effort (e.g. **`Close()`** on **`*sql.DB`** does not return an error to the caller). If rollback appears to fail, **`toolAddConnection`** returns a single Go error that **wraps** both the activation failure and the rollback failure so operators see both (**`fmt.Errorf("... %w ... %v", err, rbErr)`**). **Persistent process-wide “corrupted” state markers** (e.g. `ConnectionState[name]`) and dedicated **`recover_connection` / `clear_corrupted`** APIs are **not** implemented: remediation is to retry **`RemoveConnection`**, restart the process, or inspect logs; future work could add explicit state tracking if needed.
8. On full success, the server returns a response naming the active connection.

**Replace (`AddConnectionWithPoolConfig`):** When replacing an existing name, the **old** pool and tunnel remain registered until the **new** DSN has been fully validated (ping + server-type probe). **`tearDownNamedConnection(name)`** runs only in the **final** lock section after success, so a failed replace does **not** delete a working registration.

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
  * Parse DSN and reject **`root`** user (case-insensitive); see §3 for scope of validation.
  * Call **`cm.AddConnectionIfAbsentWithPoolConfig(ctx, connCfg, cfg)`** (registers `ConnectionConfig`, pool, **`Ping`**).
  * Call **`cm.SetActive(name)`**; on error, **`cm.RemoveConnection(name)`** then return error.
* **cmd/mysql-mcp-server/tool_wrappers.go**: Wrap `toolAddConnection`.
* **cmd/mysql-mcp-server/main.go**: Register the tool when extended + opt-in env (see README).

## 6. Constraints & Edge Cases
* **Duplicate Names**: Returns an error if the name exists (prevents accidental overwrite) for **`AddConnectionIfAbsentWithPoolConfig`**.
* **Invalid DSN**: Returns an error if the DSN format is invalid or unreachable.
* **Privilege Guard**: Blocks **`root`** user DSNs for runtime registration; further privilege checks require deployment controls or future session-based validation.
* **Ping / context cancellation**: Failed or cancelled **`PingContext`** closes ephemeral resources and leaves the manager unchanged; safe to retry.
