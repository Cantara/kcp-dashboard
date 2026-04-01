package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const hookPort = 7734

// ── HTTP request / response types ────────────────────────────────────────────

type hookRequest struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
	SessionID string         `json:"session_id"`
	CWD       string         `json:"cwd"`
}

type hookResponse struct {
	HookSpecificOutput hookOutput `json:"hookSpecificOutput"`
}

type hookOutput struct {
	HookEventName      string            `json:"hookEventName"`
	PermissionDecision string            `json:"permissionDecision"`
	AdditionalContext  string            `json:"additionalContext,omitempty"`
	UpdatedInput       map[string]string `json:"updatedInput,omitempty"`
}

// ── Usage event writer ────────────────────────────────────────────────────────

type usageWriter struct {
	mu  sync.Mutex
	db  *sql.DB
}

func newUsageWriter(dbPath string) (*usageWriter, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS usage_events (
		event_type            TEXT,
		unit_id               TEXT,
		token_estimate        INTEGER,
		manifest_token_total  INTEGER,
		timestamp             TEXT,
		result_count          INTEGER,
		query                 TEXT,
		project               TEXT
	)`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &usageWriter{db: db}, nil
}

func (u *usageWriter) writeInject(unitID, project string, tokenEstimate int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.db.Exec(
		`INSERT INTO usage_events (event_type, unit_id, token_estimate, timestamp, project)
		 VALUES ('inject', ?, ?, ?, ?)`,
		unitID,
		tokenEstimate,
		time.Now().UTC().Format(time.RFC3339),
		project,
	)
}

func (u *usageWriter) writeFilter(unitID, project string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.db.Exec(
		`INSERT INTO usage_events (event_type, unit_id, timestamp, project)
		 VALUES ('filter', ?, ?, ?)`,
		unitID,
		time.Now().UTC().Format(time.RFC3339),
		project,
	)
}

// ── Serve command ─────────────────────────────────────────────────────────────

func runServe() {
	kcpDir := filepath.Join(os.Getenv("HOME"), ".kcp")
	manifestDir := filepath.Join(kcpDir, "commands")

	store, err := loadManifests(manifestDir)
	if err != nil {
		log.Printf("[kcp-hook] warning: manifests not loaded from %s: %v", manifestDir, err)
		log.Printf("[kcp-hook] install kcp-commands to populate %s", manifestDir)
		store = &ManifestStore{byKey: make(map[string]*Manifest)}
	} else {
		log.Printf("[kcp-hook] loaded %d manifests from %s", store.Size(), manifestDir)
	}

	dbPath := filepath.Join(kcpDir, "usage.db")
	usage, err := newUsageWriter(dbPath)
	if err != nil {
		log.Printf("[kcp-hook] warning: could not open usage.db at %s: %v", dbPath, err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"version":%q,"backend":"go"}`, version)
	})
	mux.HandleFunc("/hook", func(w http.ResponseWriter, r *http.Request) {
		handleHook(w, r, store, usage)
	})
	mux.HandleFunc("/filter/", func(w http.ResponseWriter, r *http.Request) {
		handleFilter(w, r, store, usage)
	})

	addr := fmt.Sprintf("localhost:%d", hookPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("[kcp-hook] cannot bind to %s — is another kcp daemon already running? (%v)", addr, err)
	}

	log.Printf("[kcp-hook] kcp-dashboard serve v%s — listening on http://%s (%d manifests)", version, addr, store.Size())

	srv := &http.Server{
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	if err := srv.Serve(ln); err != nil {
		log.Fatalf("[kcp-hook] %v", err)
	}
}

// ── /hook handler (Phase A + Phase B wrapping) ────────────────────────────────

func handleHook(w http.ResponseWriter, r *http.Request, store *ManifestStore, usage *usageWriter) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	var req hookRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Only handle Bash tool calls
	if req.ToolName != "Bash" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	command, _ := req.ToolInput["command"].(string)
	if command == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	manifest := store.Lookup(command)
	if manifest == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	key := manifestKey(manifest)
	out := hookOutput{
		HookEventName:      "PreToolUse",
		PermissionDecision: "allow",
	}

	// Phase A: inject guidance context
	ctx := buildAdditionalContext(manifest)
	if ctx != "" {
		out.AdditionalContext = ctx
		// Estimate tokens (~4 chars per token) for usage tracking
		if usage != nil {
			usage.writeInject(key, req.CWD, len(ctx)/4)
		}
	}

	// Phase B: wrap command for output filtering
	if manifest.OutputSchema.EnableFilter {
		out.UpdatedInput = map[string]string{
			"command": fmt.Sprintf(
				`%s | curl -s -X POST "http://localhost:%d/filter/%s" --data-binary @-`,
				command, hookPort, key,
			),
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(hookResponse{HookSpecificOutput: out})
}

// ── /filter/{key} handler (Phase B output filtering) ─────────────────────────

func handleFilter(w http.ResponseWriter, r *http.Request, store *ManifestStore, usage *usageWriter) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	key := strings.TrimPrefix(r.URL.Path, "/filter/")
	if key == "" {
		http.Error(w, "missing manifest key", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20)) // 4 MB max
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	manifest := store.Get(key)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	if manifest == nil || !manifest.OutputSchema.EnableFilter {
		// No manifest or filter disabled — pass through unchanged
		w.Write(body)
		return
	}

	if usage != nil {
		usage.writeFilter(key, "")
	}

	filtered := applyOutputFilter(string(body), &manifest.OutputSchema)
	fmt.Fprint(w, filtered)
}

// ── Phase B noise filtering ───────────────────────────────────────────────────

func applyOutputFilter(output string, schema *OutputSchema) string {
	// Compile noise patterns once
	patterns := make([]*regexp.Regexp, 0, len(schema.NoisePatterns))
	for _, np := range schema.NoisePatterns {
		if re, err := regexp.Compile(np.Pattern); err == nil {
			patterns = append(patterns, re)
		}
	}

	lines := strings.Split(output, "\n")
	kept := make([]string, 0, len(lines))

	for _, line := range lines {
		if matchesNoise(line, patterns) {
			continue
		}
		// Collapse consecutive blank lines produced by noise removal
		if strings.TrimSpace(line) == "" && len(kept) > 0 && kept[len(kept)-1] == "" {
			continue
		}
		kept = append(kept, line)
	}

	// Apply max_lines truncation
	if schema.MaxLines > 0 && len(kept) > schema.MaxLines {
		remaining := len(kept) - schema.MaxLines
		kept = kept[:schema.MaxLines]
		msg := schema.TruncationMessage
		if msg == "" {
			msg = fmt.Sprintf("... %d more lines.", remaining)
		} else {
			msg = strings.ReplaceAll(msg, "{remaining}", fmt.Sprintf("%d", remaining))
		}
		kept = append(kept, msg)
	}

	return strings.Join(kept, "\n")
}

func matchesNoise(line string, patterns []*regexp.Regexp) bool {
	for _, re := range patterns {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}
