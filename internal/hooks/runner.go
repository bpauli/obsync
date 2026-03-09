package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

const defaultTimeout = 30 // seconds

// Runner executes hooks in response to events.
type Runner struct {
	cfg       *HooksConfig
	vaultName string
	vaultID   string
	vaultPath string
}

// NewRunner creates a Runner. If cfg is nil or has no hooks, the Runner is a no-op.
func NewRunner(cfg *HooksConfig, vaultName, vaultID, vaultPath string) *Runner {
	return &Runner{
		cfg:       cfg,
		vaultName: vaultName,
		vaultID:   vaultID,
		vaultPath: vaultPath,
	}
}

// exitCodeBlock is the exit code that signals a hook wants to block the operation.
const exitCodeBlock = 2

// BlockedError is returned when a hook exits with code 2.
type BlockedError struct {
	Hook   string
	Reason string
}

func (e *BlockedError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("hook %q blocked operation: %s", e.Hook, e.Reason)
	}
	return fmt.Sprintf("hook %q blocked operation", e.Hook)
}

// Fire dispatches an event to all matching hooks. For file-level events, the
// matcher is checked against the file path. For other events, all hooks fire.
// If a hook exits with code 2, Fire returns a *BlockedError. Any other non-zero
// exit code logs a warning and continues.
func (r *Runner) Fire(ctx context.Context, event Event) error {
	if r == nil || r.cfg == nil || len(r.cfg.Hooks) == 0 {
		return nil
	}

	groups, ok := r.cfg.Hooks[event.Event]
	if !ok || len(groups) == 0 {
		return nil
	}

	// Fill in vault context.
	event.VaultName = r.vaultName
	event.VaultID = r.vaultID
	event.VaultPath = r.vaultPath

	filePath := ""
	if event.File != nil {
		filePath = event.File.Path
	}

	for i := range groups {
		mg := &groups[i]
		if event.File != nil && !mg.Matches(filePath) {
			continue
		}

		for _, h := range mg.Hooks {
			if h.Type != "command" {
				slog.Warn("unsupported hook type", "type", h.Type)
				continue
			}

			if err := r.execHook(ctx, h, event); err != nil {
				return err
			}
		}
	}

	return nil
}

func (r *Runner) execHook(ctx context.Context, h HookHandler, event Event) error {
	timeout := defaultTimeout
	if h.Timeout > 0 {
		timeout = h.Timeout
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	inputJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal hook input: %w", err)
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", h.Command)
	cmd.Stdin = bytes.NewReader(inputJSON)
	cmd.Dir = r.vaultPath

	// Set environment variables.
	cmd.Env = append(os.Environ(),
		"OBSYNC_EVENT="+string(event.Event),
		"OBSYNC_VAULT_NAME="+r.vaultName,
		"OBSYNC_VAULT_ID="+r.vaultID,
		"OBSYNC_VAULT_PATH="+r.vaultPath,
	)
	if event.File != nil {
		cmd.Env = append(cmd.Env, "OBSYNC_FILE_PATH="+event.File.Path)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	slog.Debug("running hook", "event", event.Event, "command", h.Command)

	runErr := cmd.Run()
	if runErr == nil {
		return nil
	}

	exitCode := -1
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		exitCode = exitErr.ExitCode()
	}

	if exitCode == exitCodeBlock {
		reason := strings.TrimSpace(stderr.String())
		return &BlockedError{Hook: h.Command, Reason: reason}
	}

	slog.Warn("hook failed (non-blocking)", "event", event.Event, "command", h.Command, "exit_code", exitCode, "stderr", strings.TrimSpace(stderr.String()))
	return nil
}
