//go:build darwin

package cmd

import (
	"errors"
	"fmt"
	"os"

	"obsync/internal/ui"
)

func platformUninstall(u *ui.UI, vaultID string) error {
	label := plistLabel(vaultID)
	uid := fmt.Sprintf("gui/%d", os.Getuid())
	_ = runLaunchctl("bootout", uid+"/"+label)
	pPath, err := plistPath(vaultID)
	if err != nil {
		return err
	}
	if err := os.Remove(pPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			u.Out().Infof("Plist file not found: %s", pPath)
			return nil
		}
		return fmt.Errorf("remove plist file: %w", err)
	}
	u.Out().Successf("Service %s stopped and removed", label)
	return nil
}
