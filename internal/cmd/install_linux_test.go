//go:build linux

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
	content, err := generateServiceFile("v1", "relative/path", "/usr/bin/obsync")
	if err != nil {
		t.Fatalf("generateServiceFile failed: %v", err)
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "ExecStart=") {
			parts := strings.Fields(line)
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
	origExec := executablePath
	executablePath = func() (string, error) { return "/usr/local/bin/obsync", nil }
	defer func() { executablePath = origExec }()
	origSystemctl := runSystemctl
	var systemctlCalls [][]string
	runSystemctl = func(args ...string) error {
		systemctlCalls = append(systemctlCalls, args)
		return nil
	}
	defer func() { runSystemctl = origSystemctl }()
	content, err := generateServiceFile("testvault", "/tmp/vault", "/usr/local/bin/obsync")
	if err != nil {
		t.Fatalf("generateServiceFile failed: %v", err)
	}
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	svcFile := filepath.Join(svcDir, "obsync@testvault.service")
	if err := os.WriteFile(svcFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	data, err := os.ReadFile(svcFile)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(string(data), "ExecStart=/usr/local/bin/obsync watch testvault /tmp/vault") {
		t.Errorf("service file missing expected ExecStart\n\nGot:\n%s", string(data))
	}
}
