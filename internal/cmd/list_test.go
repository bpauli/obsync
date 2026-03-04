package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"obsync/internal/api"
	"obsync/internal/config"
	"obsync/internal/secrets"
)

func setupListTest(t *testing.T, handler http.Handler) (cleanup func()) {
	t.Helper()
	srv := httptest.NewServer(handler)

	store := newMockStore()
	_ = store.SetToken("user@example.com", "test-token")

	origOpenDefault := secrets.OpenDefault
	secrets.OpenDefault = func() (secrets.Store, error) { return store, nil }

	origNewClient := newAPIClient
	newAPIClient = func() *api.Client {
		return &api.Client{BaseURL: srv.URL, HTTPClient: srv.Client()}
	}

	return func() {
		srv.Close()
		secrets.OpenDefault = origOpenDefault
		newAPIClient = origNewClient
	}
}

func writeConfig(t *testing.T, email string) (cfgPath string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath = filepath.Join(dir, "config.json")
	cfg := config.Config{Email: email}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

func TestListCmd_Success(t *testing.T) {
	cleanup := setupListTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(api.ListVaultsResponse{
			Vaults: []api.Vault{
				{ID: "vault-1", Name: "My Notes", Password: "salt123"},
				{ID: "vault-2", Name: "Work", Password: ""},
			},
			Shared: []api.Vault{
				{ID: "vault-3", Name: "Team Docs", Password: "abc"},
			},
		})
	}))
	defer cleanup()

	ctx, buf := testContext(t)
	cmd := &ListCmd{}
	flags := &RootFlags{Config: writeConfig(t, "user@example.com")}

	if err := cmd.Run(ctx, flags); err != nil {
		t.Fatalf("Run: %v", err)
	}

	out := buf.String()
	// Check header
	if !bytes.Contains(buf.Bytes(), []byte("ID")) || !bytes.Contains(buf.Bytes(), []byte("NAME")) {
		t.Errorf("missing table header, got: %s", out)
	}
	// Check vault entries
	for _, want := range []string{"vault-1", "My Notes", "vault-2", "Work", "vault-3", "Team Docs"} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Errorf("output missing %q, got: %s", want, out)
		}
	}
	// Check E2E column
	if !bytes.Contains(buf.Bytes(), []byte("yes")) {
		t.Errorf("output missing E2E 'yes', got: %s", out)
	}
}

func TestListCmd_JSON(t *testing.T) {
	cleanup := setupListTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(api.ListVaultsResponse{
			Vaults: []api.Vault{
				{ID: "v1", Name: "Test Vault", Password: "p"},
			},
		})
	}))
	defer cleanup()

	ctx, buf := testContext(t)
	cmd := &ListCmd{}
	flags := &RootFlags{Config: writeConfig(t, "user@example.com"), JSON: true}

	if err := cmd.Run(ctx, flags); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var resp api.ListVaultsResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON output: %v\nraw: %s", err, buf.String())
	}
	if len(resp.Vaults) != 1 || resp.Vaults[0].ID != "v1" {
		t.Errorf("unexpected JSON response: %+v", resp)
	}
}

func TestListCmd_NoVaults(t *testing.T) {
	cleanup := setupListTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(api.ListVaultsResponse{})
	}))
	defer cleanup()

	ctx, buf := testContext(t)
	cmd := &ListCmd{}
	flags := &RootFlags{Config: writeConfig(t, "user@example.com")}

	if err := cmd.Run(ctx, flags); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !bytes.Contains(buf.Bytes(), []byte("No vaults found")) {
		t.Errorf("expected 'No vaults found' message, got: %s", buf.String())
	}
}

func TestListCmd_NotLoggedIn(t *testing.T) {
	ctx, _ := testContext(t)
	cmd := &ListCmd{}
	// Config with empty email
	flags := &RootFlags{Config: writeConfig(t, "")}

	err := cmd.Run(ctx, flags)
	if err == nil {
		t.Fatal("expected error for not logged in")
	}
	if ExitCode(err) != ExitAuth {
		t.Errorf("exit code = %d, want %d", ExitCode(err), ExitAuth)
	}
}

func TestListCmd_APIError(t *testing.T) {
	cleanup := setupListTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{"error": "token expired"})
	}))
	defer cleanup()

	ctx, _ := testContext(t)
	cmd := &ListCmd{}
	flags := &RootFlags{Config: writeConfig(t, "user@example.com")}

	err := cmd.Run(ctx, flags)
	if err == nil {
		t.Fatal("expected error for API failure")
	}
	if ExitCode(err) != ExitAuth {
		t.Errorf("exit code = %d, want %d", ExitCode(err), ExitAuth)
	}
}

func TestListCmd_SharedVaults(t *testing.T) {
	cleanup := setupListTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(api.ListVaultsResponse{
			Shared: []api.Vault{
				{ID: "shared-1", Name: "Shared Vault", Password: "enc"},
			},
		})
	}))
	defer cleanup()

	ctx, buf := testContext(t)
	cmd := &ListCmd{}
	flags := &RootFlags{Config: writeConfig(t, "user@example.com")}

	if err := cmd.Run(ctx, flags); err != nil {
		t.Fatalf("Run: %v", err)
	}

	out := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte("shared-1")) {
		t.Errorf("output missing shared vault ID, got: %s", out)
	}
	if !bytes.Contains(buf.Bytes(), []byte("Shared Vault")) {
		t.Errorf("output missing shared vault name, got: %s", out)
	}
}
