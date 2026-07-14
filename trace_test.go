package main

import (
	"net/http"
	"net/http/httptest"
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
