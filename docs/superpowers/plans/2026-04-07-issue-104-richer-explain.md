# Richer EXPLAIN as Structured Output Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upgrade the `explain_query` tool to execute `EXPLAIN FORMAT=JSON` and parse it into a strongly typed, unified Go struct (`UnifiedExplainPlan`) to provide the LLM with predictable JSON.

**Architecture:** We will add the unified structs to `types.go`. We will then update `toolExplainQuery` in `tools_extended.go`. It will default to "json" format. If format is "traditional", it falls back to raw tables. If "json", it executes `EXPLAIN FORMAT=JSON`, unmarshals the raw string, and maps it to the new `UnifiedExplainPlan` struct, which is returned in the `ExplainQueryOutput`.

**Tech Stack:** Go, standard library (`encoding/json`, `database/sql`).

---

### Task 1: Define Unified Structs in types.go

**Files:**
- Modify: `cmd/mysql-mcp-server/types.go`

- [ ] **Step 1: Write the structs**
Add the new unified structures to `types.go`. Note: `ExplainQueryOutput` currently holds `[]map[string]interface{}`. We will change this to `interface{}` to support both the legacy array of maps (traditional) and the new unified struct (json).

```go
type ExplainQueryOutput struct {
	Plan     interface{} `json:"plan" jsonschema:"query execution plan (array of maps for traditional, or object for json)"`
	Warnings []string    `json:"warnings,omitempty" jsonschema:"actionable optimization suggestions derived from the execution plan"`
}

type UnifiedExplainPlan struct {
	QueryCost  float64     `json:"query_cost,omitempty" jsonschema:"Total estimated cost of the query"`
	Operations []UnifiedOp `json:"operations" jsonschema:"List of operations in the execution plan"`
}

type UnifiedOp struct {
	TableName         string     `json:"table_name,omitempty" jsonschema:"Table being accessed"`
	AccessType        string     `json:"access_type,omitempty" jsonschema:"Join type (e.g., ALL, ref, range, index)"`
	PossibleKeys      []string   `json:"possible_keys,omitempty" jsonschema:"Indexes that could be used"`
	Key               string     `json:"key,omitempty" jsonschema:"The actual index used"`
	KeyLength         string     `json:"key_length,omitempty" jsonschema:"Length of the chosen key"`
	RowsExamined      int64      `json:"rows_examined,omitempty" jsonschema:"Estimated number of rows read"`
	Filtered          float64    `json:"filtered,omitempty" jsonschema:"Percentage of rows filtered (matches EXPLAIN JSON key filtered)"`
	CostInfo          OpCostInfo `json:"cost_info,omitempty" jsonschema:"Detailed cost metrics"`
	AttachedCondition string     `json:"attached_condition,omitempty" jsonschema:"WHERE or ON clauses applied during table access"`
	Message           string     `json:"message,omitempty" jsonschema:"Additional execution details (e.g., Using temporary, Using filesort)"`
}

type OpCostInfo struct {
	ReadCost        float64 `json:"read_cost,omitempty"`
	EvalCost        float64 `json:"eval_cost,omitempty"`
	PrefixCost      float64 `json:"prefix_cost,omitempty"`
	DataReadPerJoin string  `json:"data_read_per_join,omitempty"`
}
```

- [ ] **Step 2: Commit**
```bash
git add cmd/mysql-mcp-server/types.go
git commit -m "feat: add unified explain structs to types.go"
```

### Task 2: Implement Mapping Logic

**Files:**
- Modify: `cmd/mysql-mcp-server/tools_extended.go`

- [ ] **Step 1: Write mapping function**
Add a helper function inside `tools_extended.go` to parse the raw JSON string into the unified struct. We will use a generic map-based traversal to handle both MySQL and MariaDB safely without complex nested unmarshaling errors.

```go
func mapRawExplainToUnified(rawJSON string) (UnifiedExplainPlan, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &raw); err != nil {
		return UnifiedExplainPlan{}, err
	}

	plan := UnifiedExplainPlan{}

	if qb, ok := raw["query_block"].(map[string]interface{}); ok {
		costFromInfo := false
		if ci, ok := qb["cost_info"].(map[string]interface{}); ok {
			if costStr, ok := ci["query_cost"].(string); ok {
				if v, err := strconv.ParseFloat(costStr, 64); err == nil {
					plan.QueryCost = v
					costFromInfo = true
				}
			}
		}
		if !costFromInfo {
			if v, ok := float64FromExplainJSONNumber(qb["cost"]); ok {
				plan.QueryCost = v
			}
		}
		if tables, ok := qb["table"].(map[string]interface{}); ok {
			plan.Operations = append(plan.Operations, extractUnifiedOp(tables))
		} else if tablesList, ok := qb["table"].([]interface{}); ok {
			for _, t := range tablesList {
				if tMap, ok := t.(map[string]interface{}); ok {
					plan.Operations = append(plan.Operations, extractUnifiedOp(tMap))
				}
			}
		} else if nestedOps, ok := qb["nested_loop"].([]interface{}); ok {
			for _, nl := range nestedOps {
				if nlMap, ok := nl.(map[string]interface{}); ok {
					if tMap, ok := tableMapFromNestedLoopStep(nlMap); ok {
						plan.Operations = append(plan.Operations, extractUnifiedOp(tMap))
					}
				}
			}
		}
	}

	return plan, nil
}
// extractUnifiedOp, float64FromExplainJSONNumber, tableMapFromNestedLoopStep: see tools_extended.go
```

