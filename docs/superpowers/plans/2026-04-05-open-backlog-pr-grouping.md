# Open backlog — PR grouping and “next PR” recommendation

> **For agentic workers:** This is a **planning** document (issue grouping + sequencing), not an implementation plan with checkboxes. For implementing a chosen slice, spawn a follow-up plan with `writing-plans` / `subagent-driven-development` per **AGENTS.md** and **workflow.md**.

**Goal:** Turn the six open GitHub issues into **reviewable PR boundaries** aligned with **workflow.md** (“one cohesive PR when issues share one implementation; separate PRs when independent”).

**Sources:** Parallel **explore** subagents (independent codebase maps) + `gh issue view` bodies + **AGENTS.md** / **workflow.md**.

**Tech stack:** Go 1.24+, existing `cmd/mysql-mcp-server`, `internal/config`, `internal/util`, extended tools in `tools_extended.go`.

---

## Subagent synthesis (high level)

| Issue | Theme | Overlap with others |
|-------|--------|---------------------|
| **#104** | `explain_query` structured output + docs | Isolated to extended tools + README; **no** dependency on writes or connections |
| **#103** | `write_query` + confirmation + audit | Core SQL path, validators, security tests; **different** subsystem from #106 |
| **#106** | `add_connection` / HTTP persistence | `ConnectionManager`, optional HTTP; **different** security story from #103 |
| **#24** | TiDB matrix + compatibility | DB-side; may need code fixes beyond Compose — **not** “CI only” until proven |
| **#64** | Local LLMs (doc vs built-in client) | Overlaps **#80** only if #80 uses **server-side** LLM calls |
| **#80** | `ask_nl_sql` | Can ship as **client-LLM** (schema/prompt bundle) **without** #64; server-side NL→SQL wants shared LLM config → pair with #64 |

---

## Recommended PR groups (do not merge unrelated groups)

### PR-A — **Next PR (recommended): issue #104 only**

- **Why next:** Smallest blast radius, clear ownership (`tools_extended.go`, `types.go`, tests, README), satisfies “document + deepen `explain_query`” without touching read-only guarantees or connection registry.
- **Scope:** Wire `ExplainQueryInput.Format` (today unused), optional `EXPLAIN FORMAT=JSON` / `TREE`, normalize structured output; expand MCP description and README vs `run_query` + `EXPLAIN`.
- **Verify:** `go test ./...`, extended-mode tests in `tools_extended_test.go`; CI parity per **workflow.md**.

### PR-B — issue #106 only (`add_connection`)

- Runtime DSN registration, duplicate-name errors, masking, optional `POST /api/connections`, optional persistence flag.
- **Do not bundle with #103** — different review focus (connection trust boundary vs mutation trust boundary).

### PR-C — issue #103 only (`write_query`)

- New gated tool, DML-only validation, confirmation semantics, audit entries, disabled by default.
- **Heavy security / integration tests**; land after or before #106 on a clean `main`, but **not in the same PR** as #106.

### PR-D — issue #24 only (TiDB)

- Compose service + CI matrix **plus** any compatibility fixes discovered; keep LLM work out of this PR.

### PR-E — issues #64 + #80 **only if** product chooses **server-side** NL→SQL

- Single config surface (OpenAI-compatible URL, model, timeouts) and one doc pass.
- **Alternative:** **#80-first (client-LLM mode):** tool returns grounded prompt / suggested SQL for the **host** MCP client — then **#64** can be README-only in a **separate small PR**.

### PR-F — issue #64 alone (documentation track)

- If #80 stays client-driven, land **#64** as docs/examples only without blocking #80.

---

## Parallel agent rules (for implementation phase)

Per **AGENTS.md** / **workflow.md** / **dispatching-parallel-agents**:

- **Parallel:** e.g. README edits vs test-only fixes vs unrelated packages — **after** PR scope is frozen.
- **Sequential:** anything sharing one security design (#103 confirmation + validator + audit) should stay **one agent or one coordinated series**, not split across parallel agents.

---

## Suggested branch names (examples)

- `feature/issue-104-explain-structured` → **PR-A**
- `feature/issue-106-add-connection` → **PR-B**
- `feature/issue-103-write-query` → **PR-C**
- `feature/issue-24-tidb-ci` → **PR-D**
- `feature/issues-64-80-llm-nl-sql` → **PR-E** (only if combined scope agreed)

---

## Execution handoff

**Default “next PR”:** open **PR-A (#104)** from a fresh branch off `main`, run **CI parity** from **workflow.md**, then open the GitHub PR linking `Closes #104` (or partial wording if you split docs vs code).

If you want **PR-B** or **PR-C** first instead, treat this document as **ordering guidance** only and update the issue links in the PR description accordingly.
