//go:build linux

package cmd

import (
	"errors"
	"fmt"
	"os"

	"obsync/internal/ui"
)

func platformUninstall(u *ui.UI, vaultID string) error {
	svcName := serviceName(vaultID)
	_ = runSystemctl("stop", svcName)
	_ = runSystemctl("disable", svcName)
	svcPath, err := servicePath(vaultID)
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
	if err := runSystemctl("daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	u.Out().Successf("Service %s stopped, disabled, and removed", svcName)
	return nil
}
