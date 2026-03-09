package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMatcherGroup_Matches(t *testing.T) {
	tests := []struct {
		name    string
		matcher string
		path    string
		want    bool
	}{
		{"empty matcher matches all", "", "anything.md", true},
		{"star matches all", "*", "anything.md", true},
		{"regex matches", `.*\.md$`, "notes/daily.md", true},
		{"regex no match", `.*\.md$`, "notes/daily.txt", false},
		{"invalid regex never matches", `[invalid`, "anything", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mg := &MatcherGroup{Matcher: tt.matcher}
			if got := mg.Matches(tt.path); got != tt.want {
				t.Errorf("Matches(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestLoadHooks_MissingFiles(t *testing.T) {
	cfg, err := LoadHooks("/nonexistent/global.json", "/nonexistent/local.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Hooks) != 0 {
		t.Errorf("expected empty hooks, got %d events", len(cfg.Hooks))
	}
}

func TestLoadHooks_SingleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	os.WriteFile(path, []byte(`{
		"hooks": {
			"PostFileReceived": [
				{
					"matcher": ".*\\.md$",
					"hooks": [{"type": "command", "command": "echo hello"}]
				}
			]
		}
	}`), 0o644)

	cfg, err := LoadHooks(path, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	groups := cfg.Hooks[PostFileReceived]
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if len(groups[0].Hooks) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(groups[0].Hooks))
	}
	if groups[0].Hooks[0].Command != "echo hello" {
		t.Errorf("unexpected command: %s", groups[0].Hooks[0].Command)
	}
}

func TestLoadHooks_MergesBothFiles(t *testing.T) {
	dir := t.TempDir()

	global := filepath.Join(dir, "global.json")
	os.WriteFile(global, []byte(`{
		"hooks": {
			"PrePull": [{"hooks": [{"type": "command", "command": "global-cmd"}]}]
		}
	}`), 0o644)

	local := filepath.Join(dir, "local.json")
	os.WriteFile(local, []byte(`{
		"hooks": {
			"PrePull": [{"hooks": [{"type": "command", "command": "local-cmd"}]}],
			"PrePush": [{"hooks": [{"type": "command", "command": "push-cmd"}]}]
		}
	}`), 0o644)

	cfg, err := LoadHooks(global, local)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// PrePull should have groups from both files.
	if got := len(cfg.Hooks[PrePull]); got != 2 {
		t.Errorf("expected 2 PrePull groups, got %d", got)
	}
	// PrePush should have group from local only.
	if got := len(cfg.Hooks[PrePush]); got != 1 {
		t.Errorf("expected 1 PrePush group, got %d", got)
	}
}

func TestLoadHooks_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte(`{not json}`), 0o644)

	_, err := LoadHooks(path, "")
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestLoadHooks_DefaultTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	os.WriteFile(path, []byte(`{
		"hooks": {
			"PrePush": [
				{"hooks": [{"type": "command", "command": "test", "timeout": 60}]}
			]
		}
	}`), 0o644)

	cfg, err := LoadHooks(path, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h := cfg.Hooks[PrePush][0].Hooks[0]
	if h.Timeout != 60 {
		t.Errorf("expected timeout 60, got %d", h.Timeout)
	}
}
