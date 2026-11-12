package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// loadTestStore builds a ManifestStore from the shared test fixtures.
func loadTestStore(t *testing.T) *ManifestStore {
	t.Helper()
	store, err := loadManifests(writeFixtures(t))
	if err != nil {
		t.Fatalf("loadManifests: %v", err)
	}
	return store
}

// ── handleHook ────────────────────────────────────────────────────────────────

func postHook(t *testing.T, store *ManifestStore, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleHook(rr, req, store, nil, hookPort) // nil usage writer — no DB needed in tests
	return rr
}

// The hook must never make Claude Code's permission decision: "allow" skips the permission prompt for
// the whole command, and only the first word is used to find a manifest, so "mvn test; rm -rf x" ran
// without a prompt because mvn has one.
func TestHandleHook_CompoundCommand_NoPermissionDecision(t *testing.T) {
	store := loadTestStore(t)
	rr := postHook(t, store, `{"tool_name":"Bash","tool_input":{"command":"mvn test; rm -rf /tmp/kcp-test-does-not-exist"},"session_id":"s1","cwd":"/tmp"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (context is still added), got %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "permissionDecision") {
		t.Errorf("the hook must not decide permission, got: %s", rr.Body.String())
	}
}

func TestHandleHook_NonBashTool_Returns204(t *testing.T) {
	store := loadTestStore(t)
	rr := postHook(t, store, `{"tool_name":"Read","tool_input":{"file_path":"/tmp/x"},"session_id":"s1","cwd":"/tmp"}`)
	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rr.Code)
	}
}

func TestHandleHook_UnknownCommand_Returns204(t *testing.T) {
	store := loadTestStore(t)
	rr := postHook(t, store, `{"tool_name":"Bash","tool_input":{"command":"grep -r foo ."},"session_id":"s1","cwd":"/tmp"}`)
	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rr.Code)
	}
}

func TestHandleHook_EmptyCommand_Returns204(t *testing.T) {
	store := loadTestStore(t)
	rr := postHook(t, store, `{"tool_name":"Bash","tool_input":{"command":""},"session_id":"s1","cwd":"/tmp"}`)
	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rr.Code)
	}
}

func TestHandleHook_KnownCommand_Returns200WithContext(t *testing.T) {
	store := loadTestStore(t)
	rr := postHook(t, store, `{"tool_name":"Bash","tool_input":{"command":"mvn clean package"},"session_id":"s1","cwd":"/tmp"}`)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\nbody: %s", rr.Code, rr.Body.String())
	}

	var resp hookResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	out := resp.HookSpecificOutput
	if out.HookEventName != "PreToolUse" {
		t.Errorf("expected hookEventName=PreToolUse, got %q", out.HookEventName)
	}
	if strings.Contains(rr.Body.String(), "permissionDecision") {
		t.Errorf("the hook must not decide permission, got: %s", rr.Body.String())
	}
	if out.AdditionalContext == "" {
		t.Error("expected non-empty additionalContext")
	}
	if !containsStr(out.AdditionalContext, "Apache Maven build tool") {
		t.Errorf("additionalContext missing description:\n%s", out.AdditionalContext)
	}
}

// installFilter points filterCommand at an executable stand-in for ~/.kcp/kcp-filter for one test.
func installFilter(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "kcp-filter")
	if err := os.WriteFile(f, []byte("#!/bin/sh\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := filterCommand
	filterCommand = f
	t.Cleanup(func() { filterCommand = saved })
	return f
}

func TestHandleHook_FilterEnabled_PipesIntoKcpFilter(t *testing.T) {
	f := installFilter(t, filepath.Join(t.TempDir(), ".kcp"))
	store := loadTestStore(t)
	rr := postHook(t, store, `{"tool_name":"Bash","tool_input":{"command":"mvn test"},"session_id":"s1","cwd":"/tmp"}`)
	var resp hookResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	want := "mvn test | " + f + " mvn " + strconv.Itoa(hookPort)
	if got := resp.HookSpecificOutput.UpdatedInput["command"]; got != want {
		t.Errorf("updatedInput command:\n got %q\nwant %q", got, want)
	}
}

func TestHandleHook_FilterNotInstalled_CommandUnchanged(t *testing.T) {
	saved := filterCommand
	filterCommand = filepath.Join(t.TempDir(), "nowhere", "kcp-filter")
	t.Cleanup(func() { filterCommand = saved })
	store := loadTestStore(t)
	rr := postHook(t, store, `{"tool_name":"Bash","tool_input":{"command":"mvn test"},"session_id":"s1","cwd":"/tmp"}`)
	var resp hookResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.HookSpecificOutput.UpdatedInput != nil {
		t.Errorf("a missing filter must leave the command as is, got %v", resp.HookSpecificOutput.UpdatedInput)
	}
}

func TestFiltered_PathWithWhitespace_NotUsed(t *testing.T) {
	installFilter(t, filepath.Join(t.TempDir(), "a user", ".kcp"))
	if got := filtered("mvn test", "mvn", hookPort); got != "" {
		t.Errorf("a path that needs quoting cannot match the permission rule; got %q", got)
	}
}

func TestHandleHook_FilterEnabled_WrapsCommand(t *testing.T) {
	installFilter(t, filepath.Join(t.TempDir(), ".kcp"))
	store := loadTestStore(t)
	rr := postHook(t, store, `{"tool_name":"Bash","tool_input":{"command":"mvn test"},"session_id":"s1","cwd":"/tmp"}`)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp hookResponse
	json.NewDecoder(rr.Body).Decode(&resp)

	out := resp.HookSpecificOutput
	if out.UpdatedInput == nil {
		t.Fatal("expected updatedInput for filter-enabled manifest, got nil")
	}
	wrapped := out.UpdatedInput["command"]
	if !containsStr(wrapped, "kcp-filter mvn ") {
		t.Errorf("expected updatedInput to pipe through kcp-filter mvn:\n%s", wrapped)
	}
	if containsStr(wrapped, "curl") {
		t.Errorf("the rewrite must not need a broad curl permission:\n%s", wrapped)
	}
	if !containsStr(wrapped, "mvn test") {
		t.Errorf("expected original command preserved in updatedInput:\n%s", wrapped)
	}
}

func TestHandleHook_FilterDisabled_NoUpdatedInput(t *testing.T) {
	store := loadTestStore(t)
	// kubectl-apply has enable_filter: false
	rr := postHook(t, store, `{"tool_name":"Bash","tool_input":{"command":"kubectl apply -f x.yaml"},"session_id":"s1","cwd":"/tmp"}`)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp hookResponse
	json.NewDecoder(rr.Body).Decode(&resp)

	if resp.HookSpecificOutput.UpdatedInput != nil {
		t.Errorf("expected no updatedInput for filter-disabled manifest, got %v", resp.HookSpecificOutput.UpdatedInput)
	}
}

func TestHandleHook_InvalidJSON_Returns400(t *testing.T) {
	store := loadTestStore(t)
	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader("not json"))
	rr := httptest.NewRecorder()
	handleHook(rr, req, store, nil, hookPort)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestHandleHook_WrongMethod_Returns405(t *testing.T) {
	store := loadTestStore(t)
	req := httptest.NewRequest(http.MethodGet, "/hook", nil)
	rr := httptest.NewRecorder()
	handleHook(rr, req, store, nil, hookPort)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

// ── handleFilter ──────────────────────────────────────────────────────────────

func postFilter(t *testing.T, store *ManifestStore, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/filter/"+key, strings.NewReader(body))
	rr := httptest.NewRecorder()
	handleFilter(rr, req, store, nil)
	return rr
}

const mvnOutput = `[INFO] Scanning for projects...
[INFO] --- maven-compiler-plugin:3.8.1:compile ---
[INFO]
[ERROR] COMPILATION ERROR
[ERROR] src/App.java:[10] method not found
[INFO]
[INFO] BUILD FAILURE`

func TestHandleFilter_RemovesNoisePatterns(t *testing.T) {
	store := loadTestStore(t)
	rr := postFilter(t, store, "mvn", mvnOutput)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	result := rr.Body.String()
	if containsStr(result, "[INFO] Scanning for projects") {
		t.Error("noise line should have been removed")
	}
	if containsStr(result, "[INFO] --- maven-compiler-plugin") {
		t.Error("plugin header should have been removed")
	}
	if !containsStr(result, "[ERROR] COMPILATION ERROR") {
		t.Error("error line should be retained")
	}
}

func TestHandleFilter_Truncates(t *testing.T) {
	store := loadTestStore(t)
	// mvn fixture has max_lines: 5
	longOutput := strings.Repeat("[ERROR] line\n", 20)
	rr := postFilter(t, store, "mvn", longOutput)

	result := rr.Body.String()
	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	// Should be truncated: max_lines lines + 1 truncation message
	if len(lines) > 6 {
		t.Errorf("expected at most 6 lines after truncation, got %d", len(lines))
	}
	if !containsStr(result, "more Maven lines") {
		t.Errorf("expected truncation message, got:\n%s", result)
	}
}

func TestHandleFilter_TruncationMessageInterpolation(t *testing.T) {
	store := loadTestStore(t)
	longOutput := strings.Repeat("[ERROR] line\n", 10)
	rr := postFilter(t, store, "mvn", longOutput)

	result := rr.Body.String()
	// {remaining} should be replaced with a number, not the literal string
	if containsStr(result, "{remaining}") {
		t.Errorf("{remaining} placeholder was not replaced:\n%s", result)
	}
}

func TestHandleFilter_UnknownKey_PassesThrough(t *testing.T) {
	store := loadTestStore(t)
	input := "some output line"
	rr := postFilter(t, store, "unknowntool", input)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if rr.Body.String() != input {
		t.Errorf("expected pass-through for unknown key, got %q", rr.Body.String())
	}
}

func TestHandleFilter_MissingKey_Returns400(t *testing.T) {
	store := loadTestStore(t)
	req := httptest.NewRequest(http.MethodPost, "/filter/", strings.NewReader("output"))
	rr := httptest.NewRecorder()
	handleFilter(rr, req, store, nil)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

// ── applyOutputFilter ─────────────────────────────────────────────────────────

func TestApplyOutputFilter_CollapsesBlanks(t *testing.T) {
	schema := &OutputSchema{
		NoisePatterns: []NoisePattern{{Pattern: "^NOISE"}},
		MaxLines:      0,
	}
	input := "keep\nNOISE line\n\nNOISE line\n\nkeep2"
	result := applyOutputFilter(input, schema)

	// Should not have two consecutive blank lines
	if containsStr(result, "\n\n\n") {
		t.Errorf("consecutive blanks not collapsed:\n%s", result)
	}
	if !containsStr(result, "keep") || !containsStr(result, "keep2") {
		t.Errorf("keep lines missing from output:\n%s", result)
	}
}

func TestApplyOutputFilter_NoMaxLines_KeepsAll(t *testing.T) {
	schema := &OutputSchema{MaxLines: 0}
	input := strings.Repeat("line\n", 100)
	result := applyOutputFilter(input, schema)
	if strings.Count(result, "line") != 100 {
		t.Errorf("expected 100 lines when max_lines=0")
	}
}
