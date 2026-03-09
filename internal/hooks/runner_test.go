package hooks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunner_Fire_NilRunner(t *testing.T) {
	var r *Runner
	if err := r.Fire(context.Background(), Event{Event: PrePull}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunner_Fire_NilConfig(t *testing.T) {
	r := NewRunner(nil, "vault", "id", "/tmp")
	if err := r.Fire(context.Background(), Event{Event: PrePull}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunner_Fire_NoMatchingEvent(t *testing.T) {
	cfg := &HooksConfig{Hooks: map[EventType][]MatcherGroup{
		PrePull: {{Hooks: []HookHandler{{Type: "command", Command: "false"}}}},
	}}
	r := NewRunner(cfg, "vault", "id", "/tmp")
	// Fire a different event — no hooks should run.
	if err := r.Fire(context.Background(), Event{Event: PrePush}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunner_Fire_SuccessfulHook(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")

	cfg := &HooksConfig{Hooks: map[EventType][]MatcherGroup{
		PostPull: {{Hooks: []HookHandler{{
			Type:    "command",
			Command: "touch " + marker,
		}}}},
	}}
	r := NewRunner(cfg, "vault", "id", dir)

	if err := r.Fire(context.Background(), Event{Event: PostPull}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker file not created: %v", err)
	}
}

func TestRunner_Fire_ExitCode2_Blocks(t *testing.T) {
	cfg := &HooksConfig{Hooks: map[EventType][]MatcherGroup{
		PrePush: {{Hooks: []HookHandler{{
			Type:    "command",
			Command: "echo 'validation failed' >&2; exit 2",
		}}}},
	}}
	r := NewRunner(cfg, "vault", "id", t.TempDir())

	err := r.Fire(context.Background(), Event{Event: PrePush})
	if err == nil {
		t.Fatal("expected error from blocking hook")
	}

	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("expected BlockedError, got %T: %v", err, err)
	}
	if !strings.Contains(blocked.Reason, "validation failed") {
		t.Errorf("expected reason to contain 'validation failed', got %q", blocked.Reason)
	}
}

func TestRunner_Fire_ExitCode1_NonBlocking(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "second-ran")

	cfg := &HooksConfig{Hooks: map[EventType][]MatcherGroup{
		PostPull: {
			{Hooks: []HookHandler{
				{Type: "command", Command: "exit 1"},
				{Type: "command", Command: "touch " + marker},
			}},
		},
	}}
	r := NewRunner(cfg, "vault", "id", dir)

	err := r.Fire(context.Background(), Event{Event: PostPull})
	if err != nil {
		t.Fatalf("unexpected error from non-blocking hook: %v", err)
	}

	// Second hook should still run.
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("second hook did not run: %v", err)
	}
}

func TestRunner_Fire_FileMatcherFilters(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")

	cfg := &HooksConfig{Hooks: map[EventType][]MatcherGroup{
		PostFileReceived: {{
			Matcher: `.*\.md$`,
			Hooks:   []HookHandler{{Type: "command", Command: "touch " + marker}},
		}},
	}}
	r := NewRunner(cfg, "vault", "id", dir)

	// Non-matching file — hook should NOT run.
	err := r.Fire(context.Background(), Event{
		Event: PostFileReceived,
		File:  &FileInfo{Path: "image.png"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("hook ran for non-matching file")
	}

	// Matching file — hook should run.
	err = r.Fire(context.Background(), Event{
		Event: PostFileReceived,
		File:  &FileInfo{Path: "notes/daily.md"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("hook did not run for matching file: %v", err)
	}
}

func TestRunner_Fire_StdinJSON(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "stdin.json")

	cfg := &HooksConfig{Hooks: map[EventType][]MatcherGroup{
		PostFileReceived: {{Hooks: []HookHandler{{
			Type:    "command",
			Command: "cat > " + output,
		}}}},
	}}
	r := NewRunner(cfg, "test-vault", "vault-123", dir)

	err := r.Fire(context.Background(), Event{
		Event: PostFileReceived,
		File:  &FileInfo{Path: "note.md", LocalPath: "/tmp/note.md", Size: 42, Hash: "abc"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("failed to read captured stdin: %v", err)
	}

	s := string(data)
	for _, want := range []string{`"event":"PostFileReceived"`, `"vault_name":"test-vault"`, `"vault_id":"vault-123"`, `"path":"note.md"`, `"size":42`} {
		if !strings.Contains(s, want) {
			t.Errorf("stdin JSON missing %q, got: %s", want, s)
		}
	}
}

func TestRunner_Fire_EnvVars(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "env.txt")

	cfg := &HooksConfig{Hooks: map[EventType][]MatcherGroup{
		PostFileReceived: {{Hooks: []HookHandler{{
			Type:    "command",
			Command: "env > " + output,
		}}}},
	}}
	r := NewRunner(cfg, "My Vault", "v-123", dir)

	err := r.Fire(context.Background(), Event{
		Event: PostFileReceived,
		File:  &FileInfo{Path: "test.md"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("failed to read env: %v", err)
	}

	s := string(data)
	for _, want := range []string{
		"OBSYNC_EVENT=PostFileReceived",
		"OBSYNC_VAULT_NAME=My Vault",
		"OBSYNC_VAULT_ID=v-123",
		"OBSYNC_FILE_PATH=test.md",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("env missing %q", want)
		}
	}
}

func TestRunner_Fire_Timeout(t *testing.T) {
	cfg := &HooksConfig{Hooks: map[EventType][]MatcherGroup{
		PrePull: {{Hooks: []HookHandler{{
			Type:    "command",
			Command: "sleep 10",
			Timeout: 1,
		}}}},
	}}
	r := NewRunner(cfg, "vault", "id", t.TempDir())

	// Should not block for 10 seconds — timeout should kill it.
	// Non-zero exit from timeout is non-blocking (not exit code 2).
	err := r.Fire(context.Background(), Event{Event: PrePull})
	if err != nil {
		t.Fatalf("timeout should be non-blocking, got error: %v", err)
	}
}
