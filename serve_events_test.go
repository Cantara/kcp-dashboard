package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A downstream KCP consumer (a static browser page) must be able to read
// governed-run telemetry out of usage.db over the serve daemon. This exercises
// the read side: insert an inject event, GET /events, and assert the row comes
// back as JSON with permissive CORS headers.
func TestHandleEvents_ReturnsUsageEventsAsJSON(t *testing.T) {
	usagePath := createTestDBPair(t)
	uw, err := newUsageWriter(usagePath)
	if err != nil {
		t.Fatalf("newUsageWriter: %v", err)
	}
	defer uw.db.Close()

	uw.writeInject("kb://acme", "/app", 42)

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	rr := httptest.NewRecorder()
	handleEvents(rr, req, uw)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rr.Code, rr.Body.String())
	}

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected permissive CORS header, got %q", got)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json content-type, got %q", ct)
	}

	var events []usageEventRow
	if err := json.NewDecoder(rr.Body).Decode(&events); err != nil {
		t.Fatalf("decode /events body: %v\nbody: %s", err, rr.Body.String())
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.EventType != "inject" || ev.UnitID != "kb://acme" ||
		ev.Project != "/app" || ev.TokenEstimate != 42 {
		t.Errorf("event fields wrong: %+v", ev)
	}
}

// CORS preflight from a browser must be answered without touching the DB.
func TestHandleEvents_CORSPreflight(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/events", nil)
	rr := httptest.NewRecorder()
	handleEvents(rr, req, nil)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("preflight: expected 204, got %d", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("preflight missing CORS header, got %q", got)
	}
}

// Only GET (and the OPTIONS preflight) are read verbs; writes must be rejected.
func TestHandleEvents_WrongMethod_Returns405(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/events", nil)
	rr := httptest.NewRecorder()
	handleEvents(rr, req, nil)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

// An absent usage writer must degrade to an empty JSON array, not a 500.
func TestHandleEvents_NilUsage_EmptyArray(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	rr := httptest.NewRecorder()
	handleEvents(rr, req, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "[]" && body != "[]\n" {
		t.Errorf("expected empty JSON array, got %q", body)
	}
}
