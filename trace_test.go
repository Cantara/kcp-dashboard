package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleTrace = `{
  "kind":"decision_trace","session_id":"sess-1","ts":"2026-07-14T14:03:12Z",
  "project":"/app","manifest":"kb://acme","task":"access docs/x.md","as_of":"2026-07-14",
  "selected":1,"skipped":1,
  "gate_summary":[{"gate":"relevance","passed":1,"failed":1}],
  "units":[
    {"id":"u-ok","path":"docs/x.md","outcome":"selected","score":0.72,"gates":[{"gate":"relevance","verdict":"pass"}]},
    {"id":"u-no","path":"docs/y.md","outcome":"skipped","rejected_by":"temporal","gates":[{"gate":"temporal","verdict":"fail"}]}
  ]
}`

func TestHandleTrace_InsertsTraceAndUnits(t *testing.T) {
	usagePath := createTestDBPair(t)
	uw, err := newUsageWriter(usagePath)
	if err != nil {
		t.Fatalf("newUsageWriter: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/trace", strings.NewReader(sampleTrace))
	rr := httptest.NewRecorder()
	handleTrace(rr, req, uw)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (%s)", rr.Code, rr.Body.String())
	}
	uw.db.Close()

	traces, err := loadSessionTraces(usagePath, "sess-1")
	if err != nil {
		t.Fatalf("loadSessionTraces: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("want 1 trace, got %d", len(traces))
	}
	tr := traces[0]
	if tr.Task != "access docs/x.md" || tr.SelectedCount != 1 || tr.SkippedCount != 1 {
		t.Errorf("trace fields wrong: %+v", tr)
	}
	if len(tr.GateSummary) != 1 || tr.GateSummary[0].Gate != "relevance" ||
		tr.GateSummary[0].Passed != 1 || tr.GateSummary[0].Failed != 1 {
		t.Errorf("gate summary wrong: %+v", tr.GateSummary)
	}
	if len(tr.Units) != 2 {
		t.Fatalf("want 2 units, got %d", len(tr.Units))
	}
	var selected, skipped *TraceUnitRow
	for i := range tr.Units {
		switch tr.Units[i].Outcome {
		case "selected":
			selected = &tr.Units[i]
		case "skipped":
			skipped = &tr.Units[i]
		}
	}
	if selected == nil || selected.UnitID != "u-ok" || selected.Score < 0.71 || selected.Score > 0.73 {
		t.Errorf("selected unit wrong: %+v", selected)
	}
	if skipped == nil || skipped.RejectedBy != "temporal" {
		t.Errorf("skipped unit rejected_by wrong: %+v", skipped)
	}
}

func TestHandleTrace_Dedup(t *testing.T) {
	usagePath := createTestDBPair(t)
	uw, _ := newUsageWriter(usagePath)
	for i := 0; i < 3; i++ {
		rr := httptest.NewRecorder()
		handleTrace(rr, httptest.NewRequest(http.MethodPost, "/trace", strings.NewReader(sampleTrace)), uw)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("post %d: got %d", i, rr.Code)
		}
	}
	uw.db.Close()

	traces, err := loadSessionTraces(usagePath, "sess-1")
	if err != nil {
		t.Fatalf("loadSessionTraces: %v", err)
	}
	if len(traces) != 1 {
		t.Errorf("dedup failed: want 1 trace after 3 identical posts, got %d", len(traces))
	}
	if len(traces) == 1 && len(traces[0].Units) != 2 {
		t.Errorf("dedup should not duplicate units: got %d", len(traces[0].Units))
	}
}

func TestHandleTrace_BadInput(t *testing.T) {
	usagePath := createTestDBPair(t)
	uw, _ := newUsageWriter(usagePath)
	defer uw.db.Close()

	rr := httptest.NewRecorder()
	handleTrace(rr, httptest.NewRequest(http.MethodPost, "/trace", strings.NewReader("nope")), uw)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad json: want 400, got %d", rr.Code)
	}

	rr2 := httptest.NewRecorder()
	handleTrace(rr2, httptest.NewRequest(http.MethodGet, "/trace", nil), uw)
	if rr2.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: want 405, got %d", rr2.Code)
	}
}

// sampleProhibited is a kind:"prohibited_attempt" event in the PRE-§17 wire
// shape this dashboard invented before the schema existed (PR #45): "ts",
// "playbook", "step" instead of the normative "timestamp", "playbook_id",
// "step_id", and no unit_id/matched_pattern/acknowledged_by/correlation_id.
// Kept deliberately: older emitters must keep ingesting during rollout.
const sampleProhibited = `{
  "kind":"prohibited_attempt","session_id":"sess-p","ts":"2026-07-30T09:15:00Z",
  "project":"/app","manifest":"kb://acme",
  "playbook":"pb-002-gdpr-sletting","step":"slett",
  "token":"legal/hold/**","dimension":"paths","binding_source":"playbook"
}`

