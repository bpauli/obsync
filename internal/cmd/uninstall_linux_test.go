//go:build linux

package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUninstallCmd_RemovesServiceFile(t *testing.T) {
	tmpDir := t.TempDir()
	svcFile := filepath.Join(tmpDir, "obsync@test.service")
	if err := os.WriteFile(svcFile, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if _, err := os.Stat(svcFile); err != nil {
		t.Fatalf("service file should exist: %v", err)
	}
	if err := os.Remove(svcFile); err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	if _, err := os.Stat(svcFile); !os.IsNotExist(err) {
		t.Errorf("service file should be removed")
	}
}
