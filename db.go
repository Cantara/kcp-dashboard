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

	// kcp-memory (memory.db) — tool_events
	TotalBashCalls    int64
	ManifestHitRate   float64        // manifested / total (0.0–1.0)
	FilteredRetryRate float64        // retry rate excluding iterative commands (0.0–1.0)
	HelpFollowupRate  float64        // --help calls within 5min of inject (0.0–1.0)
	QualityAlerts     []QualityAlert

	// kcp-memory (memory.db) — sessions
	SessionSizeDist   [5]int64 // [1-5, 6-20, 21-50, 51-100, 100+] turns
	AvgTurns          float64
	AvgToolCalls      float64

	// kcp-mcp smart routing (kept for future use)
	TotalGets         int64
	TokensSaved       int64

	// Misc
	Projects          []string
	TopCommands       []UnitRow
	TopUnits          []UnitRow

	Err               error
}

type QualityAlert struct {
	ManifestKey string
	TotalCalls  int64
	RetryRate   float64
	HelpRate    float64
	Score       float64 // lower = worse: 0.5*retry + 0.5*help
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
	loadMemoryStats(memDbPath, &s, days)

	return s
}

func loadMemoryStats(memDbPath string, s *Stats, days int) {
	db, err := sql.Open("sqlite", "file:"+memDbPath+"?mode=ro&_journal_mode=WAL")
	if err != nil {
		return
	}
	defer db.Close()

	since := time.Now().AddDate(0, 0, -days).UTC().Format("2006-01-02T15:04:05Z")

	db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&s.MemSessions)
	db.QueryRow(`SELECT COUNT(DISTINCT project_dir) FROM sessions`).Scan(&s.MemProjects)

	// ── Manifest coverage ────────────────────────────────────────────────
	var total, manifested int64
	db.QueryRow(`SELECT
		COUNT(*) as total,
		COUNT(CASE WHEN manifest_key IS NOT NULL AND manifest_key != '' THEN 1 END) as manifested
	FROM tool_events
	WHERE event_ts >= ?`, since).Scan(&total, &manifested)
	s.TotalBashCalls = total
	if total > 0 {
		s.ManifestHitRate = float64(manifested) / float64(total)
	}

	// ── Session size distribution + averages ─────────────────────────────
	rows, err := db.Query(`SELECT turn_count, tool_call_count
		FROM sessions WHERE started_at >= ?`, since)
	if err == nil {
		defer rows.Close()
		var totalTurns, totalToolCalls, sessionCount int64
		for rows.Next() {
			var turns, tools int64
			rows.Scan(&turns, &tools)
			totalTurns += turns
			totalToolCalls += tools
			sessionCount++
			switch {
			case turns <= 5:
				s.SessionSizeDist[0]++
			case turns <= 20:
				s.SessionSizeDist[1]++
			case turns <= 50:
				s.SessionSizeDist[2]++
			case turns <= 100:
				s.SessionSizeDist[3]++
			default:
				s.SessionSizeDist[4]++
			}
		}
		if sessionCount > 0 {
			s.AvgTurns = float64(totalTurns) / float64(sessionCount)
			s.AvgToolCalls = float64(totalToolCalls) / float64(sessionCount)
		}
	}

	// ── Filtered retry rate ──────────────────────────────────────────────
	iterExclude := "('ls','grep','cat','head','find','cd','tail','wc','echo','sort','uniq','cut')"

	var retryCount, totalAction int64
	db.QueryRow(`SELECT COUNT(*) FROM tool_events e1
		WHERE e1.manifest_key IS NOT NULL AND e1.manifest_key != ''
		  AND e1.manifest_key NOT IN `+iterExclude+`
		  AND e1.event_ts >= ?
		  AND EXISTS (
			SELECT 1 FROM tool_events e2
			WHERE e2.session_id = e1.session_id
			  AND e2.manifest_key = e1.manifest_key
			  AND e2.id > e1.id
			  AND e2.id <= e1.id + 20
			  AND (julianday(e2.event_ts) - julianday(e1.event_ts)) * 86400 <= 90
		  )`, since).Scan(&retryCount)

	db.QueryRow(`SELECT COUNT(*) FROM tool_events
		WHERE manifest_key IS NOT NULL AND manifest_key != ''
		  AND manifest_key NOT IN `+iterExclude+`
		  AND event_ts >= ?`, since).Scan(&totalAction)

	if totalAction > 0 {
		s.FilteredRetryRate = float64(retryCount) / float64(totalAction)
	}

	// ── Help-followup rate ───────────────────────────────────────────────
	var helpCount, totalManifested int64
	db.QueryRow(`SELECT COUNT(DISTINCT e1.id) FROM tool_events e1
		WHERE e1.manifest_key IS NOT NULL AND e1.manifest_key != ''
		  AND e1.event_ts >= ?
		  AND EXISTS (
			SELECT 1 FROM tool_events e2
			WHERE e2.session_id = e1.session_id
			  AND e2.id > e1.id
			  AND (e2.command LIKE '%--%help%' OR e2.command LIKE '% -h %' OR e2.command LIKE '% -h')
			  AND (julianday(e2.event_ts) - julianday(e1.event_ts)) * 86400 <= 300
		  )`, since).Scan(&helpCount)

	db.QueryRow(`SELECT COUNT(*) FROM tool_events
		WHERE manifest_key IS NOT NULL AND manifest_key != ''
		  AND event_ts >= ?`, since).Scan(&totalManifested)

	if totalManifested > 0 {
		s.HelpFollowupRate = float64(helpCount) / float64(totalManifested)
	}

	// ── Quality alerts (top 5 worst manifests) ───────────────────────────
	alertRows, err := db.Query(`SELECT manifest_key, COUNT(*) as total_calls,
		CAST(SUM(CASE WHEN EXISTS (
			SELECT 1 FROM tool_events e2
			WHERE e2.session_id = tool_events.session_id
			  AND e2.manifest_key = tool_events.manifest_key
			  AND e2.id > tool_events.id
			  AND e2.id <= tool_events.id + 20
			  AND (julianday(e2.event_ts) - julianday(tool_events.event_ts)) * 86400 <= 90
		) THEN 1 ELSE 0 END) AS REAL) / COUNT(*) as retry_rate,
		CAST(SUM(CASE WHEN EXISTS (
			SELECT 1 FROM tool_events e2
			WHERE e2.session_id = tool_events.session_id
			  AND e2.id > tool_events.id
			  AND (e2.command LIKE '%--%help%' OR e2.command LIKE '% -h %')
			  AND (julianday(e2.event_ts) - julianday(tool_events.event_ts)) * 86400 <= 300
		) THEN 1 ELSE 0 END) AS REAL) / COUNT(*) as help_rate
	FROM tool_events
	WHERE manifest_key IS NOT NULL AND manifest_key != ''
	  AND manifest_key NOT IN `+iterExclude+`
	  AND event_ts >= ?
	GROUP BY manifest_key
	HAVING total_calls >= 10
	ORDER BY (retry_rate * 0.5 + help_rate * 0.5) DESC
	LIMIT 5`, since)
	if err == nil {
		defer alertRows.Close()
		for alertRows.Next() {
			var a QualityAlert
			alertRows.Scan(&a.ManifestKey, &a.TotalCalls, &a.RetryRate, &a.HelpRate)
			a.Score = a.RetryRate*0.5 + a.HelpRate*0.5
			s.QualityAlerts = append(s.QualityAlerts, a)
		}
	}
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