// TestHandleTrace_IngestsProhibitedAttempt is the RFC-0030 / KCP 0.32 gap test:
// a deny-hit raises a notify-only prohibited-attempt event (§4.3b — a deny is
// never grantable), and the dashboard must ingest it on the existing /trace
// path and store it alongside decision traces, not drop it.
func TestHandleTrace_IngestsProhibitedAttempt(t *testing.T) {
	usagePath := createTestDBPair(t)
	uw, err := newUsageWriter(usagePath)
	if err != nil {
		t.Fatalf("newUsageWriter: %v", err)
	}

	rr := httptest.NewRecorder()
	handleTrace(rr, httptest.NewRequest(http.MethodPost, "/trace", strings.NewReader(sampleProhibited)), uw)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (%s)", rr.Code, rr.Body.String())
	}
	uw.db.Close()

	rows, err := loadSessionProhibitedAttempts(usagePath, "sess-p")
	if err != nil {
		t.Fatalf("loadSessionProhibitedAttempts: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 prohibited attempt, got %d", len(rows))
	}
	pa := rows[0]
	if pa.Playbook != "pb-002-gdpr-sletting" || pa.Step != "slett" ||
		pa.Token != "legal/hold/**" || pa.Dimension != "paths" ||
		pa.BindingSource != "playbook" || pa.Count != 1 {
		t.Errorf("prohibited attempt fields wrong: %+v", pa)
	}

	// A prohibited_attempt is not a decision trace — it must not fabricate one.
	traces, err := loadSessionTraces(usagePath, "sess-p")
	if err != nil {
		t.Fatalf("loadSessionTraces: %v", err)
	}
	if len(traces) != 0 {
		t.Errorf("prohibited_attempt must not create a decision trace, got %d", len(traces))
	}
}

// TestHandleTrace_ProhibitedAttempt_RepeatCounts: repeated attempts against the
// same deny aggregate into a count — the RFC-0030 governance signal (repeated
// prohibited attempts = misconfiguration, compromise, or probing). A retransmit
// of the same event (same ts) is deduplicated, distinct ts are distinct attempts.
func TestHandleTrace_ProhibitedAttempt_RepeatCounts(t *testing.T) {
	usagePath := createTestDBPair(t)
	uw, _ := newUsageWriter(usagePath)
	post := func(ts string) {
		t.Helper()
		body := `{"kind":"prohibited_attempt","session_id":"sess-p","ts":"` + ts + `",` +
			`"playbook":"pb-002-gdpr-sletting","step":"slett",` +
			`"token":"legal/hold/**","dimension":"paths","binding_source":"playbook"}`
		rr := httptest.NewRecorder()
		handleTrace(rr, httptest.NewRequest(http.MethodPost, "/trace", strings.NewReader(body)), uw)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("post ts=%s: got %d", ts, rr.Code)
		}
	}
	post("2026-07-30T09:15:00Z")
	post("2026-07-30T09:15:00Z") // retransmit — must not double-count
	post("2026-07-30T09:16:00Z")
	post("2026-07-30T09:17:00Z")
	uw.db.Close()

	rows, err := loadSessionProhibitedAttempts(usagePath, "sess-p")
	if err != nil {
		t.Fatalf("loadSessionProhibitedAttempts: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("same deny should aggregate to 1 row, got %d", len(rows))
	}
	if rows[0].Count != 3 {
		t.Errorf("want count 3 (retransmit deduplicated), got %d", rows[0].Count)
	}
	if rows[0].LastTS != "2026-07-30T09:17:00Z" {
		t.Errorf("want last ts of the newest attempt, got %q", rows[0].LastTS)
	}
}

// s17Fixture loads the vendored §17 conformance fixture, verbatim.
//
// testdata/prohibited-attempt.json is a byte-identical copy of
// conformance/fixtures/observability/prohibited-attempt.json from
// Cantara/knowledge-context-protocol @ v0.32.1 (PR #190, commit 2bcf73b) —
// the canonical wire object of SPEC §17 (prohibited_attempt_events).
// Normative: an emitter and an ingester are conformant when this fixture
// round-trips between them. Do not edit; re-vendor from upstream instead.
func s17Fixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "prohibited-attempt.json"))
	if err != nil {
		t.Fatalf("read §17 fixture: %v", err)
	}
	return string(b)
}

