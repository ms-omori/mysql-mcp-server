# Design Specification: Issue #104 - Richer EXPLAIN as Structured Output

## 1. Overview
The goal of this feature is to upgrade the `explain_query` tool to provide a strongly typed, unified JSON schema for query execution plans. This ensures that the LLM receives predictable, structured data for query analysis, regardless of whether the backend engine is MySQL (8.x, 9.x) or MariaDB (10.x, 11.x).

## 2. Approach: Strongly Typed Unified Schema
We will implement a normalization layer that executes `EXPLAIN FORMAT=JSON`, unmarshals the engine-specific JSON, and maps it to a unified Go struct. The LLM will perform all analysis on the unified JSON output; **auto-generated warnings are only provided for the legacy `traditional` format path** (see §3 and §5).

## 3. Data Flow
1. The user requests an `explain_query` via MCP.
2. **Format and SQL sent to the server**
   - If the `format` field is **omitted or empty**, the server treats it as **`"json"`**: it runs **`EXPLAIN FORMAT=JSON <query>`** and returns structured output (see steps 4–6).
   - If **`format` is explicitly `"traditional"`**, the server runs a plain **`EXPLAIN <query>`** (no `FORMAT=JSON`) and returns the **previous raw tabular plan** as an array of row maps (same behavior as before structured JSON). Optimization **warnings** are attached for this path only when `format` is `"traditional"` (consistent with §2: no auto-generated warnings for the default JSON/unified path).
   - Other explicit values (e.g. **`"tree"`**) follow the non-JSON path: `EXPLAIN` is run with the chosen format and rows are returned as scanned column maps (not the unified struct).
3. When the effective format is **`json`**, the server executes **`EXPLAIN FORMAT=JSON <query>`**.
4. The server unmarshals the raw JSON string returned by the database (single row/column).
5. A mapping function traverses the raw JSON and populates the **`UnifiedExplainPlan`** struct.
6. The structured **`ExplainQueryOutput`** is returned to the client (see §5 for error and **`Plan`** typing).

### 3.1 `Operations []UnifiedOp`: representation of nesting
The unified schema uses a **flat list**, not a tree of nested `UnifiedOp` values.

- **`UnifiedExplainPlan.Operations`** is an **ordered list** of **`UnifiedOp`** entries in **execution order** as produced by a depth-first walk of the engine JSON: first the driving table access, then each joined table / nested-loop step in order.
- **`UnifiedOp` does not** carry **`SubOperations`**, **`Children`**, or parent pointers. Hierarchical relationships in the original plan (e.g. `nested_loop` arrays, subqueries, derived tables) are **flattened** into this sequence; the consumer infers structure only by position and by repeating patterns in the raw JSON if needed.
- **Encoding summary:** subqueries / derived tables / multi-level nests that appear as nested `query_block` or deep `nested_loop` trees in MySQL or MariaDB JSON are mapped by **emitting one `UnifiedOp` per leaf `table` object** encountered in that walk, in order. The spec does not require a separate field for subtree roots.

**Example (conceptual):** two-table join with `nested_loop` → **two** `UnifiedOp` rows: first for the outer table, second for the inner table, matching the join order in the JSON array.

### 3.2 Flattening complex queries into `Operations` (illustrative)
The engine’s JSON is richer than a single sequence of table scans; the **unified** model still stores a **single ordered `[]UnifiedOp`**. Below, “JSON sketch” is conceptual—real plans vary by version and optimizer.

**(1) WHERE with subquery** — e.g. `SELECT * FROM orders o WHERE o.user_id = (SELECT id FROM users WHERE email = ?)`

- **JSON sketch:** outer `query_block` may list a `table` for `orders`, and a nested `query_block` (or `materialized_from_subquery` / `subqueries` region, depending on engine) for the `users` lookup. Each resolvable **`table`** leaf in depth-first order becomes one **`UnifiedOp`**.
- **Resulting `Operations` (order):** `[ UnifiedOp{TableName: "orders", …}, UnifiedOp{TableName: "users", …} ]` — outer driver first, then inner/subquery table access as emitted by the walk.

**(2) Derived table in FROM** — e.g. `SELECT * FROM (SELECT id FROM t1) AS d JOIN t2 ON …`

- **JSON sketch:** optimizer may show `nested_loop` with first step scanning `t1` (inside derived `d`) and second step `t2`, or a temporary/materialization node followed by joins; every **`table`** object visited in order yields one **`UnifiedOp`**.
- **Resulting `Operations` (order):** `[ UnifiedOp{TableName: "t1" …}, UnifiedOp{TableName: "t2" …} ]` (names illustrative; derived names may appear as `<subqueryN>` in some engines).

**(3) Materialized subquery / temp** — e.g. `WHERE x IN (SELECT …)` with materialization

- **JSON sketch:** plan may include explicit materialization or block-nl-join nodes; still, each **`table`** leaf under the walked structure becomes **`UnifiedOp`** in visit order.
- **Resulting `Operations` (order):** ordered list of **`UnifiedOp`** entries matching the sequence of **`table`** objects—materialization does not add a separate unified field; it may appear only in raw JSON or **`message`**.

