package cmd

import (
	"context"

	"obsync/internal/ui"
)

// StatusCmd shows the status of the background service for a vault.
type StatusCmd struct {
	Vault string `arg:"" help:"Vault name or ID to check status for."`
}

func (c *StatusCmd) Run(ctx context.Context, flags *RootFlags) error {
	u := ui.FromContext(ctx)
	return platformStatus(u, c.Vault)
}
