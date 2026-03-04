package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"

	"obsync/internal/api"
	"obsync/internal/crypto"
	"obsync/internal/sync"
	"obsync/internal/ui"
)

func TestWatchCmd_InitialSync(t *testing.T) {
	key, _ := crypto.DeriveKey("testpass", "testsalt")
	encPath, _ := crypto.EncryptPath(key, "notes/hello.md")
	content := []byte("# Hello World\n")
	contentHash := fileHash(content)
	encHash, _ := crypto.EncryptPath(key, contentHash)
	encContent, _ := crypto.Encrypt(key, content)

	vaults := []api.Vault{
		{ID: "vault-1", Name: "My Notes", Password: "x", Salt: "testsalt"},
	}

	ctx, cancel := context.WithCancel(context.Background())

	cleanup := mockSyncServer(t, vaults, func(conn *websocket.Conn) {
		// Send a push message for initial pull.
		conn.WriteJSON(map[string]any{
			"op":    "push",
			"path":  encPath,
			"hash":  encHash,
			"size":  int64(len(content)),
			"ctime": int64(1709553600000),
			"mtime": int64(1709553600000),
			"uid":   int64(1),
		})

		// Read pull request.
		var pull map[string]any
		conn.ReadJSON(&pull)

		// Send size response.
		conn.WriteJSON(map[string]any{
			"op":     "size",
			"size":   int64(len(encContent)),
			"pieces": 1,
		})

		// Send binary content.
		conn.WriteMessage(websocket.BinaryMessage, encContent)

		// Send ready.
		conn.WriteJSON(map[string]any{"op": "ready", "version": int64(42)})

		// After initial sync, the receive loop starts reading.
		// Wait for the next read attempt (which blocks), then cancel.
		// The read will block until we cancel or send something.
		time.Sleep(500 * time.Millisecond)
		cancel()

		// Keep connection alive briefly for clean shutdown.
		time.Sleep(200 * time.Millisecond)
	})
	defer cleanup()

	dir := t.TempDir()
	var buf bytes.Buffer
	u, err := ui.New(ui.Options{Stdout: &buf, Stderr: &buf, Color: "never"})
	if err != nil {
		t.Fatal(err)
	}
	watchCtx := ui.WithUI(ctx, u)

	cmd := &WatchCmd{
		Vault:    "My Notes",
		Path:     dir,
		Password: "testpass",
	}
	flags := &RootFlags{Config: writeConfig(t, "user@example.com")}

	if err := cmd.Run(watchCtx, flags); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify file was written during initial pull.
	data, err := os.ReadFile(filepath.Join(dir, "notes", "hello.md"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "# Hello World\n" {
		t.Errorf("file content = %q, want %q", string(data), "# Hello World\n")
	}

	// Verify state was saved.
	state, err := sync.LoadState(dir)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state.Version != 42 {
		t.Errorf("state version = %d, want 42", state.Version)
	}

	out := buf.String()
	if !strings.Contains(out, "Initial sync") {
		t.Errorf("output missing initial sync message, got: %s", out)
	}
}

func TestWatchCmd_NotLoggedIn(t *testing.T) {
	ctx, _ := testContext(t)
	cmd := &WatchCmd{
		Vault: "test",
		Path:  t.TempDir(),
	}
	flags := &RootFlags{Config: writeConfig(t, "")}

	err := cmd.Run(ctx, flags)
	if err == nil {
		t.Fatal("expected error for not logged in")
	}
	if ExitCode(err) != ExitAuth {
		t.Errorf("exit code = %d, want %d", ExitCode(err), ExitAuth)
	}
}

func TestWatchCmd_VaultNotFound(t *testing.T) {
	vaults := []api.Vault{
		{ID: "vault-1", Name: "My Notes", Password: "x", Salt: "testsalt"},
	}
	cleanup := mockSyncServer(t, vaults, nil)
	defer cleanup()

	ctx, _ := testContext(t)
	cmd := &WatchCmd{
		Vault:    "nonexistent",
		Path:     t.TempDir(),
		Password: "testpass",
	}
	flags := &RootFlags{Config: writeConfig(t, "user@example.com")}

	err := cmd.Run(ctx, flags)
	if err == nil {
		t.Fatal("expected error for vault not found")
	}
	if ExitCode(err) != ExitNotFound {
		t.Errorf("exit code = %d, want %d", ExitCode(err), ExitNotFound)
	}
}

func TestDebounceDuration(t *testing.T) {
	if debounceDuration != 500*time.Millisecond {
		t.Errorf("debounceDuration = %v, want 500ms", debounceDuration)
	}
}

func TestAddWatchDirs(t *testing.T) {
	dir := t.TempDir()

	// Create directory structure.
	os.MkdirAll(filepath.Join(dir, "sub", "nested"), 0o755)
	os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("create watcher: %v", err)
	}
	defer watcher.Close()

	if err := addWatchDirs(watcher, dir); err != nil {
		t.Fatalf("addWatchDirs: %v", err)
	}

	watchList := watcher.WatchList()
	has := func(p string) bool {
		for _, w := range watchList {
			if w == p {
				return true
			}
		}
		return false
	}

	if !has(dir) {
		t.Error("root dir not watched")
	}
	if !has(filepath.Join(dir, "sub")) {
		t.Error("sub dir not watched")
	}
	if !has(filepath.Join(dir, "sub", "nested")) {
		t.Error("nested dir not watched")
	}
	if has(filepath.Join(dir, ".hidden")) {
		t.Error("hidden dir should not be watched")
	}
}

func TestWatchCmd_GracefulShutdown(t *testing.T) {
	vaults := []api.Vault{
		{ID: "vault-1", Name: "My Notes", Password: "x", Salt: "testsalt"},
	}

	ctx, cancel := context.WithCancel(context.Background())

	cleanup := mockSyncServer(t, vaults, func(conn *websocket.Conn) {
		// Send ready immediately (no files to sync).
		conn.WriteJSON(map[string]any{"op": "ready", "version": int64(1)})

		// Cancel after initial sync to trigger graceful shutdown.
		time.Sleep(100 * time.Millisecond)
		cancel()

		// Keep connection alive briefly.
		time.Sleep(200 * time.Millisecond)
	})
	defer cleanup()

	dir := t.TempDir()
	var buf bytes.Buffer
	u, err := ui.New(ui.Options{Stdout: &buf, Stderr: &buf, Color: "never"})
	if err != nil {
		t.Fatal(err)
	}
	watchCtx := ui.WithUI(ctx, u)

	cmd := &WatchCmd{
		Vault:    "vault-1",
		Path:     dir,
		Password: "testpass",
	}
	flags := &RootFlags{Config: writeConfig(t, "user@example.com")}

	if err := cmd.Run(watchCtx, flags); err != nil {
		t.Fatalf("Run: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Watch stopped") {
		t.Errorf("output missing stop message, got: %s", out)
	}
}
