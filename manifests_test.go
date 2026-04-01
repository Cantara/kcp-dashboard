package main

import (
	"os"
	"path/filepath"
	"testing"
)

// ── Test fixtures ─────────────────────────────────────────────────────────────

const mvnYAML = `
command: mvn
platform: all
description: "Apache Maven build tool"
syntax:
  usage: "mvn [options] [<goal(s)>]"
  key_flags:
    - flag: "test"
      description: "Compile and run tests"
      use_when: "Verify the build after changes"
    - flag: "package"
      description: "Compile, test, and package into JAR/WAR"
  preferred_invocations:
    - invocation: "mvn test"
      use_when: "Run all tests"
    - invocation: "mvn clean package -DskipTests"
      use_when: "Fast build"
output_schema:
  enable_filter: true
  noise_patterns:
    - pattern: "^\\[INFO\\] Scanning"
      reason: "Boilerplate startup"
    - pattern: "^\\[INFO\\] --- "
      reason: "Plugin execution headers"
    - pattern: "^\\[INFO\\]\\s*$"
      reason: "Blank INFO lines"
  max_lines: 5
  truncation_message: "... {remaining} more Maven lines."
`

const kubectlApplyYAML = `
command: kubectl
subcommand: apply
platform: all
description: "Apply a configuration to a resource"
syntax:
  key_flags:
    - flag: "-f <file>"
      description: "Filename or directory of resource manifests"
  preferred_invocations:
    - invocation: "kubectl apply -f manifest.yaml"
      use_when: "Deploy a manifest"
output_schema:
  enable_filter: false
`

const gitYAML = `
command: git
platform: all
description: "Git version control"
syntax:
  key_flags:
    - flag: "status"
      description: "Show working tree status"
output_schema:
  enable_filter: false
`

// writeFixtures creates YAML files in a temp dir and returns the dir path.
func writeFixtures(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	fixtures := map[string]string{
		"mvn.yaml":            mvnYAML,
		"kubectl-apply.yaml":  kubectlApplyYAML,
		"git.yaml":            gitYAML,
	}
	for name, content := range fixtures {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	return dir
}

// ── loadManifests ─────────────────────────────────────────────────────────────

func TestLoadManifests_CountsFiles(t *testing.T) {
	dir := writeFixtures(t)
	store, err := loadManifests(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.Size() != 3 {
		t.Errorf("expected 3 manifests, got %d", store.Size())
	}
}

func TestLoadManifests_MissingDir(t *testing.T) {
	store, err := loadManifests("/nonexistent/path/xyz")
	if err == nil {
		t.Error("expected error for missing dir, got nil")
	}
	if store.Size() != 0 {
		t.Errorf("expected empty store on error, got %d", store.Size())
	}
}

func TestLoadManifests_IgnoresNonYAML(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# docs"), 0644)
	os.WriteFile(filepath.Join(dir, "mvn.yaml"), []byte(mvnYAML), 0644)

	store, err := loadManifests(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.Size() != 1 {
		t.Errorf("expected 1 manifest, got %d", store.Size())
	}
}

// ── Lookup ────────────────────────────────────────────────────────────────────

func TestLookup_BaseCommand(t *testing.T) {
	store, _ := loadManifests(writeFixtures(t))

	m := store.Lookup("mvn clean package")
	if m == nil {
		t.Fatal("expected manifest for mvn, got nil")
	}
	if m.Command != "mvn" {
		t.Errorf("expected command mvn, got %s", m.Command)
	}
}

func TestLookup_BaseCommandWithSubcommand(t *testing.T) {
	store, _ := loadManifests(writeFixtures(t))

	m := store.Lookup("kubectl apply -f deploy.yaml")
	if m == nil {
		t.Fatal("expected manifest for kubectl-apply, got nil")
	}
	if m.Subcommand != "apply" {
		t.Errorf("expected subcommand apply, got %q", m.Subcommand)
	}
}

func TestLookup_BaseCommandFallback(t *testing.T) {
	// kubectl without subcommand should fall back to... nothing (no base kubectl manifest)
	store, _ := loadManifests(writeFixtures(t))

	m := store.Lookup("kubectl get pods")
	// "kubectl-get" not in fixtures; "kubectl" not in fixtures → nil
	if m != nil {
		t.Errorf("expected nil for unknown kubectl subcommand, got %+v", m)
	}
}

func TestLookup_StripPathPrefix(t *testing.T) {
	store, _ := loadManifests(writeFixtures(t))

	m := store.Lookup("/usr/bin/mvn test")
	if m == nil {
		t.Fatal("expected manifest after stripping path prefix")
	}
	if m.Command != "mvn" {
		t.Errorf("expected command mvn, got %s", m.Command)
	}
}

func TestLookup_UnknownCommand(t *testing.T) {
	store, _ := loadManifests(writeFixtures(t))

	if m := store.Lookup("grep -r foo ."); m != nil {
		t.Errorf("expected nil for unknown command, got %+v", m)
	}
}

func TestLookup_EmptyCommand(t *testing.T) {
	store, _ := loadManifests(writeFixtures(t))

	if m := store.Lookup(""); m != nil {
		t.Errorf("expected nil for empty command, got %+v", m)
	}
}

// ── manifestKey ───────────────────────────────────────────────────────────────

func TestManifestKey_WithSubcommand(t *testing.T) {
	m := &Manifest{Command: "kubectl", Subcommand: "apply"}
	if got := manifestKey(m); got != "kubectl-apply" {
		t.Errorf("expected kubectl-apply, got %s", got)
	}
}

func TestManifestKey_WithoutSubcommand(t *testing.T) {
	m := &Manifest{Command: "mvn"}
	if got := manifestKey(m); got != "mvn" {
		t.Errorf("expected mvn, got %s", got)
	}
}

// ── buildAdditionalContext ────────────────────────────────────────────────────

func TestBuildAdditionalContext_ContainsDescription(t *testing.T) {
	store, _ := loadManifests(writeFixtures(t))
	m := store.Lookup("mvn test")

	ctx := buildAdditionalContext(m)
	if ctx == "" {
		t.Fatal("expected non-empty context")
	}
	if !contains(ctx, "Apache Maven build tool") {
		t.Errorf("context missing description:\n%s", ctx)
	}
}

func TestBuildAdditionalContext_ContainsFlags(t *testing.T) {
	store, _ := loadManifests(writeFixtures(t))
	m := store.Lookup("mvn test")

	ctx := buildAdditionalContext(m)
	if !contains(ctx, "package") {
		t.Errorf("context missing key flag:\n%s", ctx)
	}
	if !contains(ctx, "Verify the build after changes") {
		t.Errorf("context missing use_when:\n%s", ctx)
	}
}

func TestBuildAdditionalContext_ContainsInvocations(t *testing.T) {
	store, _ := loadManifests(writeFixtures(t))
	m := store.Lookup("mvn test")

	ctx := buildAdditionalContext(m)
	if !contains(ctx, "mvn clean package -DskipTests") {
		t.Errorf("context missing preferred invocation:\n%s", ctx)
	}
}

func TestBuildAdditionalContext_SubcommandHeader(t *testing.T) {
	store, _ := loadManifests(writeFixtures(t))
	m := store.Lookup("kubectl apply -f x.yaml")

	ctx := buildAdditionalContext(m)
	if !contains(ctx, "kubectl apply:") {
		t.Errorf("expected 'kubectl apply:' header in context:\n%s", ctx)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
