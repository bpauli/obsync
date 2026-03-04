package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/alecthomas/kong"

	"obsync/internal/ui"
)

// RootFlags holds global flags available to all commands.
type RootFlags struct {
	Verbose bool   `help:"Enable verbose logging" short:"v"`
	JSON    bool   `help:"Output JSON to stdout" short:"j"`
	Config  string `help:"Path to config file" type:"path" env:"OBSYNC_CONFIG"`
}

// CLI is the top-level Kong command struct.
type CLI struct {
	RootFlags `embed:""`

	Version kong.VersionFlag `help:"Print version and exit"`

	Login LoginCmd `cmd:"" help:"Log in to Obsidian Sync."`
	List  ListCmd  `cmd:"" help:"List available vaults."`
	Pull  PullCmd  `cmd:"" help:"Pull remote vault changes to a local directory."`
	Push  PushCmd  `cmd:"" help:"Push local changes to a remote vault."`
	Watch WatchCmd `cmd:"" help:"Watch and continuously sync a vault bidirectionally."`

	Install   InstallCmd   `cmd:"" help:"Install a systemd user service for continuous vault sync."`
	Uninstall UninstallCmd `cmd:"" help:"Uninstall the systemd user service for a vault."`
	Status    StatusCmd    `cmd:"" help:"Show the status of the systemd user service for a vault."`
}

// exitPanic is used to handle Kong's Exit() calls via panic/recover.
type exitPanic struct{ code int }

// Execute parses CLI arguments and runs the matched command.
func Execute(args []string) (err error) {
	parser, cli, err := newParser()
	if err != nil {
		return err
	}

	defer func() {
		if r := recover(); r != nil {
			if ep, ok := r.(exitPanic); ok {
				if ep.code == 0 {
					err = nil
					return
				}
				err = &ExitError{Code: ep.code, Err: errors.New("exited")}
				return
			}
			panic(r)
		}
	}()

	kctx, err := parser.Parse(args)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return &ExitError{Code: ExitUsage, Err: err}
	}

	logLevel := slog.LevelWarn
	if cli.Verbose {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	})))

	u, err := ui.New(ui.Options{})
	if err != nil {
		return err
	}

	ctx := ui.WithUI(context.Background(), u)
	kctx.BindTo(ctx, (*context.Context)(nil))
	kctx.Bind(&cli.RootFlags)

	if err := kctx.Run(); err != nil {
		if ExitCode(err) == 0 {
			return nil
		}
		return err
	}
	return nil
}

func newParser() (*kong.Kong, *CLI, error) {
	cli := &CLI{}
	parser, err := kong.New(
		cli,
		kong.Name("obsync"),
		kong.Description("Obsidian Sync CLI for headless servers"),
		kong.Vars{"version": VersionString()},
		kong.Exit(func(code int) { panic(exitPanic{code: code}) }),
	)
	if err != nil {
		return nil, nil, err
	}
	return parser, cli, nil
}
