//go:build darwin

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLaunchctlPrint_Running(t *testing.T) {
	output := "{\n\t\"LimitLoadToSessionType\" = \"Aqua\";\n\t\"Label\" = \"com.obsync.testvault\";\n\t\"OnDemand\" = false;\n\t\"LastExitStatus\" = 0;\n\t\"PID\" = 12345;\n\t\"Program\" = \"/usr/local/bin/obsync\";\n};"
	pid, exitStatus := parseLaunchctlPrint(output)
	if pid != "12345" {
		t.Errorf("expected PID 12345, got %q", pid)
	}
	if exitStatus != "0" {
		t.Errorf("expected exit status 0, got %q", exitStatus)
	}
}

func TestParseLaunchctlPrint_Failed(t *testing.T) {
	output := "{\n\t\"Label\" = \"com.obsync.testvault\";\n\t\"LastExitStatus\" = 256;\n};"
	pid, exitStatus := parseLaunchctlPrint(output)
	if pid != "" {
		t.Errorf("expected no PID, got %q", pid)
	}
	if exitStatus != "256" {
		t.Errorf("expected exit status 256, got %q", exitStatus)
	}
}

func TestParseLaunchctlPrint_NoPID(t *testing.T) {
	output := "{\n\t\"Label\" = \"com.obsync.testvault\";\n\t\"LastExitStatus\" = 0;\n};"
	pid, exitStatus := parseLaunchctlPrint(output)
	if pid != "" {
		t.Errorf("expected no PID, got %q", pid)
	}
	if exitStatus != "0" {
		t.Errorf("expected exit status 0, got %q", exitStatus)
	}
}

func TestReadLogTail(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	if err := os.WriteFile(logFile, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	tail := readLogTail(logFile, 20)
	tailLines := strings.Split(tail, "\n")
	if len(tailLines) != 20 {
		t.Errorf("expected 20 lines, got %d", len(tailLines))
	}
	if tailLines[0] != "line 11" {
		t.Errorf("expected first line 'line 11', got %q", tailLines[0])
	}
	if tailLines[19] != "line 30" {
		t.Errorf("expected last line 'line 30', got %q", tailLines[19])
	}
}

func TestReadLogTail_FileNotFound(t *testing.T) {
	result := readLogTail("/nonexistent/file.log", 20)
	if result != "" {
		t.Errorf("expected empty string for missing file, got %q", result)
	}
}

func TestStatusCmd_ServiceNotFound_Darwin(t *testing.T) {
	origQuery := queryLaunchctl
	queryLaunchctl = func(args ...string) (string, error) {
		return "", fmt.Errorf("could not find service")
	}
	defer func() { queryLaunchctl = origQuery }()
	_, err := queryLaunchctl("print", "gui/501/com.obsync.testvault")
	if err == nil {
		t.Error("expected error for missing service")
	}
}

func TestStatusCmd_ServiceRunning_Darwin(t *testing.T) {
	origQuery := queryLaunchctl
	queryLaunchctl = func(args ...string) (string, error) {
		return "{\n\t\"PID\" = 12345;\n\t\"LastExitStatus\" = 0;\n};", nil
	}
	defer func() { queryLaunchctl = origQuery }()
	output, err := queryLaunchctl("print", "gui/501/com.obsync.testvault")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "PID") {
		t.Errorf("expected PID in output, got: %s", output)
	}
}

func TestStatusCmd_ServiceFailed_Darwin(t *testing.T) {
	origQuery := queryLaunchctl
	queryLaunchctl = func(args ...string) (string, error) {
		return "{\n\t\"Label\" = \"com.obsync.testvault\";\n\t\"LastExitStatus\" = 256;\n};", nil
	}
	defer func() { queryLaunchctl = origQuery }()
	output, err := queryLaunchctl("print", "gui/501/com.obsync.testvault")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "LastExitStatus") {
		t.Errorf("expected LastExitStatus in output, got: %s", output)
	}
}
