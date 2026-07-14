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

// TraceUnitRow is one unit's verdict within a decision trace.
type TraceUnitRow struct {
	UnitID     string
	Path       string
	Outcome    string // "selected" | "skipped"
	RejectedBy string // gate that rejected it ("" for selected)
	Score      float64
	GatesJSON  string // full cascade, for the detail view
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
			        COALESCE(score,0), COALESCE(gates_json,'[]')
			   FROM trace_units WHERE trace_id = ? ORDER BY id ASC`, out[i].ID)
		if err != nil {
			return nil, err
		}
		for urows.Next() {
			var u TraceUnitRow
			if err := urows.Scan(&u.UnitID, &u.Path, &u.Outcome, &u.RejectedBy, &u.Score, &u.GatesJSON); err != nil {
				urows.Close()
				return nil, err
			}
			out[i].Units = append(out[i].Units, u)
		}
		urows.Close()
	}
	return out, nil
}
