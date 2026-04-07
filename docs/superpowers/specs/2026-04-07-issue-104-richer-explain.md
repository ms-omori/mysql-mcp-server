# Design Specification: Issue #104 - Richer EXPLAIN as Structured Output

## 1. Overview
The goal of this feature is to upgrade the `explain_query` tool to provide a strongly typed, unified JSON schema for query execution plans. This ensures that the LLM receives predictable, structured data for query analysis, regardless of whether the backend engine is MySQL (8.x, 9.x) or MariaDB (10.x, 11.x).

## 2. Approach: Strongly Typed Unified Schema
We will implement a normalization layer that executes `EXPLAIN FORMAT=JSON`, unmarshals the engine-specific JSON, and maps it to a unified Go struct. The LLM will perform all analysis; no auto-generated warnings will be provided by the server.

## 3. Data Flow
1. The user requests an `explain_query` via MCP.
2. If `format` is "traditional" (or unspecified but explicitly requested as traditional), the tool behaves as it previously did (returning raw tabular output).
3. If `format` is "json" (the new default), the server executes `EXPLAIN FORMAT=JSON <query>`.
4. The server unmarshals the raw JSON string returned by the database.
5. A mapping function traverses the raw JSON and populates the `UnifiedExplainPlan` struct.
6. The structured `ExplainQueryOutput` is returned to the client.

## 4. Struct Definitions
```go
type UnifiedExplainPlan struct {
	QueryCost  float64     `json:"query_cost,omitempty" jsonschema:"Total estimated cost of the query"`
	Operations []UnifiedOp `json:"operations" jsonschema:"List of operations in the execution plan"`
}

type UnifiedOp struct {
	TableName      string   `json:"table_name,omitempty" jsonschema:"Table being accessed"`
	AccessType     string   `json:"access_type,omitempty" jsonschema:"Join type (e.g., ALL, ref, range, index)"`
	PossibleKeys   []string `json:"possible_keys,omitempty" jsonschema:"Indexes that could be used"`
	Key            string   `json:"key,omitempty" jsonschema:"The actual index used"`
	KeyLength      string   `json:"key_length,omitempty" jsonschema:"Length of the chosen key"`
	RowsExamined   int64    `json:"rows_examined,omitempty" jsonschema:"Estimated number of rows read"`
	Filtered       float64  `json:"filtered_percent,omitempty" jsonschema:"Percentage of rows filtered by table condition"`
	CostInfo       CostInfo `json:"cost_info,omitempty" jsonschema:"Detailed cost metrics"`
	AttachedCondition string `json:"attached_condition,omitempty" jsonschema:"WHERE or ON clauses applied during table access"`
	Message        string   `json:"message,omitempty" jsonschema:"Additional execution details (e.g., Using temporary, Using filesort)"`
}

type CostInfo struct {
	ReadCost      float64 `json:"read_cost,omitempty"`
	EvalCost      float64 `json:"eval_cost,omitempty"`
	PrefixCost    float64 `json:"prefix_cost,omitempty"`
	DataReadPerJoin string `json:"data_read_per_join,omitempty"`
}
```

## 5. Implementation Details
* **cmd/mysql-mcp-server/types.go**: Add the `UnifiedExplainPlan` and associated structs.
* **cmd/mysql-mcp-server/tools_extended.go**: 
  * Modify `toolExplainQuery` to set `json` as the default format.
  * Execute `EXPLAIN FORMAT=JSON`.
  * Parse the JSON string (which is returned as a single row/column from the database).
  * Map the MySQL and MariaDB JSON topologies (which differ slightly in nesting) into the `UnifiedExplainPlan`.
  * Update the return structure.

## 6. Backwards Compatibility
The `ExplainQueryInput` will retain the `Format` field. If `format: "traditional"` is passed, the tool will fallback to executing a standard `EXPLAIN` and returning the raw tabular data.

