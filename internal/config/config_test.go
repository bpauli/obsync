package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MissingFile(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.json")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if cfg.Device == "" {
		t.Error("expected default device (hostname), got empty string")
	}
}

func TestLoad_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data := `{"email": "test@example.com", "device": "myserver", "keyring_backend": "file"}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Email != "test@example.com" {
		t.Errorf("email = %q, want %q", cfg.Email, "test@example.com")
	}
	if cfg.Device != "myserver" {
		t.Errorf("device = %q, want %q", cfg.Device, "myserver")
	}
	if cfg.KeyringBackend != "file" {
		t.Errorf("keyring_backend = %q, want %q", cfg.KeyringBackend, "file")
	}
}

func TestLoad_EmptyDevice_DefaultsToHostname(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data := `{"email": "test@example.com"}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hostname, _ := os.Hostname()
	if cfg.Device != hostname {
		t.Errorf("device = %q, want hostname %q", cfg.Device, hostname)
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := os.WriteFile(path, []byte("{invalid"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSave_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "config.json")

	cfg := Config{
		Email:          "test@example.com",
		Device:         "myserver",
		KeyringBackend: "auto",
	}

	if err := Save(path, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read saved file: %v", err)
	}

	var loaded Config
	if err := json.Unmarshal(b, &loaded); err != nil {
		t.Fatalf("saved file is not valid JSON: %v", err)
	}

	if loaded.Email != cfg.Email {
		t.Errorf("email = %q, want %q", loaded.Email, cfg.Email)
	}
	if loaded.Device != cfg.Device {
		t.Errorf("device = %q, want %q", loaded.Device, cfg.Device)
	}
}

func TestSave_AtomicWrite_NoTmpLeftover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := Config{Email: "test@example.com"}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("tmp file should not exist after successful save")
	}
}

func TestSave_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := Save(path, Config{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("file permissions = %o, want 600", perm)
	}
}

func TestSave_Load_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	original := Config{
		Email:          "user@example.com",
		Device:         "server01",
		KeyringBackend: "file",
	}

	if err := Save(path, original); err != nil {
		t.Fatalf("save error: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	if loaded != original {
		t.Errorf("round-trip mismatch:\n  got:  %+v\n  want: %+v", loaded, original)
	}
}

func TestConfigDir(t *testing.T) {
	dir, err := ConfigDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if filepath.Base(dir) != appName {
		t.Errorf("config dir should end with %q, got %q", appName, dir)
	}
}

func TestDefaultConfigPath(t *testing.T) {
	path, err := DefaultConfigPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if filepath.Base(path) != configFile {
		t.Errorf("config path should end with %q, got %q", configFile, path)
	}
}

func TestEnsureKeyringDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	kd, err := EnsureKeyringDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if filepath.Base(kd) != keyringDir {
		t.Errorf("keyring dir should end with %q, got %q", keyringDir, kd)
	}

	info, err := os.Stat(kd)
	if err != nil {
		t.Fatalf("keyring dir should exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("keyring dir should be a directory")
	}

	perm := info.Mode().Perm()
	if perm != 0o700 {
		t.Errorf("keyring dir permissions = %o, want 700", perm)
	}
}
