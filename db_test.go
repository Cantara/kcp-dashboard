package main

import (
	"database/sql"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// createTestDB creates a temp SQLite file with the usage_events schema
// and returns the file path. Caller must os.Remove it when done.
func createTestDB(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "kcp-dashboard-test-*.db")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	path := f.Name()
	f.Close()

	db, err := sql.Open("sqlite", "file:"+path+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE usage_events (
			id                   INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp            TEXT    NOT NULL,
			event_type           TEXT    NOT NULL,
			project              TEXT,
			query                TEXT,
			unit_id              TEXT,
			result_count         INTEGER,
			token_estimate       INTEGER,
			manifest_token_total INTEGER,
			session_id           TEXT
		)`)
	if err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}

	return path
}

func recentTimestamp() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

func insertEvent(t *testing.T, dbPath string, eventType, project, query, unitID string,
	tokenEstimate, manifestTokenTotal int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("sql.Open for insert: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		INSERT INTO usage_events (timestamp, event_type, project, query, unit_id, token_estimate, manifest_token_total)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		recentTimestamp(), eventType, project, query, unitID, tokenEstimate, manifestTokenTotal)
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}
}

func TestLoadStats_CountsByEventType(t *testing.T) {
	dbPath := createTestDB(t)
	defer os.Remove(dbPath)

	// Insert known counts: 3 search, 2 get_unit, 4 inject
	for i := 0; i < 3; i++ {
		insertEvent(t, dbPath, "search", "proj-a", "find widgets", "", 0, 0)
	}
	for i := 0; i < 2; i++ {
		insertEvent(t, dbPath, "get_unit", "proj-a", "", "xorcery", 100, 5000)
	}
	for i := 0; i < 4; i++ {
		insertEvent(t, dbPath, "inject", "proj-a", "", "docker", 250, 0)
	}

	s := loadStats(dbPath, 30, "")

	if s.Err != nil {
		t.Fatalf("loadStats error: %v", s.Err)
	}
	if s.TotalSearches != 3 {
		t.Errorf("TotalSearches: got %d, want 3", s.TotalSearches)
	}
	if s.TotalGets != 2 {
		t.Errorf("TotalGets: got %d, want 2", s.TotalGets)
	}
	if s.TotalInjects != 4 {
		t.Errorf("TotalInjects: got %d, want 4", s.TotalInjects)
	}
}

func TestLoadStats_TokensSaved(t *testing.T) {
	dbPath := createTestDB(t)
	defer os.Remove(dbPath)

	// get_unit with manifest_token_total=5000, token_estimate=100 => saved 4900
	insertEvent(t, dbPath, "get_unit", "proj", "", "lib-pcb", 100, 5000)
	// get_unit with manifest_token_total=3000, token_estimate=200 => saved 2800
	insertEvent(t, dbPath, "get_unit", "proj", "", "xorcery", 200, 3000)

	s := loadStats(dbPath, 30, "")

	if s.Err != nil {
		t.Fatalf("loadStats error: %v", s.Err)
	}
	// Total saved = (5000-100) + (3000-200) = 4900 + 2800 = 7700
	if s.TokensSaved != 7700 {
		t.Errorf("TokensSaved: got %d, want 7700", s.TokensSaved)
	}
}

func TestLoadStats_TopCommands(t *testing.T) {
	dbPath := createTestDB(t)
	defer os.Remove(dbPath)

	// docker: 5 injects, git: 2 injects, npm: 1 inject
	for i := 0; i < 5; i++ {
		insertEvent(t, dbPath, "inject", "proj", "", "docker", 250, 0)
	}
	for i := 0; i < 2; i++ {
		insertEvent(t, dbPath, "inject", "proj", "", "git", 100, 0)
	}
	insertEvent(t, dbPath, "inject", "proj", "", "npm-install", 50, 0)

	s := loadStats(dbPath, 30, "")

	if s.Err != nil {
		t.Fatalf("loadStats error: %v", s.Err)
	}
	if len(s.TopCommands) != 3 {
		t.Fatalf("TopCommands length: got %d, want 3", len(s.TopCommands))
	}
	// Should be ordered by count descending
	if s.TopCommands[0].UnitID != "docker" {
		t.Errorf("TopCommands[0].UnitID: got %q, want %q", s.TopCommands[0].UnitID, "docker")
	}
	if s.TopCommands[0].Count != 5 {
		t.Errorf("TopCommands[0].Count: got %d, want 5", s.TopCommands[0].Count)
	}
	if s.TopCommands[1].UnitID != "git" {
		t.Errorf("TopCommands[1].UnitID: got %q, want %q", s.TopCommands[1].UnitID, "git")
	}
	if s.TopCommands[1].Count != 2 {
		t.Errorf("TopCommands[1].Count: got %d, want 2", s.TopCommands[1].Count)
	}
	if s.TopCommands[2].UnitID != "npm-install" {
		t.Errorf("TopCommands[2].UnitID: got %q, want %q", s.TopCommands[2].UnitID, "npm-install")
	}
}

func TestLoadStats_TopUnits(t *testing.T) {
	dbPath := createTestDB(t)
	defer os.Remove(dbPath)

	// xorcery: 3 get_unit, lib-pcb: 1 get_unit
	for i := 0; i < 3; i++ {
		insertEvent(t, dbPath, "get_unit", "proj", "", "xorcery", 100, 2000)
	}
	insertEvent(t, dbPath, "get_unit", "proj", "", "lib-pcb", 200, 4000)

	s := loadStats(dbPath, 30, "")

	if s.Err != nil {
		t.Fatalf("loadStats error: %v", s.Err)
	}
	if len(s.TopUnits) != 2 {
		t.Fatalf("TopUnits length: got %d, want 2", len(s.TopUnits))
	}
	if s.TopUnits[0].UnitID != "xorcery" {
		t.Errorf("TopUnits[0].UnitID: got %q, want %q", s.TopUnits[0].UnitID, "xorcery")
	}
	if s.TopUnits[0].Count != 3 {
		t.Errorf("TopUnits[0].Count: got %d, want 3", s.TopUnits[0].Count)
	}
}

func TestLoadStats_InjectTokens(t *testing.T) {
	dbPath := createTestDB(t)
	defer os.Remove(dbPath)

	// 3 injects at 250 tokens each = 750 total context delivered
	for i := 0; i < 3; i++ {
		insertEvent(t, dbPath, "inject", "proj", "", "docker", 250, 0)
	}
	// 2 injects at 100 tokens each = 200
	for i := 0; i < 2; i++ {
		insertEvent(t, dbPath, "inject", "proj", "", "git", 100, 0)
	}

	s := loadStats(dbPath, 30, "")

	if s.Err != nil {
		t.Fatalf("loadStats error: %v", s.Err)
	}
	// Total context delivered = 3*250 + 2*100 = 950
	if s.InjectTokens != 950 {
		t.Errorf("InjectTokens: got %d, want 950", s.InjectTokens)
	}
	if s.UniqueTools != 2 {
		t.Errorf("UniqueTools: got %d, want 2", s.UniqueTools)
	}
}

func TestCountManifests(t *testing.T) {
	// Create a temp directory simulating ~/.kcp/ with a commands/ subdir
	tmpDir := t.TempDir()
	cmdDir := tmpDir + "/commands"
	if err := os.Mkdir(cmdDir, 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	// Create some .yaml files and a non-yaml file
	for _, name := range []string{"docker.yaml", "git.yaml", "npm.yaml", "README.md"} {
		f, err := os.Create(cmdDir + "/" + name)
		if err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
		f.Close()
	}

	// countManifests expects a dbPath and looks for commands/ next to it
	fakeDbPath := tmpDir + "/usage.db"
	got := countManifests(fakeDbPath)
	if got != 3 {
		t.Errorf("countManifests: got %d, want 3", got)
	}
}

func TestCountManifests_NoDir(t *testing.T) {
	// When commands/ directory doesn't exist, should return 0
	tmpDir := t.TempDir()
	fakeDbPath := tmpDir + "/usage.db"
	got := countManifests(fakeDbPath)
	if got != 0 {
		t.Errorf("countManifests with no dir: got %d, want 0", got)
	}
}

func TestCountManifests_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	cmdDir := tmpDir + "/commands"
	if err := os.Mkdir(cmdDir, 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	fakeDbPath := tmpDir + "/usage.db"
	got := countManifests(fakeDbPath)
	if got != 0 {
		t.Errorf("countManifests with empty dir: got %d, want 0", got)
	}
}

func TestLoadStats_Projects(t *testing.T) {
	dbPath := createTestDB(t)
	defer os.Remove(dbPath)

	insertEvent(t, dbPath, "search", "beta-proj", "q", "", 0, 0)
	insertEvent(t, dbPath, "inject", "alpha-proj", "", "docker", 100, 0)
	insertEvent(t, dbPath, "search", "alpha-proj", "q2", "", 0, 0)

	s := loadStats(dbPath, 30, "")

	if s.Err != nil {
		t.Fatalf("loadStats error: %v", s.Err)
	}
	if len(s.Projects) != 2 {
		t.Fatalf("Projects length: got %d, want 2", len(s.Projects))
	}
	// Should be sorted alphabetically
	if s.Projects[0] != "alpha-proj" {
		t.Errorf("Projects[0]: got %q, want %q", s.Projects[0], "alpha-proj")
	}
	if s.Projects[1] != "beta-proj" {
		t.Errorf("Projects[1]: got %q, want %q", s.Projects[1], "beta-proj")
	}
}

func TestLoadStats_ProjectFilter(t *testing.T) {
	dbPath := createTestDB(t)
	defer os.Remove(dbPath)

	insertEvent(t, dbPath, "search", "proj-a", "q1", "", 0, 0)
	insertEvent(t, dbPath, "search", "proj-b", "q2", "", 0, 0)
	insertEvent(t, dbPath, "inject", "proj-a", "", "git", 100, 0)

	s := loadStats(dbPath, 30, "proj-a")

	if s.Err != nil {
		t.Fatalf("loadStats error: %v", s.Err)
	}
	if s.TotalSearches != 1 {
		t.Errorf("TotalSearches with project filter: got %d, want 1", s.TotalSearches)
	}
	if s.TotalInjects != 1 {
		t.Errorf("TotalInjects with project filter: got %d, want 1", s.TotalInjects)
	}
}

func TestLoadStats_EmptyDb(t *testing.T) {
	dbPath := createTestDB(t)
	defer os.Remove(dbPath)

	s := loadStats(dbPath, 30, "")

	if s.Err != nil {
		t.Fatalf("loadStats error: %v", s.Err)
	}
	if s.TotalSearches != 0 {
		t.Errorf("TotalSearches on empty DB: got %d, want 0", s.TotalSearches)
	}
	if s.TotalGets != 0 {
		t.Errorf("TotalGets on empty DB: got %d, want 0", s.TotalGets)
	}
	if s.TotalInjects != 0 {
		t.Errorf("TotalInjects on empty DB: got %d, want 0", s.TotalInjects)
	}
}

func TestLoadStats_NonExistentDb(t *testing.T) {
	path := "/tmp/does-not-exist-kcp-test-" + t.Name() + ".db"
	defer os.Remove(path)

	s := loadStats(path, 30, "")

	// modernc.org/sqlite with mode=ro may open lazily; queries against missing
	// tables silently scan zero rows because loadStats ignores row.Scan errors.
	// The production code handles this gracefully — verify no panic and that
	// either an error is returned or counts are zero.
	if s.Err != nil {
		return // error is fine for non-existent DB
	}
	if s.TotalSearches != 0 || s.TotalGets != 0 || s.TotalInjects != 0 {
		t.Errorf("Expected zero counts for non-existent DB, got searches=%d gets=%d injects=%d",
			s.TotalSearches, s.TotalGets, s.TotalInjects)
	}
}
