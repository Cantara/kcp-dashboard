package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestClampIndex(t *testing.T) {
	tests := []struct {
		i, n, want int
	}{
		{0, 0, 0},
		{5, 0, 0},   // empty → 0
		{-1, 3, 0},  // below → 0
		{3, 3, 2},   // at len → last
		{9, 3, 2},   // above → last
		{1, 3, 1},   // in range
	}
	for _, tt := range tests {
		if got := clampIndex(tt.i, tt.n); got != tt.want {
			t.Errorf("clampIndex(%d,%d) = %d, want %d", tt.i, tt.n, got, tt.want)
		}
	}
}

func TestRenderSessionList(t *testing.T) {
	sessions := []SessionRow{
		{SessionID: "aaaaaaaa1111", Model: "claude-opus-4-8", Task: "fix the 404", Turns: 12},
		{SessionID: "bbbbbbbb2222", Model: "claude-sonnet-5", Task: "add dashboard", Turns: 6},
	}
	out := renderSessionList(sessions, 1, 48)

	for _, want := range []string{"aaaaaaaa", "bbbbbbbb", "fix the 404", "add dashboard"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderSessionList missing %q:\n%s", want, out)
		}
	}
	// Selection marker present, and on the selected (second) row's id.
	if !strings.Contains(out, "▸") {
		t.Errorf("renderSessionList has no selection marker:\n%s", out)
	}
	lines := strings.Split(out, "\n")
	var selLine string
	for _, ln := range lines {
		if strings.Contains(ln, "bbbbbbbb") {
			selLine = ln
		}
	}
	if !strings.Contains(selLine, "▸") {
		t.Errorf("selected row (bbbbbbbb) not marked:\n%s", out)
	}
}

func TestRenderSessionList_Empty(t *testing.T) {
	out := renderSessionList(nil, 0, 40)
	if !strings.Contains(strings.ToLower(out), "no sessions") {
		t.Errorf("empty session list should say so, got:\n%s", out)
	}
}

func TestRenderSteps(t *testing.T) {
	steps := []SessionStep{
		{Seq: 1, Tool: "Bash", Command: "docker build .", ManifestKey: "docker", Guided: true, OutputPreview: "built ok"},
		{Seq: 2, Tool: "Read", Command: "cat go.mod", Guided: false},
	}
	out := renderSteps(steps, 60)

	for _, want := range []string{"docker build .", "cat go.mod", "Bash", "Read", "✓"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderSteps missing %q:\n%s", want, out)
		}
	}
	// The non-guided step uses the unguided marker.
	if !strings.Contains(out, "·") {
		t.Errorf("renderSteps missing unguided marker:\n%s", out)
	}
}

func TestRenderSteps_Empty(t *testing.T) {
	out := renderSteps(nil, 40)
	if !strings.Contains(strings.ToLower(out), "no steps") {
		t.Errorf("empty steps should say so, got:\n%s", out)
	}
}

func TestUpdate_SessionModeToggle(t *testing.T) {
	usagePath := createTestDBPair(t)
	dir := filepath.Dir(usagePath)
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	insertRichSession(t, dir, "s1", "/test", "claude-opus-4-8", "do a thing", now, 3, 2)
	insertStep(t, dir, "s1", "Bash", "ls -la", "", "", now)

	m := newModel(usagePath, 30, "")

	// Enter session mode with 's'.
	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = tm.(model)
	if !m.sessionMode {
		t.Fatal("expected sessionMode after 's'")
	}
	if len(m.sessions) != 1 || m.sessions[0].SessionID != "s1" {
		t.Fatalf("expected 1 loaded session s1, got %+v", m.sessions)
	}
	if len(m.steps) != 1 || m.steps[0].Command != "ls -la" {
		t.Fatalf("expected steps for s1 loaded, got %+v", m.steps)
	}

	// j/k stay in range (single session).
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = tm.(model)
	if m.sessionSel != 0 {
		t.Errorf("sessionSel should clamp to 0 with one session, got %d", m.sessionSel)
	}

	// Esc exits.
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = tm.(model)
	if m.sessionMode {
		t.Error("expected sessionMode=false after esc")
	}
}
