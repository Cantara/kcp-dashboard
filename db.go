package main

import (
	"database/sql"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Stats struct {
	TotalSearches int64
	TotalGets     int64
	TokensSaved   int64
	Projects      []string
	TopUnits      []UnitRow
	TopQueries    []QueryRow
	Err           error
}

type UnitRow struct {
	UnitID      string
	Count       int64
	TokensSaved int64
}

type QueryRow struct {
	Query string
	Count int64
}

func loadStats(dbPath string, days int, project string) Stats {
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?mode=ro&_journal_mode=WAL")
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

	// Counts
	row := db.QueryRow(
		`SELECT COUNT(CASE WHEN event_type='search' THEN 1 END),
		        COUNT(CASE WHEN event_type='get_unit' THEN 1 END)
		   FROM usage_events WHERE timestamp >= ?`+projectClause, base...)
	row.Scan(&s.TotalSearches, &s.TotalGets)

	// Tokens saved
	row = db.QueryRow(
		`SELECT COALESCE(SUM(manifest_token_total - token_estimate), 0)
		   FROM usage_events
		  WHERE event_type='get_unit'
		    AND token_estimate IS NOT NULL
		    AND manifest_token_total IS NOT NULL
		    AND timestamp >= ?`+projectClause, base...)
	row.Scan(&s.TokensSaved)

	// Top units
	rows, err := db.Query(
		`SELECT unit_id, COUNT(*) cnt,
		        COALESCE(SUM(manifest_token_total - token_estimate), 0) saved
		   FROM usage_events
		  WHERE event_type='get_unit' AND unit_id IS NOT NULL
		    AND timestamp >= ?`+projectClause+`
		  GROUP BY unit_id ORDER BY cnt DESC LIMIT 10`, base...)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var u UnitRow
			rows.Scan(&u.UnitID, &u.Count, &u.TokensSaved)
			s.TopUnits = append(s.TopUnits, u)
		}
	}

	// Top queries
	rows2, err := db.Query(
		`SELECT query, COUNT(*) cnt
		   FROM usage_events
		  WHERE event_type='search' AND query IS NOT NULL
		    AND timestamp >= ?`+projectClause+`
		  GROUP BY query ORDER BY cnt DESC LIMIT 8`, base...)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var q QueryRow
			rows2.Scan(&q.Query, &q.Count)
			s.TopQueries = append(s.TopQueries, q)
		}
	}

	// Projects
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

	return s
}
