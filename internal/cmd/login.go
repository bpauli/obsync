package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"golang.org/x/term"

	"obsync/internal/api"
	"obsync/internal/config"
	"obsync/internal/secrets"
	"obsync/internal/ui"
)

// LoginCmd authenticates with Obsidian and stores the token securely.
type LoginCmd struct {
	Email    string `arg:"" help:"Obsidian account email address."`
	Password string `help:"Account password (prompts if not provided)." short:"p"`
	MFA      string `help:"MFA code if two-factor authentication is enabled." short:"m"`
}

// newAPIClient creates a new API client. Package-level var for test injection.
var newAPIClient = func() *api.Client { return api.NewClient() }

func (c *LoginCmd) Run(ctx context.Context, flags *RootFlags) error {
	u := ui.FromContext(ctx)

	password := c.Password
	if password == "" {
		var err error
		password, err = promptPassword(u)
		if err != nil {
			return fmt.Errorf("read password: %w", err)
		}
	}

	slog.Debug("signing in", "email", c.Email)
	client := newAPIClient()
	resp, err := client.Signin(ctx, c.Email, password, c.MFA)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			return &ExitError{Code: ExitAuth, Err: fmt.Errorf("login failed: %s", apiErr.Message)}
		}
		return &ExitError{Code: ExitAuth, Err: fmt.Errorf("login failed: %w", err)}
	}

	store, err := secrets.OpenDefault()
	if err != nil {
		return &ExitError{Code: ExitConfig, Err: fmt.Errorf("open keyring: %w", err)}
	}

	if err := store.SetToken(c.Email, resp.Token); err != nil {
		return fmt.Errorf("store token: %w", err)
	}

	cfgPath := flags.Config
	if cfgPath == "" {
		cfgPath, err = config.DefaultConfigPath()
		if err != nil {
			return fmt.Errorf("resolve config path: %w", err)
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cfg.Email = c.Email
	if err := config.Save(cfgPath, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	name := resp.Name
	if name == "" {
		name = resp.Email
	}
	u.Out().Successf("Logged in as %s", name)
	return nil
}

// promptPassword reads a password from the terminal without echo.
// This is a package-level var for test injection.
var promptPassword = func(u *ui.UI) (string, error) {
	u.Err().Print("Password: ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr) // newline after hidden input
	if err != nil {
		return "", err
	}
	pw := string(b)
	if pw == "" {
		return "", errors.New("password cannot be empty")
	}
	return pw, nil
}
