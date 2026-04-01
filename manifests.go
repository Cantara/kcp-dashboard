package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ── YAML manifest types ───────────────────────────────────────────────────────

type Manifest struct {
	Command      string       `yaml:"command"`
	Subcommand   string       `yaml:"subcommand"`
	Platform     string       `yaml:"platform"`
	Description  string       `yaml:"description"`
	Syntax       Syntax       `yaml:"syntax"`
	OutputSchema OutputSchema `yaml:"output_schema"`
}

type Syntax struct {
	Usage                string       `yaml:"usage"`
	KeyFlags             []KeyFlag    `yaml:"key_flags"`
	PreferredInvocations []Invocation `yaml:"preferred_invocations"`
}

type KeyFlag struct {
	Flag        string `yaml:"flag"`
	Description string `yaml:"description"`
	UseWhen     string `yaml:"use_when"`
}

type Invocation struct {
	Invocation string `yaml:"invocation"`
	UseWhen    string `yaml:"use_when"`
}

type OutputSchema struct {
	EnableFilter      bool           `yaml:"enable_filter"`
	NoisePatterns     []NoisePattern `yaml:"noise_patterns"`
	MaxLines          int            `yaml:"max_lines"`
	TruncationMessage string         `yaml:"truncation_message"`
}

type NoisePattern struct {
	Pattern string `yaml:"pattern"`
	Reason  string `yaml:"reason"`
}

// ── ManifestStore ─────────────────────────────────────────────────────────────

// ManifestStore holds all loaded manifests indexed by canonical key.
// Key format: "command" or "command-subcommand" (e.g. "kubectl-apply").
type ManifestStore struct {
	byKey map[string]*Manifest
}

func loadManifests(dir string) (*ManifestStore, error) {
	store := &ManifestStore{byKey: make(map[string]*Manifest)}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return store, fmt.Errorf("reading %s: %w", dir, err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var m Manifest
		if err := yaml.Unmarshal(data, &m); err != nil || m.Command == "" {
			continue
		}
		store.byKey[manifestKey(&m)] = &m
	}

	return store, nil
}

// manifestKey returns the canonical lookup key for a manifest.
func manifestKey(m *Manifest) string {
	if m.Subcommand != "" {
		return m.Command + "-" + m.Subcommand
	}
	return m.Command
}

// Lookup finds the best manifest for a raw shell command string.
// It tries "base-subcommand" first, then "base".
func (s *ManifestStore) Lookup(command string) *Manifest {
	words := strings.Fields(command)
	if len(words) == 0 {
		return nil
	}

	// Strip any path prefix: /usr/bin/kubectl → kubectl
	base := filepath.Base(words[0])

	// Try base-subcommand (second word, only if it looks like a subcommand)
	if len(words) >= 2 && !strings.HasPrefix(words[1], "-") {
		if m, ok := s.byKey[base+"-"+words[1]]; ok {
			return m
		}
	}

	// Fall back to base command only
	if m, ok := s.byKey[base]; ok {
		return m
	}

	return nil
}

// Get returns the manifest for an exact key (used by /filter/{key}).
func (s *ManifestStore) Get(key string) *Manifest {
	return s.byKey[key]
}

// Size returns the number of loaded manifests.
func (s *ManifestStore) Size() int {
	return len(s.byKey)
}

// ── additionalContext builder ─────────────────────────────────────────────────

// buildAdditionalContext formats a manifest into the context string
// injected into Claude's additionalContext before a Bash call.
func buildAdditionalContext(m *Manifest) string {
	var sb strings.Builder

	// Header line
	if m.Subcommand != "" {
		fmt.Fprintf(&sb, "%s %s: %s\n", m.Command, m.Subcommand, m.Description)
	} else {
		fmt.Fprintf(&sb, "%s: %s\n", m.Command, m.Description)
	}

	// Key flags
	if len(m.Syntax.KeyFlags) > 0 {
		sb.WriteString("\nKey flags:\n")
		for _, f := range m.Syntax.KeyFlags {
			sb.WriteString("  ")
			sb.WriteString(f.Flag)
			sb.WriteString(" — ")
			sb.WriteString(f.Description)
			if f.UseWhen != "" {
				sb.WriteString(" (")
				sb.WriteString(f.UseWhen)
				sb.WriteString(")")
			}
			sb.WriteString("\n")
		}
	}

	// Preferred invocations
	if len(m.Syntax.PreferredInvocations) > 0 {
		sb.WriteString("\nPreferred invocations:\n")
		for _, inv := range m.Syntax.PreferredInvocations {
			sb.WriteString("  ")
			sb.WriteString(inv.Invocation)
			if inv.UseWhen != "" {
				sb.WriteString(" — ")
				sb.WriteString(inv.UseWhen)
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}