// postTrace POSTs one body to the /trace handler and asserts 204.
func postTrace(t *testing.T, uw *usageWriter, body string) {
	t.Helper()
	rr := httptest.NewRecorder()
	handleTrace(rr, httptest.NewRequest(http.MethodPost, "/trace", strings.NewReader(body)), uw)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("POST /trace: got %d (%s)", rr.Code, rr.Body.String())
	}
}

// TestHandleTrace_S17FixtureRoundTrips is the conformance test for the §17
// wire format (KCP v0.32.1): the canonical fixture POSTed verbatim must be
// ingested with every §17 field stored — including matched_pattern (the deny
// entry that caught the token; on a glob hit it differs from the attempted
// token) and the nullable acknowledged_by stored as NULL, not "".
func TestHandleTrace_S17FixtureRoundTrips(t *testing.T) {
	usagePath := createTestDBPair(t)
	uw, err := newUsageWriter(usagePath)
	if err != nil {
		t.Fatalf("newUsageWriter: %v", err)
	}
	postTrace(t, uw, s17Fixture(t))
	uw.db.Close()

	db, err := sql.Open("sqlite", "file:"+usagePath+"?mode=ro")
	if err != nil {
		t.Fatalf("open usage.db: %v", err)
	}
	defer db.Close()

	var (
		ts, unitID, playbook, step, dimension, token, bindingSource string
		matchedPattern, acknowledgedBy, correlationID               sql.NullString
	)
	err = db.QueryRow(
		`SELECT ts, COALESCE(unit_id,''), COALESCE(playbook,''), COALESCE(step,''),
		        dimension, token, matched_pattern, binding_source,
		        acknowledged_by, correlation_id
		   FROM prohibited_attempts`).Scan(
		&ts, &unitID, &playbook, &step, &dimension, &token,
		&matchedPattern, &bindingSource, &acknowledgedBy, &correlationID)
	if err != nil {
		t.Fatalf("select stored row: %v", err)
	}
	if ts != "2026-07-31T06:15:04Z" {
		t.Errorf("timestamp: got %q", ts)
	}
	if unitID != "sletteagent" {
		t.Errorf("unit_id: got %q", unitID)
	}
	if playbook != "pb-002-gdpr-sletting" || step != "slett" {
		t.Errorf("playbook_id/step_id: got %q / %q", playbook, step)
	}
	if dimension != "paths" || bindingSource != "playbook" {
		t.Errorf("dimension/binding_source: got %q / %q", dimension, bindingSource)
	}
	if token != "legal/hold/2025/case-4711/evidence.pdf" {
		t.Errorf("token: got %q", token)
	}
	if !matchedPattern.Valid || matchedPattern.String != "legal/hold/**" {
		t.Errorf("matched_pattern: got %+v, want the deny glob that caught the token", matchedPattern)
	}
	if acknowledgedBy.Valid {
		t.Errorf("acknowledged_by is null in the fixture and must be stored as NULL, got %q", acknowledgedBy.String)
	}
	if !correlationID.Valid || !strings.HasPrefix(correlationID.String, "00-4bf92f3577b34da6") {
		t.Errorf("correlation_id: got %+v", correlationID)
	}
}

