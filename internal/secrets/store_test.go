package secrets

import (
	"testing"

	"github.com/99designs/keyring"
)

func newTestStore(t *testing.T) *KeyringStore {
	t.Helper()
	ring := keyring.NewArrayKeyring(nil)
	return &KeyringStore{ring: ring}
}

func TestSetGetToken(t *testing.T) {
	s := newTestStore(t)
	const email = "user@example.com"
	const token = "jwt-abc-123"

	if err := s.SetToken(email, token); err != nil {
		t.Fatalf("SetToken: %v", err)
	}

	got, err := s.GetToken(email)
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if got != token {
		t.Errorf("GetToken = %q, want %q", got, token)
	}
}

func TestGetToken_NotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.GetToken("nobody@example.com")
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestDeleteToken(t *testing.T) {
	s := newTestStore(t)
	const email = "user@example.com"

	if err := s.SetToken(email, "tok"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}
	if err := s.DeleteToken(email); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}

	_, err := s.GetToken(email)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestSetGetE2EPassword(t *testing.T) {
	s := newTestStore(t)
	const vaultUID = "vault-abc-123"
	const password = "my-e2e-password"

	if err := s.SetE2EPassword(vaultUID, password); err != nil {
		t.Fatalf("SetE2EPassword: %v", err)
	}

	got, err := s.GetE2EPassword(vaultUID)
	if err != nil {
		t.Fatalf("GetE2EPassword: %v", err)
	}
	if got != password {
		t.Errorf("GetE2EPassword = %q, want %q", got, password)
	}
}

func TestGetE2EPassword_NotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.GetE2EPassword("nonexistent-vault")
	if err == nil {
		t.Fatal("expected error for missing E2E password")
	}
}

func TestTokenKey(t *testing.T) {
	got := tokenKey("user@example.com")
	want := "token:user@example.com"
	if got != want {
		t.Errorf("tokenKey = %q, want %q", got, want)
	}
}

func TestE2EKey(t *testing.T) {
	got := e2eKey("vault-123")
	want := "e2e:vault-123"
	if got != want {
		t.Errorf("e2eKey = %q, want %q", got, want)
	}
}

func TestOverwriteToken(t *testing.T) {
	s := newTestStore(t)
	const email = "user@example.com"

	if err := s.SetToken(email, "old-token"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}
	if err := s.SetToken(email, "new-token"); err != nil {
		t.Fatalf("SetToken overwrite: %v", err)
	}

	got, err := s.GetToken(email)
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if got != "new-token" {
		t.Errorf("GetToken after overwrite = %q, want %q", got, "new-token")
	}
}

func TestMultipleTokens(t *testing.T) {
	s := newTestStore(t)

	if err := s.SetToken("a@b.com", "tok-a"); err != nil {
		t.Fatalf("SetToken a: %v", err)
	}
	if err := s.SetToken("c@d.com", "tok-c"); err != nil {
		t.Fatalf("SetToken c: %v", err)
	}

	gotA, err := s.GetToken("a@b.com")
	if err != nil {
		t.Fatalf("GetToken a: %v", err)
	}
	if gotA != "tok-a" {
		t.Errorf("token a = %q, want %q", gotA, "tok-a")
	}

	gotC, err := s.GetToken("c@d.com")
	if err != nil {
		t.Fatalf("GetToken c: %v", err)
	}
	if gotC != "tok-c" {
		t.Errorf("token c = %q, want %q", gotC, "tok-c")
	}
}

func TestResolveBackend_EnvOverride(t *testing.T) {
	t.Setenv(keyringBackendEnv, "file")
	got, err := resolveBackend()
	if err != nil {
		t.Fatalf("resolveBackend: %v", err)
	}
	if got != "file" {
		t.Errorf("resolveBackend = %q, want %q", got, "file")
	}
}

func TestResolveBackend_Default(t *testing.T) {
	t.Setenv(keyringBackendEnv, "")
	// Set HOME to a temp dir so config won't be found.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")

	got, err := resolveBackend()
	if err != nil {
		t.Fatalf("resolveBackend: %v", err)
	}
	if got != "auto" {
		t.Errorf("resolveBackend = %q, want %q", got, "auto")
	}
}

func TestAllowedBackends(t *testing.T) {
	tests := []struct {
		name    string
		backend string
		wantErr bool
	}{
		{"file", "file", false},
		{"keychain", "keychain", false},
		{"auto", "auto", false},
		{"empty", "", false},
		{"unknown", "bogus", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := allowedBackends(tt.backend)
			if (err != nil) != tt.wantErr {
				t.Errorf("allowedBackends(%q) error = %v, wantErr %v", tt.backend, err, tt.wantErr)
			}
		})
	}
}

func TestOpenDefault_MockKeyring(t *testing.T) {
	// Save and restore the original.
	origOpen := OpenDefault
	t.Cleanup(func() { OpenDefault = origOpen })

	called := false
	OpenDefault = func() (Store, error) {
		called = true
		ring := keyring.NewArrayKeyring(nil)
		return &KeyringStore{ring: ring}, nil
	}

	store, err := OpenDefault()
	if err != nil {
		t.Fatalf("OpenDefault: %v", err)
	}
	if !called {
		t.Error("mock was not called")
	}
	if store == nil {
		t.Error("store is nil")
	}
}
