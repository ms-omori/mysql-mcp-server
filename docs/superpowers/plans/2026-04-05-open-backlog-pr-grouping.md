# Open backlog — PR grouping and “next PR” recommendation

> **For agentic workers:** This is a **planning** document (issue grouping + sequencing), not an implementation plan with checkboxes. For implementing a chosen slice, spawn a follow-up plan with `writing-plans` / `subagent-driven-development` per **AGENTS.md** and **workflow.md**.

**Goal:** Define **reviewable PR boundaries** for the **in-scope** open issues (**#103, #104, #106**), aligned with **workflow.md** (“one cohesive PR when issues share one implementation; separate PRs when independent”).

**Open issues checklist (vs GitHub):** Every currently **open** issue is either in the table below (**#103–#106**) or listed as **out of scope** (**#24, #64, #80**). Nothing is missing. For a tabular snapshot that includes deferred items, see **[workflow.md](../../../workflow.md)** → *Open backlog snapshot*.

**Sources:** Parallel **explore** subagents (independent codebase maps) + `gh issue view` bodies + **AGENTS.md** / **workflow.md**.

**Tech stack:** Go 1.24+, existing `cmd/mysql-mcp-server`, `internal/config`, `internal/util`, extended tools in `tools_extended.go`.

**Out of scope for this document (revisit later):** **#24** (TiDB), **#64** / **#80** (local LLMs and `ask_nl_sql`, including any combined server-side NL→SQL track).

---

## Subagent synthesis (in-scope issues only)

| Issue | Theme | Overlap with others |
|-------|--------|---------------------|
| **#104** | `explain_query` structured output + docs | Isolated to extended tools + README; **no** dependency on writes or connections |
| **#103** | `write_query` + confirmation + audit | Core SQL path, validators, security tests; **different** subsystem from #106 |
| **#106** | `add_connection` / HTTP persistence | `ConnectionManager`, optional HTTP; **different** security story from #103 |

---

## Recommended PR groups (do not merge unrelated groups)

### PR-A — **Next PR (recommended): issue #104 only**

- **Why next:** Smallest blast radius, clear ownership (`tools_extended.go`, `types.go`, tests, README), satisfies “document + deepen `explain_query`” without touching read-only guarantees or connection registry.
- **Scope:** Wire `ExplainQueryInput.Format` (today unused), optional `EXPLAIN FORMAT=JSON` / `TREE`, normalize structured output; expand MCP description and README vs `run_query` + `EXPLAIN`. Confirm **MySQL version / syntax** support for chosen formats (e.g. JSON vs TREE) and document **fallback** to traditional EXPLAIN where unsupported.
- **Verify:** `go test ./...`, extended-mode tests in `tools_extended_test.go`, and **`http_test.go`** for **`/api/explain`** if HTTP behavior changes; CI parity per **workflow.md**.

### PR-B — issue #106 only (`add_connection`)

- Runtime DSN registration, duplicate-name errors, masking, optional `POST /api/connections`, optional persistence flag.
- **Do not bundle with #103** — different review focus (connection trust boundary vs mutation trust boundary).

### PR-C — issue #103 only (`write_query`)

- New gated tool, DML-only validation, confirmation semantics, audit entries, disabled by default.
- **Heavy security / integration tests**; land after or before #106 on a clean `main`, but **not in the same PR** as #106.

**PR-B + PR-C merge note:** Both will touch shared registration files (e.g. `main.go`, `types.go`, `tool_wrappers.go`). Land one, rebase the other—expect routine conflict resolution, not a reason to combine PRs.

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

---

## Execution handoff

**Default “next PR”:** open **PR-A (#104)** from a fresh branch off `main`, run **CI parity** from **workflow.md**, then open the GitHub PR. Use **`Closes #104`** only when the PR fully satisfies the issue; otherwise **`Refs #104`** (or split into a second PR for remaining work).

If you want **PR-B** or **PR-C** first instead, treat this document as **ordering guidance** only and update the issue links in the PR description accordingly.
