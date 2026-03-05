package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"obsync/internal/api"
	"obsync/internal/crypto"
	"obsync/internal/secrets"
	"obsync/internal/sync"
)

func TestPushCmd_Success(t *testing.T) {
	key, _ := crypto.DeriveKey("testpass", "testsalt")

	vaults := []api.Vault{
		{ID: "vault-1", Name: "My Notes", Password: "x", Salt: "testsalt", EncryptionVersion: 3},
	}

	var pushedPaths []string
	cleanup := mockSyncServer(t, vaults, func(conn *websocket.Conn) {
		// Send ready so client drains initial sync.
		conn.WriteJSON(map[string]any{"op": "ready", "version": int64(0)})

		// Read push metadata.
		var meta map[string]any
		if err := conn.ReadJSON(&meta); err != nil {
			t.Logf("read push meta: %v", err)
			return
		}

		// Decrypt path to verify.
		encPath, _ := meta["path"].(string)
		plainPath, _ := crypto.DecryptPath(key, encPath)
		pushedPaths = append(pushedPaths, plainPath)

		// Respond to metadata.
		conn.WriteJSON(map[string]any{})

		// Read binary chunk(s).
		pieces := int(meta["pieces"].(float64))
		for i := 0; i < pieces; i++ {
			_, _, err := conn.ReadMessage()
			if err != nil {
				t.Logf("read chunk: %v", err)
				return
			}
			// Ack chunk.
			conn.WriteJSON(map[string]any{"res": "ok"})
		}
	})
	defer cleanup()

	dir := t.TempDir()

	// Create a local file to push.
	notesDir := filepath.Join(dir, "notes")
	os.MkdirAll(notesDir, 0o755)
	os.WriteFile(filepath.Join(notesDir, "hello.md"), []byte("# Hello World\n"), 0o644)

	ctx, buf := testContext(t)
	cmd := &PushCmd{
		Vault:    "My Notes",
		Path:     dir,
		Password: "testpass",
	}
	flags := &RootFlags{Config: writeConfig(t, "user@example.com")}

	if err := cmd.Run(ctx, flags); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify file was pushed.
	if len(pushedPaths) != 1 || pushedPaths[0] != "notes/hello.md" {
		t.Errorf("pushed paths = %v, want [notes/hello.md]", pushedPaths)
	}

	// Verify state was saved.
	state, err := sync.LoadState(dir)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if _, ok := state.Files["notes/hello.md"]; !ok {
		t.Error("state missing file entry for notes/hello.md")
	}

	// Verify success message.
	out := buf.String()
	if !strings.Contains(out, "Push complete") {
		t.Errorf("output missing success message, got: %s", out)
	}
	if !strings.Contains(out, "1 files pushed") {
		t.Errorf("output missing push count, got: %s", out)
	}
}

func TestPushCmd_DeletedFile(t *testing.T) {
	key, _ := crypto.DeriveKey("testpass", "testsalt")

	vaults := []api.Vault{
		{ID: "vault-1", Name: "My Notes", Password: "x", Salt: "testsalt", EncryptionVersion: 3},
	}

	var deletedPaths []string
	cleanup := mockSyncServer(t, vaults, func(conn *websocket.Conn) {
		// Send ready so client drains initial sync.
		conn.WriteJSON(map[string]any{"op": "ready", "version": int64(10)})

		// Read delete metadata.
		var meta map[string]any
		if err := conn.ReadJSON(&meta); err != nil {
			t.Logf("read delete meta: %v", err)
			return
		}

		encPath, _ := meta["path"].(string)
		plainPath, _ := crypto.DecryptPath(key, encPath)
		deletedPaths = append(deletedPaths, plainPath)

		deleted, _ := meta["deleted"].(bool)
		if !deleted {
			t.Error("expected deleted=true in push metadata")
		}

		// Send ack.
		conn.WriteJSON(map[string]any{"res": "ok"})
	})
	defer cleanup()

	dir := t.TempDir()

	// Create state with a file that no longer exists on disk.
	state := sync.State{
		VaultUID: "vault-1",
		Version:  10,
		Files: map[string]sync.FileState{
			"old-file.md": {
				Hash:     "abc",
				SyncHash: "abc",
				Size:     100,
			},
		},
	}
	if err := state.Save(dir); err != nil {
		t.Fatalf("save state: %v", err)
	}

	ctx, buf := testContext(t)
	cmd := &PushCmd{
		Vault:    "vault-1",
		Path:     dir,
		Password: "testpass",
	}
	flags := &RootFlags{Config: writeConfig(t, "user@example.com")}

	if err := cmd.Run(ctx, flags); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify delete was sent.
	if len(deletedPaths) != 1 || deletedPaths[0] != "old-file.md" {
		t.Errorf("deleted paths = %v, want [old-file.md]", deletedPaths)
	}

	// Verify state no longer has the file.
	updatedState, err := sync.LoadState(dir)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if _, ok := updatedState.Files["old-file.md"]; ok {
		t.Error("state should not have old-file.md after deletion")
	}

	out := buf.String()
	if !strings.Contains(out, "1 deleted") {
		t.Errorf("output missing delete count, got: %s", out)
	}
}

