package cmd

import (
	"context"

	"obsync/internal/ui"
)

// UninstallCmd stops and removes the background service for a vault.
type UninstallCmd struct {
	Vault string `arg:"" help:"Vault name or ID to uninstall."`
}

func (c *UninstallCmd) Run(ctx context.Context, flags *RootFlags) error {
	u := ui.FromContext(ctx)
	return platformUninstall(u, c.Vault)
}
