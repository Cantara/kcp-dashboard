# Thought-graph Phase 2 — the decision layer (gate-cascade "why")

Status: **design / spec**
Epic: Cantara/kcp-dashboard#2
Cross-repo: Cantara/kcp-harness (emit side)

## 1. Motivation

Phase 1 shipped the **action layer**: press `s` in kcp-dashboard and browse a
session's ordered steps (tool · command · guidance ✓ · output). That answers
*"what did the agent do?"*

Phase 2 adds the **decision layer**: *"why did KCP feed the agent this
knowledge and not that?"* This is the part a generic hook-tail (or ktotto)
cannot show, because it has no deterministic decision to trace. kcp-harness
does: the kcp-agent planner runs a fixed **13-gate cascade** over every unit in
a manifest and records the verdict for each. That cascade **is** a faithful
thought-graph — deterministic and inspectable, not scraped chain-of-thought.

## 2. The faithful data (already computed, not yet persisted)

kcp-harness produces a `DecisionTrace` on demand via the `kcp_trace` tool
(`kcp-harness/src/kcp-bridge.ts` → `traceDecision(manifest, task, opts)`). Shape
(from `kcp-agent`):

```
DecisionTrace {
  task, taskTerms, asOf, capabilities,
  plan,                       // the canonical AgentPlan
  units: UnitTrace[],         // one per manifest unit, in manifest order
  gateSummary: { gate, passed, failed }[]
}

UnitTrace {
  id, path, intent,
  outcome: "selected" | "skipped",
  gates: GateVerdict[],       // in evaluation order; stops at first rejection
  rejectedBy?: GateName,      // undefined for selected units
  score?,                     // set once relevance passes
  tokens?, cost?
}

GateName = audience | not_for | temporal | deprecated | supersession
         | relevance | attestation | payment | access | strict
         | max_units | money_budget | context_budget
```

