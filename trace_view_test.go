package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestRenderGateFunnel(t *testing.T) {
	gates := []GateStat{
		{Gate: "relevance", Passed: 1, Failed: 1},
		{Gate: "temporal", Passed: 3, Failed: 0},
	}
	out := renderGateFunnel(gates, 50)
	for _, want := range []string{"relevance", "temporal", "1/2", "3/3"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderGateFunnel missing %q:\n%s", want, out)
		}
	}
}

func TestRenderDecisions(t *testing.T) {
	traces := []DecisionTraceRow{{
		Task:          "access docs/x.md",
		SelectedCount: 1,
		SkippedCount:  1,
		GateSummary:   []GateStat{{Gate: "relevance", Passed: 1, Failed: 1}},
		Units: []TraceUnitRow{
			{UnitID: "u-ok", Outcome: "selected", Score: 0.72},
			{UnitID: "u-no", Outcome: "skipped", RejectedBy: "temporal"},
		},
	}}
	out := renderDecisions(traces, 60)
	for _, want := range []string{"access docs/x.md", "relevance", "u-ok", "0.72", "u-no", "temporal", "✓", "✗"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderDecisions missing %q:\n%s", want, out)
		}
	}
}

func TestRenderDecisions_Empty(t *testing.T) {
	out := renderDecisions(nil, 40)
	if !strings.Contains(strings.ToLower(out), "no decision") {
		t.Errorf("empty decisions should say so, got:\n%s", out)
	}
}

func TestUpdate_ToggleDecisions(t *testing.T) {
	usagePath := createTestDBPair(t)
	dir := filepath.Dir(usagePath)
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	insertRichSession(t, dir, "s1", "/test", "claude-opus-4-8", "do a thing", now, 2, 2)

	// Ingest one trace for s1 via the real /trace path.
	uw, err := newUsageWriter(usagePath)
	if err != nil {
		t.Fatalf("newUsageWriter: %v", err)
	}
	body := `{"session_id":"s1","ts":"` + now + `","task":"do a thing",` +
		`"selected":1,"skipped":0,"gate_summary":[{"gate":"relevance","passed":1,"failed":0}],` +
		`"units":[{"id":"u1","outcome":"selected","score":0.9}]}`
	rr := httptest.NewRecorder()
	handleTrace(rr, httptest.NewRequest(http.MethodPost, "/trace", strings.NewReader(body)), uw)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("ingest trace: got %d", rr.Code)
	}
	uw.db.Close()

	m := newModel(usagePath, 30, "")

	// Enter session mode — traces load alongside steps, default pane is steps.
	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = tm.(model)
	if !m.sessionMode {
		t.Fatal("expected sessionMode after 's'")
	}
	if len(m.traces) != 1 {
		t.Fatalf("expected 1 trace loaded on enter, got %d", len(m.traces))
	}
	if m.showDecisions {
		t.Fatal("should default to the steps pane, not decisions")
	}

	// 'g' toggles to decisions and back.
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = tm.(model)
	if !m.showDecisions {
		t.Error("'g' should switch to the decisions pane")
	}
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = tm.(model)
	if m.showDecisions {
		t.Error("'g' should toggle back to steps")
	}
}
