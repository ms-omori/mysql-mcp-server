# Design Specification: Issue #104 - Richer EXPLAIN as Structured Output

## 1. Overview
The goal of this feature is to upgrade the `explain_query` tool to provide a strongly typed, unified JSON schema for query execution plans. This ensures that the LLM receives predictable, structured data for query analysis, regardless of whether the backend engine is MySQL (8.x, 9.x) or MariaDB (10.x, 11.x).

## 2. Approach: Strongly Typed Unified Schema
We will implement a normalization layer that executes `EXPLAIN FORMAT=JSON`, unmarshals the engine-specific JSON, and maps it to a unified Go struct. The LLM will perform all analysis; no auto-generated warnings will be provided by the server.

## 3. Data Flow
1. The user requests an `explain_query` via MCP.
2. **Format and SQL sent to the server**
   - If the `format` field is **omitted or empty**, the server treats it as **`"json"`**: it runs **`EXPLAIN FORMAT=JSON <query>`** and returns structured output (see steps 4–6).
   - If **`format` is explicitly `"traditional"`**, the server runs a plain **`EXPLAIN <query>`** (no `FORMAT=JSON`) and returns the **previous raw tabular plan** as an array of row maps (same behavior as before structured JSON). Optimization **warnings** are attached for this path only when `format` is `"traditional"`.
   - Other explicit values (e.g. **`"tree"`**) follow the non-JSON path: `EXPLAIN` is run with the chosen format and rows are returned as scanned column maps (not the unified struct).
3. When the effective format is **`json`**, the server executes **`EXPLAIN FORMAT=JSON <query>`**.
4. The server unmarshals the raw JSON string returned by the database (single row/column).
5. A mapping function traverses the raw JSON and populates the **`UnifiedExplainPlan`** struct.
6. The structured **`ExplainQueryOutput`** is returned to the client.

### 3.1 `Operations []UnifiedOp`: representation of nesting
The unified schema uses a **flat list**, not a tree of nested `UnifiedOp` values.

- **`UnifiedExplainPlan.Operations`** is an **ordered list** of **`UnifiedOp`** entries in **execution order** as produced by a depth-first walk of the engine JSON: first the driving table access, then each joined table / nested-loop step in order.
- **`UnifiedOp` does not** carry **`SubOperations`**, **`Children`**, or parent pointers. Hierarchical relationships in the original plan (e.g. `nested_loop` arrays, subqueries, derived tables) are **flattened** into this sequence; the consumer infers structure only by position and by repeating patterns in the raw JSON if needed.
- **Encoding summary:** subqueries / derived tables / multi-level nests that appear as nested `query_block` or deep `nested_loop` trees in MySQL or MariaDB JSON are mapped by **emitting one `UnifiedOp` per leaf `table` object** encountered in that walk, in order. The spec does not require a separate field for subtree roots.

**Example (conceptual):** two-table join with `nested_loop` → **two** `UnifiedOp` rows: first for the outer table, second for the inner table, matching the join order in the JSON array.

## 4. Struct Definitions
Aligned with **`UnifiedExplainPlan`** / **`UnifiedOp`** in `cmd/mysql-mcp-server/types.go`:

```go
type UnifiedExplainPlan struct {
	QueryCost  float64     `json:"query_cost,omitempty"`
	Operations []UnifiedOp `json:"operations,omitempty"`
}

type UnifiedOp struct {
	TableName         string     `json:"table_name,omitempty"`
	AccessType        string     `json:"access_type,omitempty"`
	Key               string     `json:"key,omitempty"`
	KeyLength         string     `json:"key_length,omitempty"`
	RowsExamined      int64      `json:"rows_examined,omitempty"`
	Filtered          float64    `json:"filtered,omitempty"`
	Message           string     `json:"message,omitempty"`
	AttachedCondition string     `json:"attached_condition,omitempty"`
	PossibleKeys      []string   `json:"possible_keys,omitempty"`
	CostInfo          OpCostInfo `json:"cost_info,omitempty"`
}

type OpCostInfo struct {
	ReadCost        float64 `json:"read_cost,omitempty"`
	EvalCost        float64 `json:"eval_cost,omitempty"`
	PrefixCost      float64 `json:"prefix_cost,omitempty"`
	DataReadPerJoin string  `json:"data_read_per_join,omitempty"`
}
```

Per-table fields are filled from each engine’s **`table`** object (see mapping below). **`message`** may be populated from **`message`** and/or **`Extra`** in the JSON.

## 5. Implementation Details
* **cmd/mysql-mcp-server/types.go**: Defines **`UnifiedExplainPlan`**, **`UnifiedOp`**, **`OpCostInfo`**.
* **cmd/mysql-mcp-server/tools_extended.go**:
  * **`toolExplainQuery`**: default **`format`** is **`json`** when unset; **`traditional`** selects tabular **`EXPLAIN`**.
  * **`EXPLAIN FORMAT=JSON`** for the JSON path.
  * Parse the JSON string (single row/column from the server).
  * **`mapRawExplainToUnified`** maps engine JSON under **`query_block`** into **`UnifiedExplainPlan`** (see §6).
  * Return **`ExplainQueryOutput`** with **`Plan`** set to the unified struct or raw JSON on parse failure.

## 6. MySQL vs MariaDB JSON: nesting shapes and mapping

MySQL and MariaDB both expose a top-level **`query_block`**, but they differ in **cost placement**, **join nesting**, and **MariaDB-specific** join nodes (e.g. **`block-nl-join`**). The following examples are **representative** (field sets may vary by version).

