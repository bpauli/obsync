package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"obsync/internal/api"
	"obsync/internal/config"
	"obsync/internal/secrets"
	"obsync/internal/ui"

	"github.com/99designs/keyring"
)

// mockStore implements secrets.Store using an in-memory keyring.
type mockStore struct {
	ring keyring.Keyring
}

func (m *mockStore) SetToken(email, token string) error {
	return m.ring.Set(keyring.Item{Key: "token:" + email, Data: []byte(token)})
}
func (m *mockStore) GetToken(email string) (string, error) {
	item, err := m.ring.Get("token:" + email)
	if err != nil {
		return "", err
	}
	return string(item.Data), nil
}
func (m *mockStore) DeleteToken(email string) error { return m.ring.Remove("token:" + email) }
func (m *mockStore) SetE2EPassword(vaultUID, password string) error {
	return m.ring.Set(keyring.Item{Key: "e2e:" + vaultUID, Data: []byte(password)})
}
func (m *mockStore) GetE2EPassword(vaultUID string) (string, error) {
	item, err := m.ring.Get("e2e:" + vaultUID)
	if err != nil {
		return "", err
	}
	return string(item.Data), nil
}

func newMockStore() *mockStore {
	return &mockStore{ring: keyring.NewArrayKeyring(nil)}
}

func testContext(t *testing.T) (context.Context, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	u, err := ui.New(ui.Options{Stdout: &buf, Stderr: &buf, Color: "never"})
	if err != nil {
		t.Fatal(err)
	}
	return ui.WithUI(context.Background(), u), &buf
}

func TestLoginCmd_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req api.SigninRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if req.Email != "user@example.com" || req.Password != "secret" {
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid credentials"})
			return
		}
		json.NewEncoder(w).Encode(api.SigninResponse{
			Token: "jwt-token-123",
			Email: "user@example.com",
			Name:  "Test User",
		})
	}))
	defer srv.Close()

	store := newMockStore()
	origOpenDefault := secrets.OpenDefault
	secrets.OpenDefault = func() (secrets.Store, error) { return store, nil }
	defer func() { secrets.OpenDefault = origOpenDefault }()

	origNewClient := newAPIClient
	newAPIClient = func() *api.Client {
		return &api.Client{BaseURL: srv.URL, HTTPClient: srv.Client()}
	}
	defer func() { newAPIClient = origNewClient }()

	origPrompt := promptPassword
	promptPassword = func(u *ui.UI) (string, error) { return "secret", nil }
	defer func() { promptPassword = origPrompt }()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	ctx, buf := testContext(t)
	cmd := &LoginCmd{Email: "user@example.com"}
	flags := &RootFlags{Config: cfgPath}

	if err := cmd.Run(ctx, flags); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify token stored
	token, err := store.GetToken("user@example.com")
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if token != "jwt-token-123" {
		t.Errorf("stored token = %q, want %q", token, "jwt-token-123")
	}

	// Verify config saved
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}
	if cfg.Email != "user@example.com" {
		t.Errorf("config email = %q, want %q", cfg.Email, "user@example.com")
	}

	// Verify success message
	if !bytes.Contains(buf.Bytes(), []byte("Logged in as Test User")) {
		t.Errorf("output = %q, want success message", buf.String())
	}
}