// TestHandleTrace_S17FixtureDedupAndRepeat: a retransmit of the fixture (same
// timestamp) is deduplicated; a second DISTINCT attempt against the same deny
// (later timestamp) is a new row that increments the repeat count.
func TestHandleTrace_S17FixtureDedupAndRepeat(t *testing.T) {
	usagePath := createTestDBPair(t)
	uw, err := newUsageWriter(usagePath)
	if err != nil {
		t.Fatalf("newUsageWriter: %v", err)
	}
	fixture := s17Fixture(t)
	postTrace(t, uw, fixture)
	postTrace(t, uw, fixture) // retransmit — must dedup
	postTrace(t, uw, strings.Replace(fixture,
		"2026-07-31T06:15:04Z", "2026-07-31T06:16:04Z", 1)) // distinct attempt
	uw.db.Close()

	// The fixture carries no session_id (transport envelope, not §17), so the
	// rows land under the empty session.
	rows, err := loadSessionProhibitedAttempts(usagePath, "")
	if err != nil {
		t.Fatalf("loadSessionProhibitedAttempts: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("same deny should aggregate to 1 row, got %d", len(rows))
	}
	if rows[0].Count != 2 {
		t.Errorf("want count 2 (retransmit deduplicated, distinct attempt counted), got %d", rows[0].Count)
	}
	if rows[0].MatchedPattern != "legal/hold/**" {
		t.Errorf("view row should carry matched_pattern, got %q", rows[0].MatchedPattern)
	}
	if rows[0].Token != "legal/hold/2025/case-4711/evidence.pdf" {
		t.Errorf("view row token: got %q", rows[0].Token)
	}
}

// TestHandleTrace_PreS17RowsReadBackGracefully: rows ingested in the pre-§17
// wire shape (PR #45) — no matched_pattern, "ts"/"playbook"/"step" names —
// must still ingest and read back with MatchedPattern empty, not error.
func TestHandleTrace_PreS17RowsReadBackGracefully(t *testing.T) {
	usagePath := createTestDBPair(t)
	uw, err := newUsageWriter(usagePath)
	if err != nil {
		t.Fatalf("newUsageWriter: %v", err)
	}
	postTrace(t, uw, sampleProhibited)
	uw.db.Close()

	rows, err := loadSessionProhibitedAttempts(usagePath, "sess-p")
	if err != nil {
		t.Fatalf("loadSessionProhibitedAttempts: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].Playbook != "pb-002-gdpr-sletting" || rows[0].Step != "slett" ||
		rows[0].Token != "legal/hold/**" {
		t.Errorf("legacy fields wrong: %+v", rows[0])
	}
	if rows[0].MatchedPattern != "" {
		t.Errorf("pre-§17 row has no matched_pattern; want \"\", got %q", rows[0].MatchedPattern)
	}
}

// TestCreateTraceTables_MigratesPreS17Table: a usage.db whose
// prohibited_attempts table was created by PR #45 (no §17 columns) is migrated
// additively on writer start — old rows survive and read back with NULL in the
// new columns; new §17 events land with every field.
func TestCreateTraceTables_MigratesPreS17Table(t *testing.T) {
	usagePath := createTestDBPair(t)
	db, err := sql.Open("sqlite", "file:"+usagePath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE prohibited_attempts (
		id             INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id     TEXT NOT NULL,
		ts             TEXT NOT NULL,
		project        TEXT,
		manifest       TEXT,
		playbook       TEXT,
		step           TEXT,
		token          TEXT NOT NULL,
		dimension      TEXT NOT NULL,
		binding_source TEXT NOT NULL,
		ingested_at    TEXT NOT NULL,
		UNIQUE (session_id, ts, playbook, step, token, dimension)
	)`); err != nil {
		t.Fatalf("create pre-§17 table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO prohibited_attempts
		(session_id, ts, playbook, step, token, dimension, binding_source, ingested_at)
		VALUES ('sess-old', '2026-07-01T00:00:00Z', 'pb-old', 'step-old',
		        'secrets/**', 'paths', 'skill', '2026-07-01T00:00:01Z')`); err != nil {
		t.Fatalf("insert pre-§17 row: %v", err)
	}
	db.Close()

	uw, err := newUsageWriter(usagePath) // runs the additive migration
	if err != nil {
		t.Fatalf("newUsageWriter on pre-§17 db: %v", err)
	}
	postTrace(t, uw, s17Fixture(t))
	uw.db.Close()

	old, err := loadSessionProhibitedAttempts(usagePath, "sess-old")
	if err != nil {
		t.Fatalf("load old session: %v", err)
	}
	if len(old) != 1 || old[0].Token != "secrets/**" || old[0].MatchedPattern != "" {
		t.Errorf("pre-§17 row must survive migration with empty matched_pattern, got %+v", old)
	}
	migrated, err := loadSessionProhibitedAttempts(usagePath, "")
	if err != nil {
		t.Fatalf("load fixture session: %v", err)
	}
	if len(migrated) != 1 || migrated[0].MatchedPattern != "legal/hold/**" {
		t.Errorf("§17 event on migrated table must carry matched_pattern, got %+v", migrated)
	}
}

func TestLoadSessionProhibitedAttempts_MissingTables(t *testing.T) {
	usagePath := createTestDBPair(t) // only usage_events — no prohibited_attempts table
	rows, err := loadSessionProhibitedAttempts(usagePath, "whatever")
	if err != nil {
		t.Errorf("missing tables: err=%v, want nil", err)
	}
	if len(rows) != 0 {
		t.Errorf("missing tables: len=%d, want 0", len(rows))
	}
}

func TestLoadSessionTraces_MissingTables(t *testing.T) {
	usagePath := createTestDBPair(t) // only usage_events — no trace tables
	traces, err := loadSessionTraces(usagePath, "whatever")
	if err != nil {
		t.Errorf("missing tables: err=%v, want nil", err)
	}
	if len(traces) != 0 {
		t.Errorf("missing tables: len=%d, want 0", len(traces))
	}
}
