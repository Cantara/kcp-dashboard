package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Stats struct {
	// kcp-commands
	TotalInjects      int64
	UniqueTools       int64
	InjectTokens      int64 // SUM(token_estimate) for inject events — context delivered
	ManifestCount     int64 // count of *.yaml in ~/.kcp/commands/

	// kcp-memory (search)
	TotalSearches     int64
	SuccessSearches   int64 // searches where result_count > 0
	RecentSearches    []SearchLogRow

	// kcp-memory (memory.db)
	MemSessions       int64
	MemProjects       int64

	// kcp-mcp smart routing (kept for future use)
	TotalGets         int64
	TokensSaved       int64

	// Misc
	Projects          []string
	TopCommands       []UnitRow
	TopUnits          []UnitRow

	Err               error
}

type UnitRow struct {
	UnitID      string
	Count       int64
	TokenCost   int64 // SUM(token_estimate) for display
}

type SearchLogRow struct {
	Timestamp   string
	Query       string
	ResultCount int64
}

func loadStats(dbPath string, days int, project string) Stats {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_journal_mode=WAL")
	if err != nil {
		return Stats{Err: err}
	}
	defer db.Close()

	since := time.Now().AddDate(0, 0, -days).UTC().Format("2006-01-02T15:04:05Z")

	projectClause := ""
	base := []any{since}
	if project != "" {
		projectClause = " AND project = ?"
		base = append(base, project)
	}

	var s Stats

	// ── Event counts ────────────────────────────────────────────────────────

	row := db.QueryRow(
		`SELECT COUNT(CASE WHEN event_type='search'   THEN 1 END),
		        COUNT(CASE WHEN event_type='get_unit' THEN 1 END),
		        COUNT(CASE WHEN event_type='inject'   THEN 1 END)
		   FROM usage_events WHERE timestamp >= ?`+projectClause, base...)
	row.Scan(&s.TotalSearches, &s.TotalGets, &s.TotalInjects)

	// ── kcp-commands: context delivered ──────────────────────────────────────

	row = db.QueryRow(
		`SELECT COALESCE(SUM(token_estimate), 0),
		        COUNT(DISTINCT unit_id)
		   FROM usage_events
		  WHERE event_type='inject' AND token_estimate IS NOT NULL
		    AND timestamp >= ?`+projectClause, base...)
	row.Scan(&s.InjectTokens, &s.UniqueTools)

	// ── kcp-commands: manifest library size ─────────────────────────────────

	s.ManifestCount = countManifests(dbPath)

	// ── kcp-memory: search quality ──────────────────────────────────────────

	row = db.QueryRow(
		`SELECT COUNT(CASE WHEN result_count > 0 THEN 1 END)
		   FROM usage_events
		  WHERE event_type='search' AND timestamp >= ?`+projectClause, base...)
	row.Scan(&s.SuccessSearches)

	// ── kcp-mcp: tokens saved (smart routing) ───────────────────────────────

	row = db.QueryRow(
		`SELECT COALESCE(SUM(manifest_token_total - token_estimate), 0)
		   FROM usage_events
		  WHERE event_type='get_unit'
		    AND token_estimate IS NOT NULL
		    AND manifest_token_total IS NOT NULL
		    AND timestamp >= ?`+projectClause, base...)
	row.Scan(&s.TokensSaved)

	// ── Recent memory searches (timestamp + query + result_count) ────────────

	rows, err := db.Query(
		`SELECT timestamp, COALESCE(query,''), COALESCE(result_count, 0)
		   FROM usage_events
		  WHERE event_type='search' AND timestamp >= ?`+projectClause+`
		  ORDER BY timestamp DESC LIMIT 8`, base...)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var r SearchLogRow
			rows.Scan(&r.Timestamp, &r.Query, &r.ResultCount)
			s.RecentSearches = append(s.RecentSearches, r)
		}
	}

	// ── Top commands (inject events) with token cost ─────────────────────────

	rows1, err := db.Query(
		`SELECT unit_id, COUNT(*) cnt, COALESCE(SUM(token_estimate), 0)
		   FROM usage_events
		  WHERE event_type='inject' AND unit_id IS NOT NULL
		    AND timestamp >= ?`+projectClause+`
		  GROUP BY unit_id ORDER BY cnt DESC LIMIT 10`, base...)
	if err == nil {
		defer rows1.Close()
		for rows1.Next() {
			var u UnitRow
			rows1.Scan(&u.UnitID, &u.Count, &u.TokenCost)
			s.TopCommands = append(s.TopCommands, u)
		}
	}

	// ── Top units (get_unit events) ─────────────────────────────────────────

	rows2, err := db.Query(
		`SELECT unit_id, COUNT(*) cnt,
		        COALESCE(SUM(manifest_token_total - token_estimate), 0)
		   FROM usage_events
		  WHERE event_type='get_unit' AND unit_id IS NOT NULL
		    AND timestamp >= ?`+projectClause+`
		  GROUP BY unit_id ORDER BY cnt DESC LIMIT 10`, base...)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var u UnitRow
			rows2.Scan(&u.UnitID, &u.Count, &u.TokenCost)
			s.TopUnits = append(s.TopUnits, u)
		}
	}

	// ── Projects ────────────────────────────────────────────────────────────

	rows3, err := db.Query(
		`SELECT DISTINCT project FROM usage_events
		  WHERE timestamp >= ?`+projectClause+` AND project IS NOT NULL
		  ORDER BY project`, base...)
	if err == nil {
		defer rows3.Close()
		for rows3.Next() {
			var p string
			rows3.Scan(&p)
			s.Projects = append(s.Projects, p)
		}
	}

	// ── kcp-memory: session + project counts from memory.db ─────────────────

	memDbPath := filepath.Join(filepath.Dir(dbPath), "memory.db")
	loadMemoryStats(memDbPath, &s)

	return s
}

func loadMemoryStats(memDbPath string, s *Stats) {
	db, err := sql.Open("sqlite", "file:"+memDbPath+"?mode=ro&_journal_mode=WAL")
	if err != nil {
		return
	}
	defer db.Close()
	db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&s.MemSessions)
	db.QueryRow(`SELECT COUNT(DISTINCT project_dir) FROM sessions`).Scan(&s.MemProjects)
}

// countManifests counts *.yaml files in the commands dir next to usage.db.
func countManifests(dbPath string) int64 {
	dir := filepath.Join(filepath.Dir(dbPath), "commands")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var n int64
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			n++
		}
	}
	return n
}