Crucially, the audit log **strips** this trace today
(`kcp-harness/src/audit.ts:241` — *"Omit full trace from audit (it's large);
the trace is available via kcp_trace"*). So Phase 2's emit side is about
**persisting a compact form** of what the governor already computes.

It carries **no content** — only unit ids, paths, gate names, verdicts, and
scores — so it is safe to ship the same way audit records are (sanitized).

## 3. Architecture

```
kcp-harness (governor)                 kcp-dashboard (serve.go :7734)
  │ auto-plan / kcp_trace                 │
  │ produces DecisionTrace                │
  ├── compact TraceEvent (JSON) ── POST ──►  /trace  ──► decision_traces
  │                                        │            trace_units
  │                                        │
  │                              db.go: loadSessionTraces(sessionID)
  │                                        │
  │                              session drill-down: "Decisions" pane
```

- **Emit (kcp-harness):** when the governor makes an auto-plan decision (or the
  agent calls `kcp_trace`), serialize a compact `TraceEvent` and POST it to the
  dashboard. Opt-in via config (`dashboard.url`), fail-open (never block
  governance if the dashboard is down). Mirrors the existing hook-sink pattern.
- **Ingest (kcp-dashboard):** a new `/trace` endpoint in `serve.go`, writing to
  two new tables. Same `usageWriter`/`~/.kcp` conventions.
- **Render (kcp-dashboard):** a **Decisions** view inside the existing session
  drill-down (keyed by `session_id`, so it slots next to Steps).

### Alternative considered: pull/tail of `audit.jsonl`

kcp-dashboard could tail kcp-harness's `audit.jsonl` instead of receiving a
POST. Rejected as the default because (a) kcp-dashboard already reads SQLite,
not JSONL; (b) the audit log deliberately omits the trace, so kcp-harness would
have to write it there anyway; (c) push keeps the two services decoupled and
matches the existing `/hook` sink. Tailing remains a fallback for
air-gapped/offline setups.

## 4. Wire format — `TraceEvent`

```json
{
  "kind": "decision_trace",
  "session_id": "618db3e9...",
  "ts": "2026-07-14T14:03:12Z",
  "project": "/Users/x/dev/app",
  "manifest": "kb://acme-eng",
  "task": "access docs/runbooks/oncall.md",
  "as_of": "2026-07-14",
  "selected": 3,
  "skipped": 12,
  "gate_summary": [
    { "gate": "relevance", "passed": 8, "failed": 7 },
    { "gate": "temporal",  "passed": 14, "failed": 1 }
  ],
  "units": [
    { "id": "oncall-runbook", "path": "docs/runbooks/oncall.md",
      "outcome": "selected", "score": 0.72,
      "gates": [ {"gate":"audience","verdict":"pass"}, {"gate":"relevance","verdict":"pass"} ] },
    { "id": "pci-scope", "path": "policies/pci-scope.md",
      "outcome": "skipped", "rejected_by": "temporal",
      "gates": [ {"gate":"audience","verdict":"pass"}, {"gate":"temporal","verdict":"fail","detail":"superseded 2d ago"} ] }
  ]
}
```

Direct 1:1 with `DecisionTrace`; `gates` is truncated at the first rejection
(as the planner already does).

## 5. Ingestion schema (kcp-dashboard)

Written to `~/.kcp/usage.db` (same DB `serve.go` already owns):

```sql
CREATE TABLE IF NOT EXISTS decision_traces (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id        TEXT NOT NULL,
  ts                TEXT NOT NULL,
  project           TEXT,
  manifest          TEXT,
  task              TEXT,
  as_of             TEXT,
  selected_count    INTEGER,
  skipped_count     INTEGER,
  gate_summary_json TEXT,           -- [{gate,passed,failed}]
  ingested_at       TEXT NOT NULL,
  UNIQUE (session_id, ts, task)
);

CREATE TABLE IF NOT EXISTS trace_units (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  trace_id    INTEGER NOT NULL,     -- decision_traces.id
  unit_id     TEXT NOT NULL,
  path        TEXT,
  outcome     TEXT NOT NULL,        -- selected | skipped
  rejected_by TEXT,                 -- gate name, or NULL for selected
  score       REAL,
  gates_json  TEXT                  -- full cascade for the detail view
);
```

`session_id` links a trace to the session already shown in the drill-down.

## 6. Render — a "Decisions" pane in session mode

Reuse the Phase 1 session drill-down. In session mode, a key (e.g. `g`) toggles
the right pane between **Steps** and **Decisions**:

- **Decisions list** — the traces for the selected session (`task`, time,
  `N selected / M skipped`).
- **Gate funnel** — for the selected trace, a bar per gate from
  `gate_summary_json` showing how many units each gate passed/failed — you see
  exactly where candidates drop out of the cascade.
- **Per-unit why** — selected units (with score) and skipped units annotated
  with `rejected_by` (e.g. *"pci-scope — skipped at `temporal`"*).

This is the graph: `task → candidate units → gate cascade → selected set`.

## 7. Phasing (TDD, one PR each — mirrors Phase 1)

- **PR 3 — kcp-harness: emit** *(cross-repo)*. Serialize the `TraceEvent` from
  the governor's `DecisionTrace`; opt-in `dashboard.url` POST; fail-open.
  Tracked as a kcp-harness issue.
- **PR 4 — kcp-dashboard: ingest**. `/trace` endpoint in `serve.go` + the two
  tables + `loadSessionTraces(dbPath, sessionID)`. Pure data + table tests,
  no UI. Degrade to empty when tables are absent (as `loadStats` does).
- **PR 5 — kcp-dashboard: render**. The Decisions pane + gate funnel in session
  mode. Pure render + Update-toggle tests.

## 8. Non-goals

- No chain-of-thought reconstruction — only the deterministic decision cascade.
- No content in the trace — ids, paths, gates, scores only.
- No blocking behavior — emit is fail-open; governance never waits on the
  dashboard.

## 9. Open questions

1. **Emit trigger granularity** — every auto-plan decision, or only explicit
   `kcp_trace` calls? (Proposal: auto-plan decisions, deduped per
   `session_id + task`, so the graph reflects real governance, not just manual
   traces.)
2. **Transport auth** — localhost-only like `/hook` today, or a shared token
   for remote dashboards? (Proposal: localhost default, token optional.)
3. **Retention** — traces can be frequent; do we cap/rotate `trace_units`?
   (Proposal: prune with the same window the dashboard already queries by.)
