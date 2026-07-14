package main

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// traceEvent is the wire format kcp-harness POSTs to /trace — a compact,
// content-free projection of a kcp-agent DecisionTrace (see
// docs/thought-graph-phase2.md §4).
type traceEvent struct {
	Kind        string           `json:"kind"`
	SessionID   string           `json:"session_id"`
	TS          string           `json:"ts"`
	Project     string           `json:"project"`
	Manifest    string           `json:"manifest"`
	Task        string           `json:"task"`
	AsOf        string           `json:"as_of"`
	Selected    int              `json:"selected"`
	Skipped     int              `json:"skipped"`
	GateSummary json.RawMessage  `json:"gate_summary"`
	Units       []traceUnitInput `json:"units"`
}

type traceUnitInput struct {
	ID         string          `json:"id"`
	Path       string          `json:"path"`
	Outcome    string          `json:"outcome"`
	RejectedBy string          `json:"rejected_by"`
	Score      *float64        `json:"score"`
	Gates      json.RawMessage `json:"gates"`
}

// createTraceTables ensures the decision-trace tables exist. Called from
// newUsageWriter so the serve side owns the schema.
func createTraceTables(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS decision_traces (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id        TEXT NOT NULL,
		ts                TEXT NOT NULL,
		project           TEXT,
		manifest          TEXT,
		task              TEXT,
		as_of             TEXT,
		selected_count    INTEGER,
		skipped_count     INTEGER,
		gate_summary_json TEXT,
		ingested_at       TEXT NOT NULL,
		UNIQUE (session_id, ts, task)
	)`); err != nil {
		return err
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS trace_units (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		trace_id    INTEGER NOT NULL,
		unit_id     TEXT NOT NULL,
		path        TEXT,
		outcome     TEXT NOT NULL,
		rejected_by TEXT,
		score       REAL,
		gates_json  TEXT
	)`)
	return err
}

// writeTrace persists one decision trace + its unit rows. Idempotent per
// (session_id, ts, task): a duplicate is ignored, units are not re-inserted.
func (u *usageWriter) writeTrace(ev traceEvent) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	gs := ev.GateSummary
	if len(gs) == 0 {
		gs = json.RawMessage("[]")
	}
	res, err := u.db.Exec(
		`INSERT OR IGNORE INTO decision_traces
		 (session_id, ts, project, manifest, task, as_of, selected_count, skipped_count, gate_summary_json, ingested_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.SessionID, ev.TS, ev.Project, ev.Manifest, ev.Task, ev.AsOf,
		ev.Selected, ev.Skipped, string(gs), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // duplicate — already recorded
	}
	traceID, err := res.LastInsertId()
	if err != nil {
		return err
	}
	for _, un := range ev.Units {
		var score any
		if un.Score != nil {
			score = *un.Score
		}
		gates := un.Gates
		if len(gates) == 0 {
			gates = json.RawMessage("[]")
		}
		if _, err := u.db.Exec(
			`INSERT INTO trace_units (trace_id, unit_id, path, outcome, rejected_by, score, gates_json)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			traceID, un.ID, un.Path, un.Outcome, un.RejectedBy, score, string(gates)); err != nil {
			return err
		}
	}
	return nil
}

// handleTrace ingests a decision trace POSTed by kcp-harness. Fail-open on the
// emit side; here we simply 204 on success, 400 on bad input, 405 on wrong verb.
func handleTrace(w http.ResponseWriter, r *http.Request, usage *usageWriter) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	var ev traceEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if usage != nil {
		if err := usage.writeTrace(ev); err != nil {
			http.Error(w, "write error", http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
