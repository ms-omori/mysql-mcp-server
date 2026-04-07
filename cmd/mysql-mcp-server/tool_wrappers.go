package main

import (
	"context"
	"github.com/askdba/mysql-mcp-server/internal/config"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func toolSkipsOutputTokenEstimate(name string) bool {
	switch name {
	case "list_variables", "list_status", "show_create_table":
		return true
	default:
		return false
	}
}

func wrapTool[I any, O any](toolName string, h mcp.ToolHandlerFor[I, O]) mcp.ToolHandlerFor[I, O] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input I) (*mcp.CallToolResult, O, error) {
		start := time.Now()
		res, out, err := h(ctx, req, input)

		// Only emit these extra logs when token tracking is explicitly enabled.
		// This keeps default behavior unchanged.
		if tokenTracking && toolName != "run_query" {
			inputTokens, _ := estimateTokensForValue(input)
			outputTokens := 0
			// Skip output estimation for tools that routinely return very large JSON;
			// tiktoken over big payloads can stall long enough for MCP clients to time out.
			if err == nil && !toolSkipsOutputTokenEstimate(toolName) {
				outputTokens, _ = estimateTokensForValue(out)
			}
			tokens := TokenUsage{
				InputEstimated:  inputTokens,
				OutputEstimated: outputTokens,
				TotalEstimated:  inputTokens + outputTokens,
				Model:           tokenModel,
			}

			// Record into the global metrics aggregator
			globalTokenMetrics.Record(toolName, inputTokens, outputTokens)

			fields := map[string]interface{}{
				"tool":        toolName,
				"duration_ms": time.Since(start).Milliseconds(),
				"tokens": map[string]interface{}{
					"input_estimated":  tokens.InputEstimated,
					"output_estimated": tokens.OutputEstimated,
					"total_estimated":  tokens.TotalEstimated,
					"model":            tokens.Model,
				},
			}
			if err != nil {
				fields["error"] = err.Error()
				logError("tool failed", fields)
			} else {
				logInfo("tool executed", fields)
			}
		}

		return res, out, err
	}
}

// Wrapped tool handlers used by both MCP and HTTP.
var (
	toolListDatabasesWrapped   = wrapTool("list_databases", toolListDatabases)
	toolListTablesWrapped      = wrapTool("list_tables", toolListTables)
	toolDescribeTableWrapped   = wrapTool("describe_table", toolDescribeTable)
	toolRunQueryWrapped        = toolRunQuery // run_query has dedicated query/audit logs with tokens
	toolPingWrapped            = wrapTool("ping", toolPing)
	toolServerInfoWrapped      = wrapTool("server_info", toolServerInfo)
	toolListConnectionsWrapped = wrapTool("list_connections", toolListConnections)
	toolUseConnectionWrapped   = wrapTool("use_connection", toolUseConnection)

	toolVectorSearchWrapped = wrapTool("vector_search", toolVectorSearch)
	toolVectorInfoWrapped   = wrapTool("vector_info", toolVectorInfo)

	toolListIndexesWrapped     = wrapTool("list_indexes", toolListIndexes)
	toolShowCreateTableWrapped = wrapTool("show_create_table", toolShowCreateTable)
	toolExplainQueryWrapped    = wrapTool("explain_query", toolExplainQuery)
	toolListViewsWrapped       = wrapTool("list_views", toolListViews)
	toolListTriggersWrapped    = wrapTool("list_triggers", toolListTriggers)
	toolListProceduresWrapped  = wrapTool("list_procedures", toolListProcedures)
	toolListFunctionsWrapped   = wrapTool("list_functions", toolListFunctions)
	toolListPartitionsWrapped  = wrapTool("list_partitions", toolListPartitions)
	toolDatabaseSizeWrapped    = wrapTool("database_size", toolDatabaseSize)
	toolTableSizeWrapped       = wrapTool("table_size", toolTableSize)
	toolForeignKeysWrapped     = wrapTool("foreign_keys", toolForeignKeys)
	toolListStatusWrapped      = wrapTool("list_status", toolListStatus)
	toolListVariablesWrapped   = wrapTool("list_variables", toolListVariables)

	toolSearchSchemaWrapped = wrapTool("search_schema", toolSearchSchema)
	toolSchemaDiffWrapped   = wrapTool("schema_diff", toolSchemaDiff)

	toolProcessListWrapped  = wrapTool("process_list", toolProcessList)
	toolKillQueryWrapped    = wrapTool("kill_query", toolKillQuery)
	toolReadAuditLogWrapped = wrapTool("read_audit_log", toolReadAuditLog)
	toolSlowQueryLogWrapped = wrapTool("slow_query_log", toolSlowQueryLog)
)

func wrapAddConnection(cm *ConnectionManager, cfg *config.Config) func(context.Context, *mcp.CallToolRequest, AddConnectionInput) (*mcp.CallToolResult, AddConnectionOutput, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input AddConnectionInput) (*mcp.CallToolResult, AddConnectionOutput, error) {
		return toolAddConnection(ctx, req, input, cm, cfg)
	}
}
