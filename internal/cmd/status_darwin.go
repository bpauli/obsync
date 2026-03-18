//go:build darwin

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"obsync/internal/ui"
)

var queryLaunchctl = func(args ...string) (string, error) {
	cmd := exec.Command("launchctl", args...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func parseLaunchctlPrint(output string) (pid string, exitStatus string) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "\"PID\"") || strings.HasPrefix(line, "PID") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				pid = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(parts[1]), ";"))
			}
		}
		if strings.HasPrefix(line, "\"LastExitStatus\"") || strings.HasPrefix(line, "LastExitStatus") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				exitStatus = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(parts[1]), ";"))
			}
		}
	}
	return pid, exitStatus
}

func readLogTail(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func platformStatus(u *ui.UI, vaultID string) error {
	label := plistLabel(vaultID)
	pPath, err := plistPath(vaultID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(pPath); os.IsNotExist(err) {
		u.Out().Infof("Service %s is not installed", label)
		u.Out().Infof("Run 'obsync install %s <path>' to set it up", vaultID)
		return nil
	}
	uid := fmt.Sprintf("gui/%d", os.Getuid())
	target := uid + "/" + label
	output, err := queryLaunchctl("print", target)
	u.Out().Printf("Service:  %s", label)
	u.Out().Printf("Plist:    %s", pPath)
	if err != nil {
		u.Out().Infof("Service is not loaded")
		return nil
	}
	pid, exitStatus := parseLaunchctlPrint(output)
	if pid != "" {
		u.Out().Printf("PID:      %s", pid)
		u.Out().Successf("Service is running")
	} else {
		u.Out().Printf("Last exit: %s", exitStatus)
		if exitStatus != "" && exitStatus != "0" {
			u.Out().Errorf("Service has exited with error")
			logd, _ := logDir()
			if logd != "" {
				errLog := fmt.Sprintf("%s/%s.err.log", logd, vaultID)
				if tail := readLogTail(errLog, 20); tail != "" {
					u.Out().Printf("\nRecent stderr:\n%s", tail)
				}
			}
			return &ExitError{Code: ExitGeneric, Err: fmt.Errorf("service %s has failed", label)}
		}
		u.Out().Infof("Service is not running")
	}
	return nil
}
