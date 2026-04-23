// cmd/mysql-mcp-server/tools_execute.go
package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/askdba/mysql-mcp-server/internal/dbretry"
	"github.com/askdba/mysql-mcp-server/internal/util"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// execOnConn runs finalSQL (optionally after USE database) on a dedicated
// connection and returns RowsAffected / LastInsertId. Mirrors runQueryScan's
// connection pinning so the USE scope does not leak back into the pool.
func execOnConn(ctx context.Context, db *sql.DB, finalSQL, database string) (sql.Result, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Close()

	if database != "" {
		quotedDB, err := util.QuoteIdent(database)
		if err != nil {
			return nil, fmt.Errorf("invalid database name: %w", err)
		}
		if _, err := conn.ExecContext(ctx, "USE "+quotedDB); err != nil {
			return nil, fmt.Errorf("failed to select database '%s': %w", database, err)
		}
	}

	return conn.ExecContext(ctx, finalSQL)
}

// toolExecute runs a DML write statement on the active connection. Writes are
// refused when the active connection is flagged read-only so operators can
// route writes per-DSN via ConnectionConfig.ReadOnly. Global
// MYSQL_MCP_STRICT_READ_ONLY=1 also disables this tool to preserve the
// backward-compatible guarantee that strict mode forbids all writes.
func toolExecute(
	ctx context.Context,
	req *mcp.CallToolRequest,
	input ExecuteInput,
) (*mcp.CallToolResult, ExecuteResult, error) {
	timer := NewQueryTimer("execute")

	sqlText := strings.TrimSpace(input.SQL)
	database := strings.TrimSpace(input.Database)

	inputTokens, _ := estimateTokensForValue(input)
	tokens := &TokenUsage{
		InputEstimated: inputTokens,
		TotalEstimated: inputTokens,
		Model:          tokenModel,
	}

	// logRejection records an audit entry with consistent fields for all
	// pre-execution rejection paths (missing args, read-only, validator, etc.).
	logRejection := func(err error) {
		if auditLogger == nil {
			return
		}
		auditLogger.Log(&AuditEntry{
			Tool:        "execute",
			Database:    database,
			Query:       util.TruncateQuery(sqlText, 500),
			DurationMs:  timer.ElapsedMs(),
			InputTokens: inputTokens,
			Success:     false,
			Error:       err.Error(),
		})
	}

	if sqlText == "" {
		err := fmt.Errorf("sql is required")
		logRejection(err)
		return nil, ExecuteResult{}, err
	}

	if accessControlEnabled() {
		if database == "" {
			err := fmt.Errorf("database is required when MYSQL_MCP_ALLOWED_DATABASES is set")
			logRejection(err)
			return nil, ExecuteResult{}, err
		}
		if err := requireAllowedDatabase(database); err != nil {
			logRejection(err)
			return nil, ExecuteResult{}, err
		}
	}

	// Global strict read-only mode incompatible with execute by design.
	if cfg != nil && cfg.StrictReadOnly {
		err := fmt.Errorf(
			"execute is disabled while MYSQL_MCP_STRICT_READ_ONLY is set; " +
				"unset the env var (or remove strict_read_only from the config file) to allow writes",
		)
		logRejection(err)
		return nil, ExecuteResult{}, err
	}

	_, activeName := connManager.GetActive()
	if activeName == "" {
		err := fmt.Errorf("no active MySQL connection; add one via add_connection or configure MYSQL_DSN")
		logRejection(err)
		return nil, ExecuteResult{}, err
	}
	if connManager.IsActiveReadOnly() {
		err := fmt.Errorf(
			"active connection %q is read-only; switch via use_connection or mark it writable "+
				"(read_only=false / unset MYSQL_DSN_READ_ONLY)",
			activeName,
		)
		logRejection(err)
		return nil, ExecuteResult{}, err
	}

	if err := util.ValidateWriteSQLCombined(sqlText); err != nil {
		logWarn("write query rejected by validator", map[string]interface{}{
			"error": err.Error(),
			"query": util.TruncateQuery(sqlText, 200),
		})
		wrapped := fmt.Errorf("query validation failed: %w", err)
		logRejection(wrapped)
		return nil, ExecuteResult{}, wrapped
	}
	if err := requireReferencedSchemasInQuery(sqlText); err != nil {
		logRejection(err)
		return nil, ExecuteResult{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	db := getDB()
	var result sql.Result
	err := dbretry.Do(ctx, db, dbRetryCfg, pingTimeout, func() error {
		var e error
		result, e = execOnConn(ctx, db, sqlText, database)
		return e
	})
	if err != nil {
		timer.LogError(err, sqlText, tokens, nil)
		if auditLogger != nil {
			auditLogger.Log(&AuditEntry{
				Tool:        "execute",
				Database:    database,
				Query:       util.TruncateQuery(sqlText, 500),
				DurationMs:  timer.ElapsedMs(),
				InputTokens: inputTokens,
				Success:     false,
				Error:       err.Error(),
			})
		}
		return nil, ExecuteResult{}, err
	}

	affected, _ := result.RowsAffected()
	lastID, _ := result.LastInsertId()
	out := ExecuteResult{
		AffectedRows: affected,
		LastInsertID: lastID,
		Connection:   activeName,
	}

	outputTokens, _ := estimateTokensForValue(out)
	tokens.OutputEstimated = outputTokens
	tokens.TotalEstimated = inputTokens + outputTokens

	if tokenTracking {
		globalTokenMetrics.Record("execute", inputTokens, outputTokens)
	}

	eff := CalculateEfficiency(inputTokens, outputTokens, int(affected))

	timer.LogSuccess(int(affected), sqlText, tokens, eff)
	if auditLogger != nil {
		auditLogger.Log(&AuditEntry{
			Tool:         "execute",
			Database:     database,
			Query:        util.TruncateQuery(sqlText, 500),
			DurationMs:   timer.ElapsedMs(),
			RowCount:     int(affected),
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			Success:      true,
		})
	}

	return nil, out, nil
}