- [ ] **Step 2: Update toolExplainQuery logic**
Modify `toolExplainQuery` to handle the format parameter. Default to JSON. Update the SQL execution to inject `FORMAT=JSON` if required.

Change:
```go
	format := strings.ToLower(strings.TrimSpace(input.Format))
	if format == "" {
		format = "json" // Default to new structured json
	}
	
	explainSQL := "EXPLAIN "
	if format == "json" {
		explainSQL = "EXPLAIN FORMAT=JSON "
	} else if format == "tree" {
		explainSQL = "EXPLAIN FORMAT=TREE "
	}
	explainSQL += sqlText
```

Update row iteration:
```go
	var result interface{}

	if format == "json" {
		// JSON format returns a single row with a single string column
		if rows.Next() {
			var jsonPlan string
			if err := rows.Scan(&jsonPlan); err != nil {
				return nil, ExplainQueryOutput{}, fmt.Errorf("failed to scan json explain: %w", err)
			}
			unifiedPlan, parseErr := mapRawExplainToUnified(jsonPlan)
			if parseErr != nil {
				// Fallback to raw string if parsing fails
				result = jsonPlan
			} else {
				result = unifiedPlan
			}
		}
	} else {
		// Traditional tabular parsing
		cols, _ := rows.Columns()
		var traditionalPlan []map[string]interface{}
		for rows.Next() {
			rowValues := make([]interface{}, len(cols))
			rowPointers := make([]interface{}, len(cols))
			for i := range rowValues {
				rowPointers[i] = &rowValues[i]
			}
			if err := rows.Scan(rowPointers...); err != nil {
				return nil, ExplainQueryOutput{}, err
			}

			rowData := make(map[string]interface{})
			for i, col := range cols {
				val := rowValues[i]
				if b, ok := val.([]byte); ok {
					rowData[col] = string(b)
				} else {
					rowData[col] = val
				}
			}
			traditionalPlan = append(traditionalPlan, rowData)
		}
		result = traditionalPlan
	}

	return mcp.NewToolResultText("Successfully generated query execution plan."),
		ExplainQueryOutput{Plan: result, Warnings: []string{}},
		nil
```

- [ ] **Step 3: Run Build**
Run: `go build ./...`
Expected: PASS

- [ ] **Step 4: Commit**
```bash
git add cmd/mysql-mcp-server/tools_extended.go
git commit -m "feat: implement unified explain logic and update tool format handler"
```

### Task 3: Update Unit Tests

**Files:**
- Modify: `cmd/mysql-mcp-server/tools_extended_test.go`

- [ ] **Step 1: Write test for mapping logic**
Create a new test function `TestMapRawExplainToUnified` to verify the JSON mapping layer handles MySQL 8 json properly.

```go
func TestMapRawExplainToUnified(t *testing.T) {
	mockJSON := `{
		"query_block": {
			"cost_info": { "query_cost": "2.50" },
			"table": {
				"table_name": "users",
				"access_type": "ref",
				"rows": 10,
				"filtered": "100.00"
			}
		}
	}`

	plan, err := mapRawExplainToUnified(mockJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.QueryCost != 2.50 {
		t.Errorf("expected cost 2.50, got %v", plan.QueryCost)
	}
	if len(plan.Operations) != 1 {
		t.Fatalf("expected 1 op, got %v", len(plan.Operations))
	}
	op := plan.Operations[0]
	if op.TableName != "users" {
		t.Errorf("expected users, got %v", op.TableName)
	}
	if op.RowsExamined != 10 {
		t.Errorf("expected 10 rows, got %v", op.RowsExamined)
	}
}
```

- [ ] **Step 2: Run unit test**
Run: `go test ./cmd/mysql-mcp-server -run TestMapRawExplainToUnified -v`
Expected: PASS

- [ ] **Step 3: Update existing TestExplainQuery traditional tests**
Because we changed `Plan` from `[]map[string]interface{}` to `interface{}` in `types.go`, we need to type assert in the existing tests. Search for `ExplainQueryOutput` casts in `TestExplainQuery` (in `tools_extended_test.go`) and update any length checks. E.g.: `len(output.Plan.([]map[string]interface{}))`

- [ ] **Step 4: Run all tests**
Run: `make test`
Expected: PASS

- [ ] **Step 5: Commit**
```bash
git add cmd/mysql-mcp-server/tools_extended_test.go
git commit -m "test: add mapping tests and fix traditional explain types"
```

