package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	}
}

func TestNewClient(t *testing.T) {
	c := NewClient()
	if c.BaseURL != defaultBaseURL {
		t.Errorf("BaseURL = %q, want %q", c.BaseURL, defaultBaseURL)
	}
	if c.HTTPClient == nil {
		t.Fatal("HTTPClient is nil")
	}
}

func TestSignin(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/signin" {
			t.Errorf("path = %q, want /user/signin", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

		var req SigninRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Email != "user@example.com" {
			t.Errorf("email = %q, want user@example.com", req.Email)
		}
		if req.Password != "secret" {
			t.Errorf("password = %q, want secret", req.Password)
		}

		json.NewEncoder(w).Encode(SigninResponse{
			Token: "jwt-token-123",
			Email: "user@example.com",
			Name:  "Test User",
		})
	})

	resp, err := c.Signin(context.Background(), "user@example.com", "secret", "")
	if err != nil {
		t.Fatalf("Signin: %v", err)
	}
	if resp.Token != "jwt-token-123" {
		t.Errorf("Token = %q, want jwt-token-123", resp.Token)
	}
	if resp.Email != "user@example.com" {
		t.Errorf("Email = %q, want user@example.com", resp.Email)
	}
	if resp.Name != "Test User" {
		t.Errorf("Name = %q, want Test User", resp.Name)
	}
}

func TestSigninWithMFA(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req SigninRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.MFA != "123456" {
			t.Errorf("mfa = %q, want 123456", req.MFA)
		}

		json.NewEncoder(w).Encode(SigninResponse{
			Token: "jwt-mfa-token",
			Email: req.Email,
			Name:  "MFA User",
		})
	})

	resp, err := c.Signin(context.Background(), "user@example.com", "secret", "123456")
	if err != nil {
		t.Fatalf("Signin: %v", err)
	}
	if resp.Token != "jwt-mfa-token" {
		t.Errorf("Token = %q, want jwt-mfa-token", resp.Token)
	}
}

func TestSigninError(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid credentials",
		})
	})

	_, err := c.Signin(context.Background(), "bad@example.com", "wrong", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusUnauthorized)
	}
	if apiErr.Message != "invalid credentials" {
		t.Errorf("Message = %q, want invalid credentials", apiErr.Message)
	}
	// Verify Error() string format.
	want := "api: invalid credentials (HTTP 401)"
	if apiErr.Error() != want {
		t.Errorf("Error() = %q, want %q", apiErr.Error(), want)
	}
}

func TestListVaults(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vault/list" {
			t.Errorf("path = %q, want /vault/list", r.URL.Path)
		}

		var req ListVaultsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Token != "my-token" {
			t.Errorf("token = %q, want my-token", req.Token)
		}
		if req.SupportedEncryptionVersion != 3 {
			t.Errorf("supported_encryption_version = %d, want 3", req.SupportedEncryptionVersion)
		}

		json.NewEncoder(w).Encode(ListVaultsResponse{
			Vaults: []Vault{
				{ID: "v1", Name: "Personal", Password: "pw", Salt: "salt1", EncryptionVersion: 3},
			},
			Shared: []Vault{
				{ID: "v2", Name: "Team", Salt: "salt2", EncryptionVersion: 3},
			},
		})
	})

	resp, err := c.ListVaults(context.Background(), "my-token")
	if err != nil {
		t.Fatalf("ListVaults: %v", err)
	}
	if len(resp.Vaults) != 1 {
		t.Fatalf("len(Vaults) = %d, want 1", len(resp.Vaults))
	}
	if resp.Vaults[0].ID != "v1" {
		t.Errorf("Vaults[0].ID = %q, want v1", resp.Vaults[0].ID)
	}
	if resp.Vaults[0].Name != "Personal" {
		t.Errorf("Vaults[0].Name = %q, want Personal", resp.Vaults[0].Name)
	}
	if len(resp.Shared) != 1 {
		t.Fatalf("len(Shared) = %d, want 1", len(resp.Shared))
	}
	if resp.Shared[0].ID != "v2" {
		t.Errorf("Shared[0].ID = %q, want v2", resp.Shared[0].ID)
	}
}

func TestVaultAccess(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vault/access" {
			t.Errorf("path = %q, want /vault/access", r.URL.Path)
		}

		var req VaultAccessRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Token != "my-token" {
			t.Errorf("token = %q, want my-token", req.Token)
		}
		if req.VaultUID != "vault-123" {
			t.Errorf("vault_uid = %q, want vault-123", req.VaultUID)
		}
		if req.KeyHash != "keyhash-abc" {
			t.Errorf("keyhash = %q, want keyhash-abc", req.KeyHash)
		}
		if req.Host != "sync-123.obsidian.md" {
			t.Errorf("host = %q, want sync-123.obsidian.md", req.Host)
		}
		if req.EncryptionVersion != 3 {
			t.Errorf("encryption_version = %d, want 3", req.EncryptionVersion)
		}

		json.NewEncoder(w).Encode(VaultAccessResponse{
			Host: "sync-123.obsidian.md",
		})
	})

	host, err := c.VaultAccess(context.Background(), "my-token", "vault-123", "keyhash-abc", "sync-123.obsidian.md", 3)
	if err != nil {
		t.Fatalf("VaultAccess: %v", err)
	}
	if host != "sync-123.obsidian.md" {
		t.Errorf("host = %q, want sync-123.obsidian.md", host)
	}
}

func TestVaultAccessError(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "vault not found",
		})
	})

	_, err := c.VaultAccess(context.Background(), "token", "bad-vault", "hash", "sync-1.obsidian.md", 3)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusForbidden)
	}
}

func TestSigninErrorHTTP200(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Obsidian API returns 200 with error in body for some failures.
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Login failed, please double check your email and password.",
		})
	})

	_, err := c.Signin(context.Background(), "bad@example.com", "wrong", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusOK)
	}
	if apiErr.Message == "" {
		t.Error("expected non-empty error message")
	}
}

func TestAPIErrorNoMessage(t *testing.T) {
	e := &APIError{StatusCode: 500}
	want := "api: HTTP 500"
	if e.Error() != want {
		t.Errorf("Error() = %q, want %q", e.Error(), want)
	}
}

func TestSigninCancelledContext(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Should not be reached.
		t.Error("handler called with cancelled context")
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Signin(ctx, "user@example.com", "secret", "")
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}