Readers use **order** and **`table_name` / `attached_condition`** to infer correlation; deeper graph structure is not serialized in **`UnifiedExplainPlan`**.

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

### 4.1 `ExplainQueryOutput` (current implementation)
The MCP tool returns **`ExplainQueryOutput`** with:

- **`Plan`** — **`interface{}`** (JSON-polymorphic), not separate top-level `unified_plan` / `raw_json` fields. Callers distinguish shapes by type:
  - **`format` is `json` (default):** **`Plan`** is typically **`UnifiedExplainPlan`** when normalization succeeds; if normalization fails but the server returned a JSON string, **`Plan`** is that **raw JSON string** (fallback).
  - **`format` is `traditional`:** **`Plan`** is **`[]map[string]interface{}`** (tabular rows).
  - **Other formats (e.g. tree):** **`Plan`** is **`[]map[string]interface{}`** from column scans.
- **`Warnings`** — populated **only** for **`format=traditional`** (optimization hints from tabular analysis); omitted or empty for JSON/unified output.

Versioning of the unified shape is implied by the **`UnifiedExplainPlan`** struct and JSON tags; there is no separate **`format: "v1"`** field on the output today.

Per-table fields are filled from each engine’s **`table`** object (see mapping below). **`message`** may be populated from **`message`** and/or **`Extra`** in the JSON.

## 5. Error handling (as implemented)
This section matches **`toolExplainQuery`** and **`mapRawExplainToUnified`** in **`tools_extended.go`** (not an aspirational alternate API).

- **EXPLAIN fails** (syntax, permission, connection, empty result set for JSON, etc.): the handler returns a **non-nil Go `error`** and does **not** return a successful structured result. **`ExplainQueryOutput`** is the zero value on those paths. SQL/driver details are wrapped in the error (callers inspect **`err`**; MySQL error codes propagate via the driver).
- **`EXPLAIN FORMAT=JSON` succeeds** and returns a JSON string: the server unmarshals it once. **`mapRawExplainToUnified(rawJSON string) (UnifiedExplainPlan, error)`**:
  - If **JSON unmarshaling** of the plan string fails → returns **`error`**; **`toolExplainQuery`** then sets **`Plan`** to the **raw string** so the client still receives the payload.
  - If unmarshaling succeeds but **normalization fails** because a **`filtered`** field violates §7.6 rule 4 → **`toolExplainQuery`** returns a **non-nil error** (descriptive message); no **`Plan`** payload on success path.
  - If unmarshaling succeeds and mapping completes, produces a **`UnifiedExplainPlan`** (possibly empty **`Operations`** if the document lacks expected keys). There is **no** separate **`PartialOpErrors`** slice or per-op error list in the current signature.
- **Callers should check the Go `error` return before using `Plan`.** When **`err == nil`** and **`format=json`**, use a **type switch** on **`Plan`**: **`UnifiedExplainPlan`** vs **`string`** (raw fallback only when the top-level JSON document could not be unmarshaled).

## 6. Implementation Details
* **cmd/mysql-mcp-server/types.go**: Defines **`UnifiedExplainPlan`**, **`UnifiedOp`**, **`OpCostInfo`**, **`ExplainQueryOutput`**.
* **cmd/mysql-mcp-server/tools_extended.go**:
  * **`toolExplainQuery`**: default **`format`** is **`json`** when unset; **`traditional`** selects tabular **`EXPLAIN`**.
  * **`EXPLAIN FORMAT=JSON`** for the JSON path; errors on no rows / scan / **`rows.Err()`**.
  * Parse the JSON string (single row/column from the server).
  * **`mapRawExplainToUnified`** maps engine JSON under **`query_block`** into **`UnifiedExplainPlan`** (see §7).
  * Return **`ExplainQueryOutput`** with **`Plan`** set to **`UnifiedExplainPlan`**, or raw JSON string only when the plan string is not valid top-level JSON; invalid per-field values such as **`filtered`** (§7.6) surface as **`error`** from **`toolExplainQuery`**.

## 7. MySQL vs MariaDB JSON: nesting shapes and mapping

MySQL and MariaDB both expose a top-level **`query_block`**, but they differ in **cost placement**, **join nesting**, and **MariaDB-specific** join nodes (e.g. **`block-nl-join`**). The following examples are **representative** (field sets may vary by version).

### 7.1 Example: MySQL — `cost_info` + `nested_loop` with `table` objects

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

### 7.2 Example: MariaDB — block-level `cost`, single `table`, numeric `filtered`

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

### 7.3 Example: MariaDB — `nested_loop` with `block-nl-join` wrapper

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

### 7.4 Mapping table → `UnifiedExplainPlan` / `UnifiedOp`

