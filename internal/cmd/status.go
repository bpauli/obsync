package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"obsync/internal/ui"
)

// StatusCmd shows the status of a systemd user service for vault sync.
type StatusCmd struct {
	Vault string `arg:"" help:"Vault name or ID to check status for."`
}

// querySystemctl is a var for testability.
var querySystemctl = func(args ...string) (string, error) {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func (c *StatusCmd) Run(ctx context.Context, flags *RootFlags) error {
	u := ui.FromContext(ctx)

	svcName := serviceName(c.Vault)

	// Check if service file exists.
	svcPath, err := servicePath(c.Vault)
	if err != nil {
		return err
	}

	activeState, _ := querySystemctl("show", "--property=ActiveState", "--value", svcName)
	subState, _ := querySystemctl("show", "--property=SubState", "--value", svcName)
	loadState, _ := querySystemctl("show", "--property=LoadState", "--value", svcName)

	if loadState == "not-found" {
		u.Out().Infof("Service %s is not installed", svcName)
		u.Out().Infof("Run 'obsync install %s <path>' to set it up", c.Vault)
		return nil
	}

	u.Out().Printf("Service:  %s", svcName)
	u.Out().Printf("File:     %s", svcPath)
	u.Out().Printf("Active:   %s (%s)", activeState, subState)

	if activeState == "active" {
		u.Out().Successf("Service is running")
	} else if activeState == "failed" {
		u.Out().Errorf("Service has failed")
		// Show recent logs.
		logs, err := querySystemctl("status", svcName)
		if err == nil && logs != "" {
			u.Out().Printf("\nRecent output:\n%s", logs)
		}
		return &ExitError{Code: ExitGeneric, Err: fmt.Errorf("service %s has failed", svcName)}
	} else {
		u.Out().Infof("Service is %s", activeState)
	}

	return nil
}
