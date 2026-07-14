package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SessionRow is a session summary for the drill-down list, read from
// memory.db's `sessions` table.
type SessionRow struct {
	SessionID string
	Project   string
	Model     string
	Task      string // sessions.first_message, trimmed
	Turns     int64
	ToolCalls int64
	StartedAt string
}

// SessionStep is a single tool invocation within a session, read from
// memory.db's `tool_events` table in stable (id) order.
type SessionStep struct {
	Seq           int64  // tool_events.id — the step sequence
	TS            string // event_ts
	Tool          string
	Command       string
	ManifestKey   string // "" when no KCP guidance matched
	Guided        bool   // ManifestKey != ""
	OutputPreview string
}

// memoryDBPath returns the memory.db that sits next to usage.db (the same
// sibling convention loadStats uses). ok is false when it is absent, so callers
// can degrade to empty results instead of erroring.
func memoryDBPath(usageDBPath string) (path string, ok bool) {
	p := filepath.Join(filepath.Dir(usageDBPath), "memory.db")
	if _, err := os.Stat(p); err != nil {
		return "", false
	}
	return p, true
}

func openMemoryRO(usageDBPath string) (*sql.DB, bool, error) {
	p, ok := memoryDBPath(usageDBPath)
	if !ok {
		return nil, false, nil
	}
	db, err := sql.Open("sqlite", "file:"+p+"?mode=ro&_journal_mode=WAL")
	if err != nil {
		return nil, false, err
	}
	return db, true, nil
}

// loadRecentSessions returns the most recent sessions within the time window,
// newest first. An absent memory.db yields an empty slice and a nil error.
func loadRecentSessions(usageDBPath string, days int, project string, limit int) ([]SessionRow, error) {
	db, ok, err := openMemoryRO(usageDBPath)
	if err != nil || !ok {
		return nil, err
	}
	defer db.Close()

	since := time.Now().AddDate(0, 0, -days).UTC().Format("2006-01-02T15:04:05Z")

	q := `SELECT session_id, project_dir, COALESCE(model,''), COALESCE(first_message,''),
	             COALESCE(turn_count,0), COALESCE(tool_call_count,0), COALESCE(started_at,'')
	        FROM sessions
	       WHERE COALESCE(started_at,'') >= ?`
	args := []any{since}
	if project != "" {
		q += ` AND project_dir = ?`
		args = append(args, project)
	}
	q += ` ORDER BY started_at DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SessionRow
	for rows.Next() {
		var r SessionRow
		if err := rows.Scan(&r.SessionID, &r.Project, &r.Model, &r.Task,
			&r.Turns, &r.ToolCalls, &r.StartedAt); err != nil {
			return nil, err
		}
		r.Task = strings.TrimSpace(r.Task)
		out = append(out, r)
	}
	return out, rows.Err()
}

// loadSessionSteps returns every tool_event for a session in id order — the
// step timeline the drill-down view renders. Absent memory.db → empty, nil.
func loadSessionSteps(usageDBPath, sessionID string) ([]SessionStep, error) {
	db, ok, err := openMemoryRO(usageDBPath)
	if err != nil || !ok {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(
		`SELECT id, event_ts, tool, command, COALESCE(manifest_key,''), COALESCE(output_preview,'')
		   FROM tool_events
		  WHERE session_id = ?
		  ORDER BY id ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SessionStep
	for rows.Next() {
		var s SessionStep
		if err := rows.Scan(&s.Seq, &s.TS, &s.Tool, &s.Command, &s.ManifestKey, &s.OutputPreview); err != nil {
			return nil, err
		}
		s.Guided = s.ManifestKey != ""
		out = append(out, s)
	}
	return out, rows.Err()
}