func TestPushCmd_NothingToPush(t *testing.T) {
	vaults := []api.Vault{
		{ID: "vault-1", Name: "My Notes", Password: "x", Salt: "testsalt", EncryptionVersion: 3},
	}

	cleanup := mockSyncServer(t, vaults, func(conn *websocket.Conn) {
		t.Error("should not connect to WebSocket when nothing to push")
	})
	defer cleanup()

	dir := t.TempDir()

	ctx, buf := testContext(t)
	cmd := &PushCmd{
		Vault:    "vault-1",
		Path:     dir,
		Password: "testpass",
	}
	flags := &RootFlags{Config: writeConfig(t, "user@example.com")}

	if err := cmd.Run(ctx, flags); err != nil {
		t.Fatalf("Run: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Nothing to push") {
		t.Errorf("expected 'Nothing to push' message, got: %s", out)
	}
}

func TestPushCmd_NotLoggedIn(t *testing.T) {
	ctx, _ := testContext(t)
	cmd := &PushCmd{
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

func TestPushCmd_VaultNotFound(t *testing.T) {
	vaults := []api.Vault{
		{ID: "vault-1", Name: "My Notes", Password: "x", Salt: "testsalt", EncryptionVersion: 3},
	}
	cleanup := mockSyncServer(t, vaults, nil)
	defer cleanup()

	ctx, _ := testContext(t)
	cmd := &PushCmd{
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

func TestPushCmd_SavePassword(t *testing.T) {
	vaults := []api.Vault{
		{ID: "vault-1", Name: "My Notes", Password: "x", Salt: "testsalt", EncryptionVersion: 3},
	}

	cleanup := mockSyncServer(t, vaults, func(conn *websocket.Conn) {
		// No files to push — WebSocket won't be reached due to early return.
	})
	defer cleanup()

	store := newMockStore()
	_ = store.SetToken("user@example.com", "test-token")
	origOpenDefault := secrets.OpenDefault
	secrets.OpenDefault = func() (secrets.Store, error) { return store, nil }
	defer func() { secrets.OpenDefault = origOpenDefault }()

	dir := t.TempDir()
	ctx, _ := testContext(t)
	cmd := &PushCmd{
		Vault:        "vault-1",
		Path:         dir,
		Password:     "save-me",
		SavePassword: true,
	}
	flags := &RootFlags{Config: writeConfig(t, "user@example.com")}

	if err := cmd.Run(ctx, flags); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify password was saved.
	pw, err := store.GetE2EPassword("vault-1")
	if err != nil {
		t.Fatalf("get e2e password: %v", err)
	}
	if pw != "save-me" {
		t.Errorf("saved password = %q, want %q", pw, "save-me")
	}
}

func TestPushCmd_SkipsUnchangedFiles(t *testing.T) {
	vaults := []api.Vault{
		{ID: "vault-1", Name: "My Notes", Password: "x", Salt: "testsalt", EncryptionVersion: 3},
	}

	cleanup := mockSyncServer(t, vaults, func(conn *websocket.Conn) {
		t.Error("should not connect to WebSocket when files unchanged")
	})
	defer cleanup()

	dir := t.TempDir()

	// Create a file.
	content := []byte("hello")
	os.WriteFile(filepath.Join(dir, "test.md"), content, 0o644)

	// Create state where the file is already synced with matching hash.
	hash := sha256Hex(content)
	state := sync.State{
		VaultUID: "vault-1",
		Version:  5,
		Files: map[string]sync.FileState{
			"test.md": {
				Hash:     hash,
				SyncHash: hash,
				Size:     int64(len(content)),
			},
		},
	}
	if err := state.Save(dir); err != nil {
		t.Fatalf("save state: %v", err)
	}

	ctx, buf := testContext(t)
	cmd := &PushCmd{
		Vault:    "vault-1",
		Path:     dir,
		Password: "testpass",
	}
	flags := &RootFlags{Config: writeConfig(t, "user@example.com")}

	if err := cmd.Run(ctx, flags); err != nil {
		t.Fatalf("Run: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Nothing to push") {
		t.Errorf("expected 'Nothing to push' for unchanged files, got: %s", out)
	}
}

func TestScanLocalFiles(t *testing.T) {
	dir := t.TempDir()

	// Create some files.
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "root.md"), []byte("root"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "nested.md"), []byte("nested"), 0o644)

	// Create hidden file (should be skipped).
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("hidden"), 0o644)

	// Create .obsync-state.json (should be skipped as hidden).
	os.WriteFile(filepath.Join(dir, ".obsync-state.json"), []byte("{}"), 0o644)

	// Create .obsidian config (should be included).
	os.MkdirAll(filepath.Join(dir, ".obsidian"), 0o755)
	os.WriteFile(filepath.Join(dir, ".obsidian", "app.json"), []byte(`{}`), 0o644)

	files, err := scanLocalFiles(dir)
	if err != nil {
		t.Fatalf("scanLocalFiles: %v", err)
	}

	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d: %v", len(files), files)
	}

	if _, ok := files["root.md"]; !ok {
		t.Error("missing root.md")
	}
	if _, ok := files[filepath.Join("sub", "nested.md")]; !ok {
		t.Error("missing sub/nested.md")
	}
	if _, ok := files[filepath.Join(".obsidian", "app.json")]; !ok {
		t.Error("missing .obsidian/app.json")
	}
	if _, ok := files[".hidden"]; ok {
		t.Error("should skip hidden files")
	}
	if _, ok := files[".obsync-state.json"]; ok {
		t.Error("should skip state file")
	}
}

// sha256Hex computes the SHA-256 hex hash of data, matching scanLocalFiles.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
