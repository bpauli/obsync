package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"obsync/internal/ui"
)

// UninstallCmd stops, disables, and removes a systemd user service for vault sync.
type UninstallCmd struct {
	Vault string `arg:"" help:"Vault name or ID to uninstall."`
}

func (c *UninstallCmd) Run(ctx context.Context, flags *RootFlags) error {
	u := ui.FromContext(ctx)

	svcName := serviceName(c.Vault)

	// Stop the service (ignore errors if not running).
	_ = runSystemctl("stop", svcName)

	// Disable the service.
	_ = runSystemctl("disable", svcName)

	// Remove the service file.
	svcPath, err := servicePath(c.Vault)
	if err != nil {
		return err
	}

	if err := os.Remove(svcPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			u.Out().Infof("Service file not found: %s", svcPath)
			return nil
		}
		return fmt.Errorf("remove service file: %w", err)
	}

	// Reload daemon.
	if err := runSystemctl("daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}

	u.Out().Successf("Service %s stopped, disabled, and removed", svcName)
	return nil
}
