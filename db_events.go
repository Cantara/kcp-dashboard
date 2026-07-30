package main

// usageEventRow is a browser-consumable projection of one row in usage.db's
// usage_events table — the inject/filter telemetry a downstream KCP consumer
// reads to render governed-run activity. Field tags are the public JSON wire
// shape served by GET /events.
type usageEventRow struct {
	ID            int64  `json:"id"`
	Timestamp     string `json:"timestamp"`
	EventType     string `json:"event_type"`
	UnitID        string `json:"unit_id"`
	Project       string `json:"project"`
	Query         string `json:"query,omitempty"`
	ResultCount   int64  `json:"result_count"`
	TokenEstimate int64  `json:"token_estimate"`
}

// recentEvents returns the most recent usage_events, newest first. It selects
// via rowid and only the columns common to every usage_events schema variant,
// so it reads cleanly whether or not the table declares an explicit id column.
func (u *usageWriter) recentEvents(limit int) ([]usageEventRow, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	rows, err := u.db.Query(
		`SELECT rowid, COALESCE(timestamp,''), COALESCE(event_type,''),
		        COALESCE(unit_id,''), COALESCE(project,''), COALESCE(query,''),
		        COALESCE(result_count,0), COALESCE(token_estimate,0)
		   FROM usage_events
		  ORDER BY timestamp DESC, rowid DESC
		  LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []usageEventRow{}
	for rows.Next() {
		var e usageEventRow
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.EventType, &e.UnitID,
			&e.Project, &e.Query, &e.ResultCount, &e.TokenEstimate); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
