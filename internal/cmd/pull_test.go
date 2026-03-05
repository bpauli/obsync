package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"obsync/internal/api"
	"obsync/internal/crypto"
	"obsync/internal/secrets"
	"obsync/internal/sync"
	"obsync/internal/ui"
)

var wsUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

// mockSyncServer creates a test HTTP server that handles both API and WebSocket.
// The wsHandler is called for WebSocket connections.
func mockSyncServer(t *testing.T, vaults []api.Vault, wsHandler func(*websocket.Conn)) (cleanup func()) {
	t.Helper()

	// API server for vault listing and access.
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vault/list":
			json.NewEncoder(w).Encode(api.ListVaultsResponse{Vaults: vaults})
		case "/vault/access":
			json.NewEncoder(w).Encode(api.VaultAccessResponse{Host: "sync-test.obsidian.md"})
		default:
			http.Error(w, "not found", 404)
		}
	}))

	// WebSocket server.
	wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("websocket upgrade: %v", err)
			return
		}
		defer conn.Close()

		// Read init message.
		var init map[string]any
		if err := conn.ReadJSON(&init); err != nil {
			t.Logf("read init: %v", err)
			return
		}

		// Send ok response.
		conn.WriteJSON(map[string]any{"res": "ok", "perFileMax": 5242880})

		if wsHandler != nil {
			wsHandler(conn)
		}
	}))

	store := newMockStore()
	_ = store.SetToken("user@example.com", "test-token")

	origOpenDefault := secrets.OpenDefault
	secrets.OpenDefault = func() (secrets.Store, error) { return store, nil }

	origNewClient := newAPIClient
	newAPIClient = func() *api.Client {
		return &api.Client{BaseURL: apiSrv.URL, HTTPClient: apiSrv.Client()}
	}

	origConnect := syncConnectFunc
	syncConnectFunc = func(ctx context.Context, params sync.ConnectParams) (*sync.Client, error) {
		// Convert http:// URL to ws:// for WebSocket connection.
		wsURL := "ws" + strings.TrimPrefix(wsSrv.URL, "http")
		return sync.ConnectToURL(ctx, wsURL, params)
	}

	origPrompt := promptE2EPassword
	promptE2EPassword = func(u *ui.UI) (string, error) {
		return "", errors.New("should not prompt in tests")
	}

	return func() {
		apiSrv.Close()
		wsSrv.Close()
		secrets.OpenDefault = origOpenDefault
		newAPIClient = origNewClient
		syncConnectFunc = origConnect
		promptE2EPassword = origPrompt
	}
}

func TestPullCmd_Success(t *testing.T) {
	key, _ := crypto.DeriveKey("testpass", "testsalt")

	encPath, _ := crypto.EncryptPath(key, "notes/hello.md")
	encHash, _ := crypto.EncryptPath(key, "abc123")
	content := []byte("# Hello World\n")
	encContent, _ := crypto.Encrypt(key, content)

	vaults := []api.Vault{
		{ID: "vault-1", Name: "My Notes", Password: "x", Salt: "testsalt", EncryptionVersion: 3},
	}

	cleanup := mockSyncServer(t, vaults, func(conn *websocket.Conn) {
		// Send a push message.
		conn.WriteJSON(map[string]any{
			"op":    "push",
			"path":  encPath,
			"hash":  encHash,
			"size":  int64(len(content)),
			"ctime": int64(1709553600000),
			"mtime": int64(1709553600000),
			"uid":   int64(1),
		})

		// Send ready — client collects pushes first, then pulls files.
		conn.WriteJSON(map[string]any{"op": "ready", "version": int64(42)})

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
	})
	defer cleanup()

	dir := t.TempDir()
	ctx, buf := testContext(t)
	cmd := &PullCmd{
		Vault:    "My Notes",
		Path:     dir,
		Password: "testpass",
	}
	flags := &RootFlags{Config: writeConfig(t, "user@example.com")}

	if err := cmd.Run(ctx, flags); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify file was written.
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
	if _, ok := state.Files["notes/hello.md"]; !ok {
		t.Error("state missing file entry for notes/hello.md")
	}

	// Verify success message.
	out := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte("Pull complete")) {
		t.Errorf("output missing success message, got: %s", out)
	}
}