| Target field | MySQL source path(s) | MariaDB source path(s) |
|--------------|----------------------|-------------------------|
| **`UnifiedExplainPlan.QueryCost`** | **`query_block.cost_info.query_cost`** (parse string to float) | Prefer **`query_block.cost_info.query_cost`** if present; else **`query_block.cost`** (numeric) |
| **`UnifiedExplainPlan.Operations`** (ordered **`UnifiedOp`**) | Collect from **`query_block.table`** (one object → one op), or **`query_block.table`** as array, or **`query_block.nested_loop[*].table`** in array order | Same, plus **`query_block.nested_loop[*].block-nl-join.table`** (see §7.5) |
| **`UnifiedOp.TableName`** | **`table.table_name`** | **`table.table_name`** |
| **`UnifiedOp.AccessType`** | **`table.access_type`** | **`table.access_type`** |
| **`UnifiedOp.RowsExamined`** | **`table.rows_examined_per_scan`** or **`table.rows`** | **`table.rows`** or **`table.rows_examined_per_scan`** (whichever present) |
| **`UnifiedOp.Filtered`** | **`table.filtered`** (see §7.6 rule 4) | Same |
| **`UnifiedOp.Key`**, **`KeyLength`**, **`PossibleKeys`** | **`table.key`**, **`table.key_length`**, **`table.possible_keys`** | Same keys on **`table`** |
| **`UnifiedOp.AttachedCondition`** | **`table.attached_condition`** | **`table.attached_condition`**, with **wrapper merge** for **`block-nl-join`** (§7.5) |
| **`UnifiedOp.Message`** | **`table.message`** and/or **`table.Extra`** | **`table.message`** / **`Extra`** when present |
| **`UnifiedOp.CostInfo`** | **`table.cost_info`** (**`read_cost`**, **`eval_cost`**, **`prefix_cost`**, **`data_read_per_join`**) | Same when present on **`table`** |

### 7.5 `block-nl-join` and `attached_condition` (deterministic)
When walking **`query_block.nested_loop`** (or when resolving a step that contains **`block-nl-join`**):

1. Obtain the inner table map from **`block-nl-join.table`**.
2. **MUST** merge **`block-nl-join.attached_condition`** into that table map **if and only if** the table object **does not** already define **`attached_condition`** (non-empty key wins on the **`table`** object).
3. Pass the resulting map to **`extractUnifiedOp`**. This is **not** optional: implementers must apply this merge whenever **`block-nl-join`** is used so **`UnifiedOp.AttachedCondition`** reflects the join predicate when the engine places it only on the wrapper.

If a nested-loop element exposes **`table`** directly (not **`block-nl-join`**), use **`table`** as-is (no merge).

### 7.6 Fallback rules (deterministic)

1. **Query-level cost:** Use **`query_block.cost_info.query_cost`** when present; else **`query_block.cost`** (MariaDB); else leave **`QueryCost` unset** (zero / omit in JSON).
2. **Operations list:** Prefer **`query_block.table`** (scalar or array); else walk **`query_block.nested_loop`** in order. For each element: if **`table`** exists, **`extractUnifiedOp(table)`**; if **`block-nl-join`** exists, build the table map per §7.5 then **`extractUnifiedOp`**.
3. **Per-op rows:** Prefer **`rows_examined_per_scan`**, then **`rows`**.
4. **Per-op filtered (`UnifiedOp.Filtered`):** The value at **`table["filtered"]`** (when the key is present) MUST be interpreted as follows:
   - **Allowed values:** JSON numbers (e.g. `100`, `100.0` as **`float64`** after `encoding/json` unmarshaling into **`map[string]interface{}`**), integer-like JSON numbers coerced via the same numeric path as other EXPLAIN fields, **`json.Number`** (when **`Decoder.UseNumber()`** is used), and **numeric strings** whose content is a valid JSON number literal after trim (e.g. **`"100"`** → **100.0**, **`"99.5"`** → **99.5**) using the same rules as **`strconv.ParseFloat`** on that string (standard JSON number parse).
   - **Rejected:** Any other dynamic type (e.g. **`bool`**, **`[]interface{}`**, arbitrary non-numeric strings such as **`"high"`**). Implementations MUST NOT silently ignore a present but invalid **`filtered`** value.
   - **Absent or null:** If **`filtered`** is **missing** or the value is JSON **`null`**, **`Filtered`** remains **0** (zero).
   - **Failure semantics:** If **`filtered`** is present and not **`null`**, and the value is not in the allowed set above, or a numeric string fails to parse, the implementation MUST treat this as a **protocol / normalization error**: fail the **`explain_query`** request and return a **descriptive error** to the caller (do not return a partially normalized plan and do not substitute zero without error). This matches rejecting invalid EXPLAIN JSON for the **`filtered`** field specifically.
5. **Fields present on one engine only:** Map when the path exists; otherwise omit / zero. Do not invent values; optional analyzer-specific fields (e.g. MariaDB-only **`using_index`**) may be ignored unless later added to **`UnifiedOp`**.

## 8. Backwards Compatibility
**`ExplainQueryInput`** retains **`format`**.

- **`format` omitted** → treated as **`json`** (structured **`UnifiedExplainPlan`** when parsing succeeds).
- **`format: "traditional"`** → legacy **`EXPLAIN`** tabular rows and warning analysis, unchanged.
- Explicit **`json`** → same as the default JSON path.
