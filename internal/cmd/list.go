package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"text/tabwriter"

	"obsync/internal/api"
	"obsync/internal/config"
	"obsync/internal/secrets"
	"obsync/internal/ui"
)

// ListCmd lists available Obsidian Sync vaults.
type ListCmd struct{}

func (c *ListCmd) Run(ctx context.Context, flags *RootFlags) error {
	u := ui.FromContext(ctx)

	cfgPath := flags.Config
	if cfgPath == "" {
		var err error
		cfgPath, err = config.DefaultConfigPath()
		if err != nil {
			return fmt.Errorf("resolve config path: %w", err)
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.Email == "" {
		return &ExitError{Code: ExitAuth, Err: errors.New("not logged in — run 'obsync login' first")}
	}

	store, err := secrets.OpenDefault()
	if err != nil {
		return &ExitError{Code: ExitConfig, Err: fmt.Errorf("open keyring: %w", err)}
	}

	token, err := store.GetToken(cfg.Email)
	if err != nil {
		return &ExitError{Code: ExitAuth, Err: fmt.Errorf("no token found — run 'obsync login' first: %w", err)}
	}

	slog.Debug("listing vaults", "email", cfg.Email)
	client := newAPIClient()
	resp, err := client.ListVaults(ctx, token)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			return &ExitError{Code: ExitAuth, Err: fmt.Errorf("list vaults: %s", apiErr.Message)}
		}
		return fmt.Errorf("list vaults: %w", err)
	}

	if flags.JSON {
		return c.printJSON(u, resp)
	}
	c.printTable(u, resp)
	return nil
}

func (c *ListCmd) printJSON(u *ui.UI, resp *api.ListVaultsResponse) error {
	enc := json.NewEncoder(u.Out().Writer())
	enc.SetIndent("", "  ")
	return enc.Encode(resp)
}

func (c *ListCmd) printTable(u *ui.UI, resp *api.ListVaultsResponse) {
	allVaults := make([]struct {
		vault  api.Vault
		shared bool
	}, 0, len(resp.Vaults)+len(resp.Shared))

	for _, v := range resp.Vaults {
		allVaults = append(allVaults, struct {
			vault  api.Vault
			shared bool
		}{vault: v, shared: false})
	}
	for _, v := range resp.Shared {
		allVaults = append(allVaults, struct {
			vault  api.Vault
			shared bool
		}{vault: v, shared: true})
	}

	if len(allVaults) == 0 {
		u.Out().Infof("No vaults found.")
		return
	}

	w := tabwriter.NewWriter(u.Out().Writer(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tE2E\tSHARED")
	for _, entry := range allVaults {
		v := entry.vault
		e2e := "no"
		if v.Password != "" {
			e2e = "yes"
		}
		shared := ""
		if entry.shared {
			shared = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", v.ID, v.Name, e2e, shared)
	}
	w.Flush()
}