func TestPullCmd_DeletedFile(t *testing.T) {
	key, _ := crypto.DeriveKey("testpass", "testsalt")
	encPath, _ := crypto.EncryptPath(key, "old-file.md")

	vaults := []api.Vault{
		{ID: "vault-1", Name: "My Notes", Password: "x", Salt: "testsalt", EncryptionVersion: 3},
	}

	cleanup := mockSyncServer(t, vaults, func(conn *websocket.Conn) {
		// Send a delete push.
		conn.WriteJSON(map[string]any{
			"op":      "push",
			"path":    encPath,
			"deleted": true,
		})

		// Send ready.
		conn.WriteJSON(map[string]any{"op": "ready", "version": int64(10)})
	})
	defer cleanup()

	dir := t.TempDir()
	// Create a file that will be deleted.
	filePath := filepath.Join(dir, "old-file.md")
	os.WriteFile(filePath, []byte("old content"), 0o644)

	ctx, _ := testContext(t)
	cmd := &PullCmd{
		Vault:    "vault-1",
		Path:     dir,
		Password: "testpass",
	}
	flags := &RootFlags{Config: writeConfig(t, "user@example.com")}

	if err := cmd.Run(ctx, flags); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// File should be gone.
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("expected file to be deleted")
	}
}

func TestPullCmd_NotLoggedIn(t *testing.T) {
	ctx, _ := testContext(t)
	cmd := &PullCmd{
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

func TestPullCmd_VaultNotFound(t *testing.T) {
	vaults := []api.Vault{
		{ID: "vault-1", Name: "My Notes", Password: "x", Salt: "testsalt", EncryptionVersion: 3},
	}
	cleanup := mockSyncServer(t, vaults, nil)
	defer cleanup()

	ctx, _ := testContext(t)
	cmd := &PullCmd{
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

func TestPullCmd_PasswordFromKeyring(t *testing.T) {
	key, _ := crypto.DeriveKey("keyring-pass", "testsalt")

	vaults := []api.Vault{
		{ID: "vault-1", Name: "My Notes", Password: "x", Salt: "testsalt", EncryptionVersion: 3},
	}

	cleanup := mockSyncServer(t, vaults, func(conn *websocket.Conn) {
		// Send ready immediately (no files to sync).
		conn.WriteJSON(map[string]any{"op": "ready", "version": int64(1)})
	})
	defer cleanup()

	// Store E2E password in keyring.
	store := newMockStore()
	_ = store.SetToken("user@example.com", "test-token")
	_ = store.SetE2EPassword("vault-1", "keyring-pass")
	origOpenDefault := secrets.OpenDefault
	secrets.OpenDefault = func() (secrets.Store, error) { return store, nil }
	defer func() { secrets.OpenDefault = origOpenDefault }()

	_ = key // used for key derivation validation

	dir := t.TempDir()
	ctx, _ := testContext(t)
	cmd := &PullCmd{
		Vault: "vault-1",
		Path:  dir,
	}
	flags := &RootFlags{Config: writeConfig(t, "user@example.com")}

	if err := cmd.Run(ctx, flags); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestPullCmd_SavePassword(t *testing.T) {
	vaults := []api.Vault{
		{ID: "vault-1", Name: "My Notes", Password: "x", Salt: "testsalt", EncryptionVersion: 3},
	}

	cleanup := mockSyncServer(t, vaults, func(conn *websocket.Conn) {
		conn.WriteJSON(map[string]any{"op": "ready", "version": int64(1)})
	})
	defer cleanup()

	store := newMockStore()
	_ = store.SetToken("user@example.com", "test-token")
	origOpenDefault := secrets.OpenDefault
	secrets.OpenDefault = func() (secrets.Store, error) { return store, nil }
	defer func() { secrets.OpenDefault = origOpenDefault }()

	dir := t.TempDir()
	ctx, _ := testContext(t)
	cmd := &PullCmd{
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

func TestResolveVault(t *testing.T) {
	resp := &api.ListVaultsResponse{
		Vaults: []api.Vault{
			{ID: "v1", Name: "Notes"},
			{ID: "v2", Name: "Work"},
		},
		Shared: []api.Vault{
			{ID: "v3", Name: "Team"},
		},
	}

	// Find by name.
	v, err := resolveVault(resp, "Notes")
	if err != nil {
		t.Fatalf("resolveVault by name: %v", err)
	}
	if v.ID != "v1" {
		t.Errorf("vault ID = %q, want %q", v.ID, "v1")
	}

	// Find by ID.
	v, err = resolveVault(resp, "v2")
	if err != nil {
		t.Fatalf("resolveVault by ID: %v", err)
	}
	if v.Name != "Work" {
		t.Errorf("vault name = %q, want %q", v.Name, "Work")
	}

	// Find shared vault.
	v, err = resolveVault(resp, "Team")
	if err != nil {
		t.Fatalf("resolveVault shared: %v", err)
	}
	if v.ID != "v3" {
		t.Errorf("vault ID = %q, want %q", v.ID, "v3")
	}

	// Not found.
	_, err = resolveVault(resp, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent vault")
	}
}
