//go:build darwin

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratePlistFile(t *testing.T) {
	content, err := generatePlistFile("abc123", "/Users/test/vault", "/usr/local/bin/obsync")
	if err != nil {
		t.Fatalf("generatePlistFile failed: %v", err)
	}
	checks := []string{
		"<string>com.obsync.abc123</string>",
		"<string>/usr/local/bin/obsync</string>",
		"<string>watch</string>",
		"<string>abc123</string>",
		"<string>/Users/test/vault</string>",
		"<key>RunAtLoad</key>",
		"<true/>",
		"<key>KeepAlive</key>",
		"<key>SuccessfulExit</key>",
		"<false/>",
		"<key>StandardOutPath</key>",
		"<key>StandardErrorPath</key>",
		"obsync/abc123.out.log",
		"obsync/abc123.err.log",
		"/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("plist missing %q\n\nGot:\n%s", check, content)
		}
	}
}

func TestGeneratePlistFile_RelativePath(t *testing.T) {
	content, err := generatePlistFile("v1", "relative/path", "/usr/bin/obsync")
	if err != nil {
		t.Fatalf("generatePlistFile failed: %v", err)
	}
	if strings.Contains(content, "<string>relative/path</string>") {
		t.Errorf("plist should contain absolute path, not relative\n\nGot:\n%s", content)
	}
}

func TestPlistPath(t *testing.T) {
	path, err := plistPath("abc123")
	if err != nil {
		t.Fatalf("plistPath failed: %v", err)
	}
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, "Library", "LaunchAgents", "com.obsync.abc123.plist")
	if path != expected {
		t.Errorf("expected %s, got %s", expected, path)
	}
}

func TestPlistLabel(t *testing.T) {
	label := plistLabel("abc123")
	if label != "com.obsync.abc123" {
		t.Errorf("expected com.obsync.abc123, got %s", label)
	}
}

func TestInstallCmd_WritesPlistFile(t *testing.T) {
	tmpDir := t.TempDir()
	launchAgentsDir := filepath.Join(tmpDir, "Library", "LaunchAgents")
	origExec := executablePath
	executablePath = func() (string, error) { return "/usr/local/bin/obsync", nil }
	defer func() { executablePath = origExec }()
	origLaunchctl := runLaunchctl
	var launchctlCalls [][]string
	runLaunchctl = func(args ...string) error {
		launchctlCalls = append(launchctlCalls, args)
		return nil
	}
	defer func() { runLaunchctl = origLaunchctl }()
	content, err := generatePlistFile("testvault", "/tmp/vault", "/usr/local/bin/obsync")
	if err != nil {
		t.Fatalf("generatePlistFile failed: %v", err)
	}
	if err := os.MkdirAll(launchAgentsDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	plistFile := filepath.Join(launchAgentsDir, "com.obsync.testvault.plist")
	if err := os.WriteFile(plistFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	data, err := os.ReadFile(plistFile)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(string(data), "<string>/usr/local/bin/obsync</string>") {
		t.Errorf("plist missing expected binary path\n\nGot:\n%s", string(data))
	}
	if !strings.Contains(string(data), "<string>testvault</string>") {
		t.Errorf("plist missing expected vault ID\n\nGot:\n%s", string(data))
	}
}
