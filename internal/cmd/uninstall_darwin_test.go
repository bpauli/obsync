//go:build darwin

package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUninstallCmd_RemovesPlistFile(t *testing.T) {
	tmpDir := t.TempDir()
	plistFile := filepath.Join(tmpDir, "com.obsync.test.plist")
	if err := os.WriteFile(plistFile, []byte("<plist/>\n"), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	origLaunchctl := runLaunchctl
	var launchctlCalls [][]string
	runLaunchctl = func(args ...string) error {
		launchctlCalls = append(launchctlCalls, args)
		return nil
	}
	defer func() { runLaunchctl = origLaunchctl }()
	if _, err := os.Stat(plistFile); err != nil {
		t.Fatalf("plist file should exist: %v", err)
	}
	if err := os.Remove(plistFile); err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	if _, err := os.Stat(plistFile); !os.IsNotExist(err) {
		t.Errorf("plist file should be removed")
	}
}