### 6.1 Example: MySQL — `cost_info` + `nested_loop` with `table` objects

```json
{
  "query_block": {
    "select_id": 1,
    "cost_info": { "query_cost": "49827.84" },
    "nested_loop": [
      {
        "table": {
          "table_name": "orders",
          "access_type": "ALL",
          "possible_keys": ["fk_item_id"],
          "rows_examined_per_scan": 229432,
          "filtered": 100.0
        }
      },
      {
        "table": {
          "table_name": "items",
          "access_type": "ref",
          "key": "PRIMARY",
          "rows_examined_per_scan": 1,
          "filtered": 100.0
        }
      }
    ]
  }
}
```
*Annotation:* MySQL often puts total cost in **`query_block.cost_info.query_cost`** (string). Each join step is **`nested_loop[]` → `table`**.

### 6.2 Example: MariaDB — block-level `cost`, single `table`, numeric `filtered`

```json
{
  "query_block": {
    "select_id": 1,
    "cost": 10.5,
    "table": {
      "table_name": "t1",
      "access_type": "ALL",
      "rows": 1000,
      "filtered": 100,
      "attached_condition": "(t1.col1 = 1)"
    }
  }
}
```
*Annotation:* MariaDB may expose **`query_block.cost`** (number) instead of **`cost_info.query_cost`**. A simple plan uses **`query_block.table`** directly (not always **`nested_loop`**).

### 6.3 Example: MariaDB — `nested_loop` with `block-nl-join` wrapper

```json
{
  "query_block": {
    "select_id": 1,
    "nested_loop": [
      {
        "table": {
          "table_name": "a",
          "access_type": "index",
          "rows": 100,
          "filtered": 100
        }
      },
      {
        "block-nl-join": {
          "table": {
            "table_name": "b",
            "access_type": "ALL",
            "rows": 200,
            "filtered": 50.0
          },
          "join_type": "BNL",
          "attached_condition": "..."
        }
      }
    ]
  }
}
```
*Annotation:* MariaDB may wrap a join step in **`block-nl-join`**; the **`table`** map is then under **`block-nl-join.table`**, not a sibling **`table`** key on the same object.

### 6.4 Mapping table → `UnifiedExplainPlan` / `UnifiedOp`

| Target field | MySQL source path(s) | MariaDB source path(s) |
|--------------|----------------------|-------------------------|
| **`UnifiedExplainPlan.QueryCost`** | **`query_block.cost_info.query_cost`** (parse string to float) | Prefer **`query_block.cost_info.query_cost`** if present; else **`query_block.cost`** (numeric) |
| **`UnifiedExplainPlan.Operations`** (ordered **`UnifiedOp`**) | Collect from **`query_block.table`** (one object → one op), or **`query_block.table`** as array, or **`query_block.nested_loop[*].table`** in array order | Same, plus **`query_block.nested_loop[*].block-nl-join.table`** where **`table`** is not a direct child |
| **`UnifiedOp.TableName`** | **`table.table_name`** | **`table.table_name`** |
| **`UnifiedOp.AccessType`** | **`table.access_type`** | **`table.access_type`** |
| **`UnifiedOp.RowsExamined`** | **`table.rows_examined_per_scan`** or **`table.rows`** | **`table.rows`** or **`table.rows_examined_per_scan`** (whichever present) |
| **`UnifiedOp.Filtered`** | **`table.filtered`** (JSON number) | **`table.filtered`** (JSON number) |
| **`UnifiedOp.Key`**, **`KeyLength`**, **`PossibleKeys`** | **`table.key`**, **`table.key_length`**, **`table.possible_keys`** | Same keys on **`table`** |
| **`UnifiedOp.AttachedCondition`** | **`table.attached_condition`** | **`table.attached_condition`** (may also appear on **`block-nl-join`**) |
| **`UnifiedOp.Message`** | **`table.message`** and/or **`table.Extra`** | **`table.message`** / **`Extra`** when present |
| **`UnifiedOp.CostInfo`** | **`table.cost_info`** (**`read_cost`**, **`eval_cost`**, **`prefix_cost`**, **`data_read_per_join`**) | Same when present on **`table`** |

### 6.5 Fallback rules (deterministic)

1. **Query-level cost:** Use **`query_block.cost_info.query_cost`** when present; else **`query_block.cost`** (MariaDB); else leave **`QueryCost` unset** (zero / omit in JSON).
2. **Operations list:** Prefer **`query_block.table`** (scalar or array); else walk **`query_block.nested_loop`** in order. For each element: if **`table`** exists, **`extractUnifiedOp(table)`**; if **`block-nl-join`** exists, use **`block-nl-join.table`** (and optionally merge **`attached_condition`** from the wrapper if **`table`** lacks it).
3. **Per-op rows:** Prefer **`rows_examined_per_scan`**, then **`rows`**.
4. **Per-op filtered:** Accept JSON numbers (and coercions per implementation); if missing, leave **`Filtered`** zero.
5. **Fields present on one engine only:** Map when the path exists; otherwise omit / zero. Do not invent values; optional analyzer-specific fields (e.g. MariaDB-only **`using_index`**) may be ignored unless later added to **`UnifiedOp`**.

## 7. Backwards Compatibility
**`ExplainQueryInput`** retains **`format`**.

- **`format` omitted** → treated as **`json`** (structured **`UnifiedExplainPlan`** when parsing succeeds).
- **`format: "traditional"`** → legacy **`EXPLAIN`** tabular rows and warning analysis, unchanged.
- Explicit **`json`** → same as the default JSON path.
