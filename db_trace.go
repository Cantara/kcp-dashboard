package main

import (
	"database/sql"
	"encoding/json"
)

// GateStat is one entry of a trace's gate_summary: how many units passed/failed
// a given gate in the cascade.
type GateStat struct {
	Gate   string `json:"gate"`
	Passed int64  `json:"passed"`
	Failed int64  `json:"failed"`
}

// DenyScope is the explicit negative scope on a unit's action_scope
// (§4.3a, v0.31, RFC-0029). Same {tools, paths, capabilities} shape as the
// allowlist, but every entry is a PROHIBITION: a token listed here is refused
// even when the allowlist grants it. Deny overrides allow, fail-closed. The
// dashboard surfaces it so an operator sees prohibitions, not just allows.
type DenyScope struct {
	Tools        []string `json:"tools,omitempty"`        // tool names the procedure MUST NOT invoke
	Paths        []string `json:"paths,omitempty"`        // paths (globs permitted) the procedure MUST NOT touch
	Capabilities []string `json:"capabilities,omitempty"` // capabilities the procedure MUST NOT exercise
}

// nonEmpty reports whether the deny scope prohibits anything. An empty deny is a
// no-op (§4.3a) and is not surfaced.
func (d *DenyScope) nonEmpty() bool {
	if d == nil {
		return false
	}
	return len(d.Tools) > 0 || len(d.Paths) > 0 || len(d.Capabilities) > 0
}

// TraceUnitRow is one unit's verdict within a decision trace.
type TraceUnitRow struct {
	UnitID     string
	Path       string
	Outcome    string // "selected" | "skipped"
	RejectedBy string // gate that rejected it ("" for selected)
	Score      float64
	GatesJSON  string     // full cascade, for the detail view
	Deny       *DenyScope // §4.3a (RFC-0029) — prohibitions the unit carries; nil when none
}

// DecisionTraceRow is one governance decision: a task planned against a
// manifest, with the gate-cascade outcome per candidate unit.
type DecisionTraceRow struct {
	ID            int64
	SessionID     string
	TS            string
	Project       string
	Manifest      string
	Task          string
	AsOf          string
	SelectedCount int64
	SkippedCount  int64
	GateSummary   []GateStat
	Units         []TraceUnitRow
}

// ProhibitedAttemptRow is one distinct deny-hit within a session (§4.3b, v0.32,
// RFC-0030), aggregated across repeats: Count > 1 means the same prohibition
// was attempted repeatedly — the governance signal the event type exists for
// (misconfiguration, compromise, or probing). BindingSource names which deny
// matched: the playbook's blanket deny, the used skill's deny, or both.
type ProhibitedAttemptRow struct {
	Playbook      string // playbook unit id ("" when the deny is skill-only)
	Step          string // step id within the playbook
	Token         string // the denied token (tool name, path, capability)
	Dimension     string // "tools" | "paths" | "capabilities"
	BindingSource string // "playbook" | "skill" | "both"
	Count         int64  // attempts against this deny (retransmits deduplicated)
	LastTS        string // timestamp of the most recent attempt
}

func tableExists(db *sql.DB, name string) bool {
	var got string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got)
	return err == nil
}

// loadSessionTraces returns the decision traces for a session, newest first,
// each with its per-unit verdicts. Absent trace tables → empty, nil error.
func loadSessionTraces(usageDBPath, sessionID string) ([]DecisionTraceRow, error) {
	db, err := sql.Open("sqlite", "file:"+usageDBPath+"?mode=ro&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if !tableExists(db, "decision_traces") {
		return nil, nil
	}

	rows, err := db.Query(
		`SELECT id, session_id, ts, COALESCE(project,''), COALESCE(manifest,''),
		        COALESCE(task,''), COALESCE(as_of,''), COALESCE(selected_count,0),
		        COALESCE(skipped_count,0), COALESCE(gate_summary_json,'[]')
		   FROM decision_traces
		  WHERE session_id = ?
		  ORDER BY ts DESC, id DESC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DecisionTraceRow
	for rows.Next() {
		var t DecisionTraceRow
		var gs string
		if err := rows.Scan(&t.ID, &t.SessionID, &t.TS, &t.Project, &t.Manifest,
			&t.Task, &t.AsOf, &t.SelectedCount, &t.SkippedCount, &gs); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(gs), &t.GateSummary)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if !tableExists(db, "trace_units") {
		return out, nil
	}
	for i := range out {
		urows, err := db.Query(
			`SELECT unit_id, COALESCE(path,''), outcome, COALESCE(rejected_by,''),
			        COALESCE(score,0), COALESCE(gates_json,'[]'), COALESCE(deny_json,'')
			   FROM trace_units WHERE trace_id = ? ORDER BY id ASC`, out[i].ID)
		if err != nil {
			return nil, err
		}
		for urows.Next() {
			var u TraceUnitRow
			var denyJSON string
			if err := urows.Scan(&u.UnitID, &u.Path, &u.Outcome, &u.RejectedBy, &u.Score, &u.GatesJSON, &denyJSON); err != nil {
				urows.Close()
				return nil, err
			}
			if denyJSON != "" {
				var d DenyScope
				if json.Unmarshal([]byte(denyJSON), &d) == nil && d.nonEmpty() {
					u.Deny = &d
				}
			}
			out[i].Units = append(out[i].Units, u)
		}
		urows.Close()
	}
	return out, nil
}

// loadSessionProhibitedAttempts returns the session's deny-hits grouped per
// (playbook, step, token, dimension, binding source), most-attempted first —
// repeated attempts against the same deny surface as one row with a count.
// Absent table → empty, nil error.
func loadSessionProhibitedAttempts(usageDBPath, sessionID string) ([]ProhibitedAttemptRow, error) {
	db, err := sql.Open("sqlite", "file:"+usageDBPath+"?mode=ro&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if !tableExists(db, "prohibited_attempts") {
		return nil, nil
	}

	rows, err := db.Query(
		`SELECT COALESCE(playbook,''), COALESCE(step,''), token, dimension,
		        binding_source, COUNT(*), MAX(ts)
		   FROM prohibited_attempts
		  WHERE session_id = ?
		  GROUP BY playbook, step, token, dimension, binding_source
		  ORDER BY COUNT(*) DESC, MAX(ts) DESC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ProhibitedAttemptRow
	for rows.Next() {
		var pa ProhibitedAttemptRow
		if err := rows.Scan(&pa.Playbook, &pa.Step, &pa.Token, &pa.Dimension,
			&pa.BindingSource, &pa.Count, &pa.LastTS); err != nil {
			return nil, err
		}
		out = append(out, pa)
	}
	return out, rows.Err()
}
