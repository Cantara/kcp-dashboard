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
	// Deny is the unit's action_scope.deny — explicit prohibitions that override
	// the allowlist, fail-closed (§4.3a, v0.31, RFC-0029). Optional; nil when the
	// unit carries no negative scope.
	Deny *DenyScope `json:"deny"`
}

// prohibitedAttemptEvent is the wire format for a kind:"prohibited_attempt"
// event on /trace — a deny-hit (SPEC §17, v0.32, RFC-0030). A deny is never
// grantable: the action was refused, finally, and this event is the notify-only
// audit record of the attempt. Field names follow the normative §17 wire object
// (KCP v0.32.1); the canonical fixture is vendored at
// testdata/prohibited-attempt.json. BindingSource names which deny matched —
// the playbook's blanket deny, the used skill's deny, or both (union
// composition). Nullable §17 fields are pointers so present-but-null survives
// to the row as NULL, not "".
type prohibitedAttemptEvent struct {
	Kind           string  `json:"kind"`
	Timestamp      string  `json:"timestamp"`       // RFC 3339 UTC
	UnitID         string  `json:"unit_id"`         // the unit (skill) whose execution hit the deny
	PlaybookID     string  `json:"playbook_id"`     // playbook unit id ("" when the deny is skill-only)
	StepID         string  `json:"step_id"`         // step id within the playbook
	Dimension      string  `json:"dimension"`       // "tools" | "paths" | "capabilities"
	Token          string  `json:"token"`           // the attempted token (tool name, path, capability)
	MatchedPattern *string `json:"matched_pattern"` // deny entry that caught it — differs from token on glob hits
	BindingSource  string  `json:"binding_source"`  // "skill" | "playbook" | "both"
	AcknowledgedBy *string `json:"acknowledged_by"` // operator ack; null until acknowledged
	CorrelationID  *string `json:"correlation_id"`  // W3C traceparent; null when unavailable

	// Transport envelope kcp-harness adds around the §17 object. Not part of
	// the normative shape — absent from the conformance fixture — so all
	// optional.
	SessionID string `json:"session_id"`
	Project   string `json:"project"`
	Manifest  string `json:"manifest"`

	// Pre-§17 field names (the shape PR #45 invented before the schema
	// existed), accepted so older emitters keep ingesting during rollout.
	// The §17 name wins whenever both are present.
	LegacyTS       string `json:"ts"`
	LegacyPlaybook string `json:"playbook"`
	LegacyStep     string `json:"step"`
}

// normalize resolves the pre-§17 aliases into the §17 fields.
func (e *prohibitedAttemptEvent) normalize() {
	if e.Timestamp == "" {
		e.Timestamp = e.LegacyTS
	}
	if e.PlaybookID == "" {
		e.PlaybookID = e.LegacyPlaybook
	}
	if e.StepID == "" {
		e.StepID = e.LegacyStep
	}
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
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS trace_units (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		trace_id    INTEGER NOT NULL,
		unit_id     TEXT NOT NULL,
		path        TEXT,
		outcome     TEXT NOT NULL,
		rejected_by TEXT,
		score       REAL,
		gates_json  TEXT,
		deny_json   TEXT
	)`); err != nil {
		return err
	}
	// RFC-0029 (v0.31): add deny_json to trace_units created before the deny
	// scope existed. Idempotent — skipped once the column is present.
	if !columnExists(db, "trace_units", "deny_json") {
		if _, err := db.Exec(`ALTER TABLE trace_units ADD COLUMN deny_json TEXT`); err != nil {
			return err
		}
	}
	// RFC-0030 (v0.32): prohibited-attempt events, one row per attempt. The
	// UNIQUE constraint deduplicates retransmits of the same event; distinct
	// timestamps are distinct attempts, so repeats stay countable. Column names
	// predate SPEC §17 (ts/playbook/step were this dashboard's invention); the
	// §17 wire names map onto them on ingest so pre-§17 rows stay readable.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS prohibited_attempts (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id      TEXT NOT NULL,
		ts              TEXT NOT NULL,
		project         TEXT,
		manifest        TEXT,
		playbook        TEXT,
		step            TEXT,
		token           TEXT NOT NULL,
		dimension       TEXT NOT NULL,
		binding_source  TEXT NOT NULL,
		unit_id         TEXT,
		matched_pattern TEXT,
		acknowledged_by TEXT,
		correlation_id  TEXT,
		ingested_at     TEXT NOT NULL,
		UNIQUE (session_id, ts, playbook, step, token, dimension)
	)`); err != nil {
		return err
	}
	// SPEC §17 (v0.32.1): columns added after PR #45 shipped the table.
	// Idempotent — skipped once present; pre-§17 rows read back as NULL.
	for _, col := range []string{"unit_id", "matched_pattern", "acknowledged_by", "correlation_id"} {
		if !columnExists(db, "prohibited_attempts", col) {
			if _, err := db.Exec(`ALTER TABLE prohibited_attempts ADD COLUMN ` + col + ` TEXT`); err != nil {
				return err
			}
		}
	}
	return nil
}

// columnExists reports whether table has a column named col.
func columnExists(db *sql.DB, table, col string) bool {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false
		}
		if name == col {
			return true
		}
	}
	return false
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
		// Persist a non-empty deny scope only; an absent/empty deny is a no-op
		// (§4.3a) and stored as NULL so readers see "no prohibition".
		var denyJSON any
		if un.Deny.nonEmpty() {
			if b, err := json.Marshal(un.Deny); err == nil {
				denyJSON = string(b)
			}
		}
		if _, err := u.db.Exec(
			`INSERT INTO trace_units (trace_id, unit_id, path, outcome, rejected_by, score, gates_json, deny_json)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			traceID, un.ID, un.Path, un.Outcome, un.RejectedBy, score, string(gates), denyJSON); err != nil {
			return err
		}
	}
	return nil
}

// writeProhibitedAttempt persists one prohibited-attempt event. Idempotent per
// (session_id, ts, playbook, step, token, dimension): a retransmitted event is
// ignored; a later attempt against the same deny is a new row, so repeats count.
func (u *usageWriter) writeProhibitedAttempt(ev prohibitedAttemptEvent) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	ev.normalize()
	_, err := u.db.Exec(
		`INSERT OR IGNORE INTO prohibited_attempts
		 (session_id, ts, project, manifest, playbook, step, token, dimension, binding_source,
		  unit_id, matched_pattern, acknowledged_by, correlation_id, ingested_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.SessionID, ev.Timestamp, ev.Project, ev.Manifest, ev.PlaybookID, ev.StepID,
		ev.Token, ev.Dimension, ev.BindingSource,
		ev.UnitID, ev.MatchedPattern, ev.AcknowledgedBy, ev.CorrelationID,
		time.Now().UTC().Format(time.RFC3339))
	return err
}

// handleTrace ingests governance events POSTed by kcp-harness: decision traces,
// and — since RFC-0030 (v0.32) — kind:"prohibited_attempt" deny-hit
// notifications, dispatched on the event's kind. Fail-open on the emit side;
// here we simply 204 on success, 400 on bad input, 405 on wrong verb.
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
	if ev.Kind == "prohibited_attempt" {
		var pa prohibitedAttemptEvent
		if err := json.Unmarshal(body, &pa); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if usage != nil {
			if err := usage.writeProhibitedAttempt(pa); err != nil {
				http.Error(w, "write error", http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
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
