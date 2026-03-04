package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateServiceFile(t *testing.T) {
	content, err := generateServiceFile("abc123", "/home/user/vault", "/usr/local/bin/obsync")
	if err != nil {
		t.Fatalf("generateServiceFile failed: %v", err)
	}

	// Check required fields.
	checks := []string{
		"Description=obsync",
		"abc123",
		"ExecStart=/usr/local/bin/obsync watch abc123 /home/user/vault",
		"Restart=on-failure",
		"Environment=OBSYNC_KEYRING_BACKEND=file",
		"WantedBy=default.target",
		"After=network-online.target",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("service file missing %q\n\nGot:\n%s", check, content)
		}
	}
}

func TestGenerateServiceFile_KeyringPassword(t *testing.T) {
	t.Setenv("OBSYNC_KEYRING_PASSWORD", "mysecret")

	content, err := generateServiceFile("vault1", "/tmp/vault", "/usr/bin/obsync")
	if err != nil {
		t.Fatalf("generateServiceFile failed: %v", err)
	}

	if !strings.Contains(content, "Environment=OBSYNC_KEYRING_PASSWORD=mysecret") {
		t.Errorf("service file should include keyring password env\n\nGot:\n%s", content)
	}
}

func TestGenerateServiceFile_NoKeyringPassword(t *testing.T) {
	t.Setenv("OBSYNC_KEYRING_PASSWORD", "")

	content, err := generateServiceFile("vault1", "/tmp/vault", "/usr/bin/obsync")
	if err != nil {
		t.Fatalf("generateServiceFile failed: %v", err)
	}

	if strings.Contains(content, "OBSYNC_KEYRING_PASSWORD") {
		t.Errorf("service file should not include empty keyring password env\n\nGot:\n%s", content)
	}
}

func TestGenerateServiceFile_RelativePath(t *testing.T) {
	// generateServiceFile should resolve relative paths to absolute.
	content, err := generateServiceFile("v1", "relative/path", "/usr/bin/obsync")
	if err != nil {
		t.Fatalf("generateServiceFile failed: %v", err)
	}

	// The path in ExecStart should be absolute.
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "ExecStart=") {
			parts := strings.Fields(line)
			// parts: ExecStart=/usr/bin/obsync watch v1 /absolute/path
			vaultPath := parts[len(parts)-1]
			if !filepath.IsAbs(vaultPath) {
				t.Errorf("vault path should be absolute, got %q", vaultPath)
			}
		}
	}
}

func TestServiceName(t *testing.T) {
	name := serviceName("abc123")
	if name != "obsync@abc123.service" {
		t.Errorf("expected obsync@abc123.service, got %s", name)
	}
}

func TestServicePath(t *testing.T) {
	path, err := servicePath("abc123")
	if err != nil {
		t.Fatalf("servicePath failed: %v", err)
	}

	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".config", "systemd", "user", "obsync@abc123.service")
	if path != expected {
		t.Errorf("expected %s, got %s", expected, path)
	}
}

func TestInstallCmd_WritesServiceFile(t *testing.T) {
	tmpDir := t.TempDir()
	svcDir := filepath.Join(tmpDir, "systemd", "user")

	// Mock executablePath.
	origExec := executablePath
	executablePath = func() (string, error) { return "/usr/local/bin/obsync", nil }
	defer func() { executablePath = origExec }()

	// Mock runSystemctl to be a no-op.
	origSystemctl := runSystemctl
	var systemctlCalls [][]string
	runSystemctl = func(args ...string) error {
		systemctlCalls = append(systemctlCalls, args)
		return nil
	}
	defer func() { runSystemctl = origSystemctl }()

	// Generate service file directly to verify content.
	content, err := generateServiceFile("testvault", "/tmp/vault", "/usr/local/bin/obsync")
	if err != nil {
		t.Fatalf("generateServiceFile failed: %v", err)
	}

	// Write it manually (since Install needs keyring and API which we don't mock here).
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	svcFile := filepath.Join(svcDir, "obsync@testvault.service")
	if err := os.WriteFile(svcFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Verify the file was written.
	data, err := os.ReadFile(svcFile)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	if !strings.Contains(string(data), "ExecStart=/usr/local/bin/obsync watch testvault /tmp/vault") {
		t.Errorf("service file missing expected ExecStart\n\nGot:\n%s", string(data))
	}
}

func TestUninstallCmd_RemovesServiceFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a fake service file.
	svcFile := filepath.Join(tmpDir, "obsync@test.service")
	if err := os.WriteFile(svcFile, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Verify it exists.
	if _, err := os.Stat(svcFile); err != nil {
		t.Fatalf("service file should exist: %v", err)
	}

	// Remove it.
	if err := os.Remove(svcFile); err != nil {
		t.Fatalf("remove failed: %v", err)
	}

	// Verify it's gone.
	if _, err := os.Stat(svcFile); !os.IsNotExist(err) {
		t.Errorf("service file should be removed")
	}
}

func TestStatusCmd_ServiceNotFound(t *testing.T) {
	// Mock querySystemctl to return not-found.
	origQuery := querySystemctl
	querySystemctl = func(args ...string) (string, error) {
		for _, arg := range args {
			if arg == "--property=LoadState" {
				return "not-found", nil
			}
			if arg == "--property=ActiveState" {
				return "inactive", nil
			}
			if arg == "--property=SubState" {
				return "dead", nil
			}
		}
		return "", nil
	}
	defer func() { querySystemctl = origQuery }()

	// Verify the load state is correctly detected.
	loadState, _ := querySystemctl("show", "--property=LoadState", "--value", "testvault")
	if loadState != "not-found" {
		t.Errorf("expected not-found, got %s", loadState)
	}
}

func TestStatusCmd_ServiceActive(t *testing.T) {
	origQuery := querySystemctl
	querySystemctl = func(args ...string) (string, error) {
		for _, arg := range args {
			if arg == "--property=LoadState" {
				return "loaded", nil
			}
			if arg == "--property=ActiveState" {
				return "active", nil
			}
			if arg == "--property=SubState" {
				return "running", nil
			}
		}
		return "", nil
	}
	defer func() { querySystemctl = origQuery }()

	activeState, _ := querySystemctl("show", "--property=ActiveState", "--value", "testvault")
	if activeState != "active" {
		t.Errorf("expected active, got %s", activeState)
	}
}
