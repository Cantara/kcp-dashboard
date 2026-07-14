package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// insertRichSession inserts a full session row (model, first_message, started_at,
// project) so the recent-sessions query can be exercised end to end.
func insertRichSession(t *testing.T, dir, sessionID, project, model, firstMsg, startedAt string, turns, toolCalls int) {
	t.Helper()
	mdb, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "memory.db")+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open memory.db: %v", err)
	}
	defer mdb.Close()
	_, err = mdb.Exec(
		`INSERT INTO sessions (session_id, project_dir, model, started_at, turn_count, tool_call_count, first_message, scanned_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, project, model, startedAt, turns, toolCalls, firstMsg, startedAt)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

// insertStep inserts a tool_event with full control over tool/manifest/preview/ts.
// Rows get an autoincrement id in insertion order — that is the step sequence.
func insertStep(t *testing.T, dir, sessionID, tool, command, manifestKey, preview, ts string) {
	t.Helper()
	mdb, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "memory.db")+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open memory.db: %v", err)
	}
	defer mdb.Close()
	_, err = mdb.Exec(
		`INSERT INTO tool_events (event_ts, session_id, project_dir, tool, command, manifest_key, output_preview, ingested_at)
		 VALUES (?, ?, '/test', ?, ?, ?, ?, ?)`,
		ts, sessionID, tool, command, manifestKey, preview, ts)
	if err != nil {
		t.Fatalf("insert tool_event: %v", err)
	}
}

func TestLoadSessionSteps_OrderScopeGuidedPreview(t *testing.T) {
	usagePath := createTestDBPair(t)
	dir := filepath.Dir(usagePath)

	// Interleave two sessions to prove scoping; id (insertion order) is the sequence.
	insertStep(t, dir, "sess-A", "Bash", "docker build .", "docker", "built ok", "2026-07-14T10:00:00Z")
	insertStep(t, dir, "sess-B", "Bash", "ls", "", "", "2026-07-14T10:00:01Z")
	insertStep(t, dir, "sess-A", "Read", "cat go.mod", "", "module kcp", "2026-07-14T10:00:02Z")
	insertStep(t, dir, "sess-A", "Bash", "go test ./...", "go", "ok", "2026-07-14T10:00:03Z")

	steps, err := loadSessionSteps(usagePath, "sess-A")
	if err != nil {
		t.Fatalf("loadSessionSteps: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("len(steps) = %d, want 3 (sess-A only)", len(steps))
	}

	wantCmd := []string{"docker build .", "cat go.mod", "go test ./..."}
	wantTool := []string{"Bash", "Read", "Bash"}
	wantGuided := []bool{true, false, true}
	for i, s := range steps {
		if s.Command != wantCmd[i] {
			t.Errorf("step[%d].Command = %q, want %q", i, s.Command, wantCmd[i])
		}
		if s.Tool != wantTool[i] {
			t.Errorf("step[%d].Tool = %q, want %q", i, s.Tool, wantTool[i])
		}
		if s.Guided != wantGuided[i] {
			t.Errorf("step[%d].Guided = %v, want %v", i, s.Guided, wantGuided[i])
		}
		if i > 0 && s.Seq <= steps[i-1].Seq {
			t.Errorf("steps not in ascending Seq order at %d: %d <= %d", i, s.Seq, steps[i-1].Seq)
		}
	}
	if steps[0].OutputPreview != "built ok" {
		t.Errorf("step[0].OutputPreview = %q, want %q", steps[0].OutputPreview, "built ok")
	}
	if steps[0].ManifestKey != "docker" {
		t.Errorf("step[0].ManifestKey = %q, want %q", steps[0].ManifestKey, "docker")
	}
}

func TestLoadRecentSessions_OrderDaysLimit(t *testing.T) {
	usagePath := createTestDBPair(t)
	dir := filepath.Dir(usagePath)

	now := time.Now().UTC()
	ts := func(d time.Duration) string { return now.Add(d).Format("2006-01-02T15:04:05Z") }

	insertRichSession(t, dir, "s-new", "/test", "claude-opus-4-8", "fix the 404", ts(0), 12, 30)
	insertRichSession(t, dir, "s-mid", "/test", "claude-sonnet-5", "add a dashboard", ts(-24*time.Hour), 6, 9)
	insertRichSession(t, dir, "s-old", "/test", "claude-opus-4-8", "older task", ts(-48*time.Hour), 3, 4)
	insertRichSession(t, dir, "s-ancient", "/test", "claude-opus-4-8", "ancient", ts(-40*24*time.Hour), 1, 1)

	// days=30 excludes s-ancient; limit=2 keeps the two newest.
	rows, err := loadRecentSessions(usagePath, 30, "", 2)
	if err != nil {
		t.Fatalf("loadRecentSessions: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2 (limit)", len(rows))
	}
	if rows[0].SessionID != "s-new" || rows[1].SessionID != "s-mid" {
		t.Errorf("order = [%s, %s], want [s-new, s-mid]", rows[0].SessionID, rows[1].SessionID)
	}
	if rows[0].Model != "claude-opus-4-8" {
		t.Errorf("rows[0].Model = %q, want claude-opus-4-8", rows[0].Model)
	}
	if rows[0].Task != "fix the 404" {
		t.Errorf("rows[0].Task = %q, want %q", rows[0].Task, "fix the 404")
	}
	if rows[0].Turns != 12 || rows[0].ToolCalls != 30 {
		t.Errorf("rows[0] counts = %d/%d, want 12/30", rows[0].Turns, rows[0].ToolCalls)
	}
}

func TestLoadRecentSessions_ProjectFilter(t *testing.T) {
	usagePath := createTestDBPair(t)
	dir := filepath.Dir(usagePath)
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	insertRichSession(t, dir, "a1", "/proj-a", "m", "t", now, 1, 1)
	insertRichSession(t, dir, "a2", "/proj-a", "m", "t", now, 1, 1)
	insertRichSession(t, dir, "b1", "/proj-b", "m", "t", now, 1, 1)

	rows, err := loadRecentSessions(usagePath, 30, "/proj-a", 10)
	if err != nil {
		t.Fatalf("loadRecentSessions: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2 (project filter)", len(rows))
	}
	for _, r := range rows {
		if r.Project != "/proj-a" {
			t.Errorf("row project = %q, want /proj-a", r.Project)
		}
	}
}

func TestSessionData_MissingMemoryDB(t *testing.T) {
	usagePath := createTestDBPair(t)
	if err := os.Remove(filepath.Join(filepath.Dir(usagePath), "memory.db")); err != nil {
		t.Fatalf("remove memory.db: %v", err)
	}

	steps, err := loadSessionSteps(usagePath, "whatever")
	if err != nil {
		t.Errorf("loadSessionSteps missing memory.db: err = %v, want nil", err)
	}
	if len(steps) != 0 {
		t.Errorf("loadSessionSteps missing memory.db: len = %d, want 0", len(steps))
	}

	rows, err := loadRecentSessions(usagePath, 30, "", 10)
	if err != nil {
		t.Errorf("loadRecentSessions missing memory.db: err = %v, want nil", err)
	}
	if len(rows) != 0 {
		t.Errorf("loadRecentSessions missing memory.db: len = %d, want 0", len(rows))
	}
}
