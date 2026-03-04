//go:build integration

package integration

import (
	"context"
	"os"
	"testing"

	"obsync/internal/api"
	"obsync/internal/crypto"
	"obsync/internal/sync"
)

func envOrSkip(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("set %s to run this test", key)
	}
	return v
}

func TestLogin(t *testing.T) {
	email := envOrSkip(t, "OBSYNC_TEST_EMAIL")
	password := envOrSkip(t, "OBSYNC_TEST_PASSWORD")
	mfa := os.Getenv("OBSYNC_TEST_MFA") // optional

	client := api.NewClient()
	resp, err := client.Signin(context.Background(), email, password, mfa)
	if err != nil {
		t.Fatalf("Signin failed: %v", err)
	}

	if resp.Token == "" {
		t.Fatal("expected non-empty token")
	}
	t.Logf("Signed in as %s (%s)", resp.Name, resp.Email)
}

func TestListVaults(t *testing.T) {
	email := envOrSkip(t, "OBSYNC_TEST_EMAIL")
	password := envOrSkip(t, "OBSYNC_TEST_PASSWORD")

	client := api.NewClient()
	resp, err := client.Signin(context.Background(), email, password, "")
	if err != nil {
		t.Fatalf("Signin failed: %v", err)
	}

	vaults, err := client.ListVaults(context.Background(), resp.Token)
	if err != nil {
		t.Fatalf("ListVaults failed: %v", err)
	}

	t.Logf("Found %d owned vaults, %d shared vaults", len(vaults.Vaults), len(vaults.Shared))
	for _, v := range vaults.Vaults {
		t.Logf("  Vault: %s (%s)", v.Name, v.ID)
	}
}

func TestEncryption(t *testing.T) {
	e2ePassword := envOrSkip(t, "OBSYNC_TEST_E2E_PASSWORD")

	// Use a fixed salt for testing.
	salt := "test-salt"
	key, err := crypto.DeriveKey(e2ePassword, salt)
	if err != nil {
		t.Fatalf("DeriveKey failed: %v", err)
	}

	// Round-trip encrypt/decrypt.
	plaintext := []byte("Hello, Obsidian!")
	ciphertext, err := crypto.Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	decrypted, err := crypto.Decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if string(decrypted) != string(plaintext) {
		t.Errorf("round-trip failed: got %q, want %q", decrypted, plaintext)
	}

	// Path encryption round-trip.
	path := "notes/daily/2024-01-01.md"
	encPath, err := crypto.EncryptPath(key, path)
	if err != nil {
		t.Fatalf("EncryptPath failed: %v", err)
	}

	decPath, err := crypto.DecryptPath(key, encPath)
	if err != nil {
		t.Fatalf("DecryptPath failed: %v", err)
	}

	if decPath != path {
		t.Errorf("path round-trip failed: got %q, want %q", decPath, path)
	}
}

func TestPullPush(t *testing.T) {
	email := envOrSkip(t, "OBSYNC_TEST_EMAIL")
	password := envOrSkip(t, "OBSYNC_TEST_PASSWORD")
	e2ePassword := envOrSkip(t, "OBSYNC_TEST_E2E_PASSWORD")
	vaultName := envOrSkip(t, "OBSYNC_TEST_VAULT")

	ctx := context.Background()
	client := api.NewClient()

	// Sign in.
	signinResp, err := client.Signin(ctx, email, password, "")
	if err != nil {
		t.Fatalf("Signin failed: %v", err)
	}

	// List vaults and find the test vault.
	vaultsResp, err := client.ListVaults(ctx, signinResp.Token)
	if err != nil {
		t.Fatalf("ListVaults failed: %v", err)
	}

	var vault *api.Vault
	for _, v := range vaultsResp.Vaults {
		if v.Name == vaultName || v.ID == vaultName {
			vault = &v
			break
		}
	}
	if vault == nil {
		t.Skipf("vault %q not found", vaultName)
	}

	// Derive key.
	key, err := crypto.DeriveKey(e2ePassword, vault.Salt)
	if err != nil {
		t.Fatalf("DeriveKey failed: %v", err)
	}
	keyHash := crypto.KeyHash(key)

	// Get WebSocket host.
	host, err := client.VaultAccess(ctx, signinResp.Token, vault.ID, keyHash, 3)
	if err != nil {
		t.Fatalf("VaultAccess failed: %v", err)
	}

	// Connect and do initial pull.
	sc, err := sync.Connect(ctx, sync.ConnectParams{
		Host:              host,
		Token:             signinResp.Token,
		VaultUID:          vault.ID,
		KeyHash:           keyHash,
		Version:           0,
		Initial:           true,
		Device:            "integration-test",
		EncryptionVersion: 3,
		Key:               key,
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sc.Close()

	// Read pushes until ready.
	fileCount := 0
	for {
		msg, err := sc.ReceivePush(ctx)
		if err != nil {
			t.Fatalf("ReceivePush failed: %v", err)
		}

		if msg.Op == "ready" {
			t.Logf("Ready at version %d, received %d file notifications", msg.UID, fileCount)
			break
		}

		if msg.Op == "push" {
			fileCount++
		}
	}
}

func TestWatchSync(t *testing.T) {
	// This test requires a running vault and is primarily a manual test.
	// It verifies the basic watch connection and initial sync.
	email := envOrSkip(t, "OBSYNC_TEST_EMAIL")
	password := envOrSkip(t, "OBSYNC_TEST_PASSWORD")
	e2ePassword := envOrSkip(t, "OBSYNC_TEST_E2E_PASSWORD")
	vaultName := envOrSkip(t, "OBSYNC_TEST_VAULT")

	ctx := context.Background()
	client := api.NewClient()

	signinResp, err := client.Signin(ctx, email, password, "")
	if err != nil {
		t.Fatalf("Signin failed: %v", err)
	}

	vaultsResp, err := client.ListVaults(ctx, signinResp.Token)
	if err != nil {
		t.Fatalf("ListVaults failed: %v", err)
	}

	var vault *api.Vault
	for _, v := range vaultsResp.Vaults {
		if v.Name == vaultName || v.ID == vaultName {
			vault = &v
			break
		}
	}
	if vault == nil {
		t.Skipf("vault %q not found", vaultName)
	}

	key, err := crypto.DeriveKey(e2ePassword, vault.Salt)
	if err != nil {
		t.Fatalf("DeriveKey failed: %v", err)
	}
	keyHash := crypto.KeyHash(key)

	host, err := client.VaultAccess(ctx, signinResp.Token, vault.ID, keyHash, 3)
	if err != nil {
		t.Fatalf("VaultAccess failed: %v", err)
	}

	sc, err := sync.Connect(ctx, sync.ConnectParams{
		Host:              host,
		Token:             signinResp.Token,
		VaultUID:          vault.ID,
		KeyHash:           keyHash,
		Version:           0,
		Initial:           true,
		Device:            "watch-test",
		EncryptionVersion: 3,
		Key:               key,
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sc.Close()

	// Just verify we can connect and receive the ready signal.
	for {
		msg, err := sc.ReceivePush(ctx)
		if err != nil {
			t.Fatalf("ReceivePush failed: %v", err)
		}
		if msg.Op == "ready" {
			t.Logf("Watch connection ready at version %d", msg.UID)
			break
		}
	}
}