func TestLoginCmd_PasswordFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(api.SigninResponse{
			Token: "tok",
			Email: "u@e.com",
			Name:  "U",
		})
	}))
	defer srv.Close()

	store := newMockStore()
	origOpenDefault := secrets.OpenDefault
	secrets.OpenDefault = func() (secrets.Store, error) { return store, nil }
	defer func() { secrets.OpenDefault = origOpenDefault }()

	origNewClient := newAPIClient
	newAPIClient = func() *api.Client {
		return &api.Client{BaseURL: srv.URL, HTTPClient: srv.Client()}
	}
	defer func() { newAPIClient = origNewClient }()

	// promptPassword should NOT be called when password is provided via flag
	origPrompt := promptPassword
	promptPassword = func(u *ui.UI) (string, error) {
		t.Fatal("promptPassword should not be called when --password is set")
		return "", nil
	}
	defer func() { promptPassword = origPrompt }()

	ctx, _ := testContext(t)
	cmd := &LoginCmd{Email: "u@e.com", Password: "direct"}
	flags := &RootFlags{Config: filepath.Join(t.TempDir(), "config.json")}

	if err := cmd.Run(ctx, flags); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestLoginCmd_MFA(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req api.SigninRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.MFA != "123456" {
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]string{"error": "mfa required"})
			return
		}
		json.NewEncoder(w).Encode(api.SigninResponse{
			Token: "mfa-tok",
			Email: req.Email,
			Name:  "MFA User",
		})
	}))
	defer srv.Close()

	store := newMockStore()
	origOpenDefault := secrets.OpenDefault
	secrets.OpenDefault = func() (secrets.Store, error) { return store, nil }
	defer func() { secrets.OpenDefault = origOpenDefault }()

	origNewClient := newAPIClient
	newAPIClient = func() *api.Client {
		return &api.Client{BaseURL: srv.URL, HTTPClient: srv.Client()}
	}
	defer func() { newAPIClient = origNewClient }()

	ctx, _ := testContext(t)
	cmd := &LoginCmd{Email: "mfa@e.com", Password: "pass", MFA: "123456"}
	flags := &RootFlags{Config: filepath.Join(t.TempDir(), "config.json")}

	if err := cmd.Run(ctx, flags); err != nil {
		t.Fatalf("Run: %v", err)
	}

	token, err := store.GetToken("mfa@e.com")
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if token != "mfa-tok" {
		t.Errorf("token = %q, want %q", token, "mfa-tok")
	}
}

func TestLoginCmd_AuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid credentials"})
	}))
	defer srv.Close()

	origNewClient := newAPIClient
	newAPIClient = func() *api.Client {
		return &api.Client{BaseURL: srv.URL, HTTPClient: srv.Client()}
	}
	defer func() { newAPIClient = origNewClient }()

	ctx, _ := testContext(t)
	cmd := &LoginCmd{Email: "bad@e.com", Password: "wrong"}
	flags := &RootFlags{}

	err := cmd.Run(ctx, flags)
	if err == nil {
		t.Fatal("expected error")
	}

	code := ExitCode(err)
	if code != ExitAuth {
		t.Errorf("exit code = %d, want %d", code, ExitAuth)
	}
}

func TestLoginCmd_DefaultConfigPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(api.SigninResponse{Token: "tok", Email: "u@e.com"})
	}))
	defer srv.Close()

	store := newMockStore()
	origOpenDefault := secrets.OpenDefault
	secrets.OpenDefault = func() (secrets.Store, error) { return store, nil }
	defer func() { secrets.OpenDefault = origOpenDefault }()

	origNewClient := newAPIClient
	newAPIClient = func() *api.Client {
		return &api.Client{BaseURL: srv.URL, HTTPClient: srv.Client()}
	}
	defer func() { newAPIClient = origNewClient }()

	// Use a temp HOME to avoid writing to real config
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	// Also set XDG_CONFIG_HOME to ensure we write to temp dir
	origXDG := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpHome, ".config"))
	defer os.Setenv("XDG_CONFIG_HOME", origXDG)

	ctx, _ := testContext(t)
	cmd := &LoginCmd{Email: "u@e.com", Password: "pass"}
	flags := &RootFlags{} // No Config path set — uses default

	if err := cmd.Run(ctx, flags); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify config was saved at default path
	cfgPath, _ := config.DefaultConfigPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}
	if cfg.Email != "u@e.com" {
		t.Errorf("config email = %q, want %q", cfg.Email, "u@e.com")
	}
}

func TestLoginCmd_FallbackToEmail(t *testing.T) {
	// When Name is empty, should display email instead
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(api.SigninResponse{Token: "tok", Email: "user@test.com", Name: ""})
	}))
	defer srv.Close()

	store := newMockStore()
	origOpenDefault := secrets.OpenDefault
	secrets.OpenDefault = func() (secrets.Store, error) { return store, nil }
	defer func() { secrets.OpenDefault = origOpenDefault }()

	origNewClient := newAPIClient
	newAPIClient = func() *api.Client {
		return &api.Client{BaseURL: srv.URL, HTTPClient: srv.Client()}
	}
	defer func() { newAPIClient = origNewClient }()

	ctx, buf := testContext(t)
	cmd := &LoginCmd{Email: "user@test.com", Password: "pass"}
	flags := &RootFlags{Config: filepath.Join(t.TempDir(), "config.json")}

	if err := cmd.Run(ctx, flags); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !bytes.Contains(buf.Bytes(), []byte("Logged in as user@test.com")) {
		t.Errorf("output = %q, want email fallback in success message", buf.String())
	}
}
