# obsync

[![release](https://github.com/bpauli/obsync/actions/workflows/release.yml/badge.svg)](https://github.com/bpauli/obsync/actions/workflows/release.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/bpauli/obsync)](https://goreportcard.com/report/github.com/bpauli/obsync)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)

A command-line tool for syncing [Obsidian](https://obsidian.md) vaults on headless Linux servers. Uses the official Obsidian Sync protocol over WebSocket with full end-to-end encryption.

Built for running Obsidian vaults on servers where the desktop app isn't available — perfect for automated workflows, CI/CD pipelines, or keeping a server-side copy of your notes.

## Features

- **Bidirectional sync** — pull remote changes and push local edits
- **Real-time watch mode** — continuous sync via WebSocket with filesystem monitoring
- **End-to-end encryption** — AES-256-GCM encryption, scrypt key derivation, compatible with Obsidian's E2E encryption
- **systemd integration** — install as a user service for always-on sync
- **Vault config sync** — syncs `.obsidian/` directory (themes, plugins, settings)
- **Headless operation** — file-based keyring backend, no GUI required
- **Chunked transfers** — handles large files with 2MB chunked uploads/downloads
- **Automatic reconnection** — exponential backoff (1s–60s) on connection loss
- **Hooks** — run custom scripts on sync events (file received, pre/post push, connection loss, etc.)

## Installation

### Homebrew (macOS / Linux)

```bash
brew install bpauli/tap/obsync
```

### Build from Source

Requires Go 1.25+.

```bash
git clone https://github.com/bpauli/obsync.git
cd obsync
go build -o obsync ./cmd/obsync
```

## Quick Start

### 1. Log in

```bash
obsync login
```

Enter your Obsidian account email and password. If you have MFA enabled, you'll be prompted for the code. Your auth token is stored in the system keyring.

### 2. List vaults

```bash
obsync list
```

Shows all vaults on your account with their IDs, names, and encryption status.

### 3. Pull a vault

```bash
obsync pull "My Notes" ~/notes -p "your-e2e-password"
```

Downloads all files from the remote vault to a local directory. Use `--save-password` / `-s` to store the E2E password in the keyring for future use.

### 4. Push local changes

```bash
obsync push "My Notes" ~/notes -p "your-e2e-password"
```

Uploads new and modified files, and sends delete notifications for removed files. Only changed files are pushed (compared by SHA-256 hash).

### 5. Watch (continuous sync)

```bash
obsync watch "My Notes" ~/notes -p "your-e2e-password"
```

Starts bidirectional real-time sync. Remote changes are pulled immediately via WebSocket. Local changes are detected via filesystem events (fsnotify) with a 500ms debounce.

## Hooks

Hooks let you run custom scripts in response to sync events — post-process files, trigger builds, send notifications, or commit changes to git.

### Configuration

Hooks are configured in JSON files at two levels:

| Location | Scope |
| --- | --- |
| `~/.config/obsync/hooks.json` | All vaults (global) |
| `<vault-path>/.obsync-hooks.json` | Single vault (local) |

Both files are loaded and merged additively.

### Example: auto-commit synced files to git

Create `~/notes/.obsync-hooks.json`:

```json
{
  "hooks": {
    "PostPull": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "git add -A && git diff --cached --quiet || git commit -m \"sync: $(date +%Y-%m-%d_%H:%M:%S)\" && git push origin main"
          }
        ]
      }
    ]
  }
}
```

This runs after every pull completes — it stages all changes, commits if there's anything new, and pushes to your remote.

### Hook events

#### File-level events (fire per file)

| Event | When it fires | Matcher |
| --- | --- | --- |
| `PostFileReceived` | After a file is pulled and written to disk | regex on file path |
| `PostFilePushed` | After a file is pushed to remote | regex on file path |
| `PostFileDeleted` | After a file is deleted | regex on file path |

#### Operation-level events (fire once per operation)

| Event | When it fires |
| --- | --- |
| `PrePull` | Before a pull begins |
| `PostPull` | After a pull completes |
| `PrePush` | Before a push begins |
| `PostPush` | After a push completes |

#### Watch-mode events

| Event | When it fires |
| --- | --- |
| `WatchStart` | When watch mode begins (after initial sync) |
| `WatchStop` | When watch mode ends (graceful shutdown) |
| `ConnectionLost` | When the WebSocket connection drops |
| `ConnectionRestored` | When reconnection succeeds |
| `SyncError` | When a non-fatal sync error occurs |

### Configuration schema

```json
{
  "hooks": {
    "PostFileReceived": [
      {
        "matcher": ".*\\.md$",
        "hooks": [
          {
            "type": "command",
            "command": "./scripts/process-note.sh",
            "timeout": 30
          }
        ]
      }
    ]
  }
}
```

| Field | Required | Default | Description |
| --- | --- | --- | --- |
| `type` | yes | — | Only `"command"` for now |
| `command` | yes | — | Shell command (run via `sh -c`) |
| `timeout` | no | 30 | Seconds before killing the process |

The `matcher` field is a regex matched against the file path for file-level events. Omit it or set to `""` / `"*"` to match all files. Operation and watch events ignore matchers.

### Exit codes

Hooks control flow via their exit code:

| Exit code | Behavior |
| --- | --- |
| `0` | Success — continue |
| `2` | **Block** — abort the current operation (stderr is shown as the error) |
| any other | Non-blocking warning — log and continue |

This means the hook script itself decides whether a failure should stop the sync or just warn. For example, a `PrePush` validation hook can `exit 2` to prevent the push, while a `PostFileReceived` notification hook can `exit 1` on failure without interrupting the pull.

### Stdin and environment

Each hook receives JSON context on stdin:

```json
{
  "event": "PostFileReceived",
  "vault_name": "My Notes",
  "vault_id": "abc123",
  "vault_path": "/home/user/notes",
  "file": {
    "path": "Daily Notes/2024-01-15.md",
    "local_path": "/home/user/notes/Daily Notes/2024-01-15.md",
    "size": 1234,
    "hash": "abcdef..."
  }
}
```

Hooks also receive these environment variables:

| Variable | Description |
| --- | --- |
| `OBSYNC_EVENT` | Event name (e.g. `PostFileReceived`) |
| `OBSYNC_VAULT_NAME` | Vault name |
| `OBSYNC_VAULT_ID` | Vault UID |
| `OBSYNC_VAULT_PATH` | Local vault directory path |
| `OBSYNC_FILE_PATH` | (file events only) Relative file path |

## Commands

| Command     | Description                                           |
| ----------- | ----------------------------------------------------- |
| `login`     | Log in to Obsidian Sync                               |
| `list`      | List available vaults                                 |
| `pull`      | Pull remote vault changes to a local directory        |
| `push`      | Push local changes to a remote vault                  |
| `watch`     | Watch and continuously sync a vault bidirectionally   |
| `install`   | Install a systemd user service for continuous sync    |
| `uninstall` | Uninstall the systemd user service for a vault        |
| `status`    | Show the status of the systemd user service           |

### Global Flags

```
-v, --verbose    Enable verbose/debug logging
-j, --json       Output JSON to stdout
    --config     Path to config file (or OBSYNC_CONFIG env var)
    --version    Print version and exit
```

### pull / push / watch Flags

```
-p, --password        E2E encryption password
-s, --save-password   Save E2E password to keyring for future use
```

## systemd Integration

Install obsync as a systemd user service for always-on vault sync:

```bash
# Install and start the service
obsync install "My Notes" ~/notes

# Check service status
obsync status "My Notes"

# View logs
journalctl --user -u obsync@<vault-id>.service -f

# Stop and remove the service
obsync uninstall "My Notes"
```

For headless servers (no active login session), enable lingering:

```bash
loginctl enable-linger $USER
```

The generated service file uses the `file` keyring backend automatically. Set `OBSYNC_KEYRING_PASSWORD` before installing if you use a custom keyring password.

## Security & Keyring

obsync stores credentials in the system keyring:

- **Auth token** — obtained via `obsync login`
- **E2E password** — optionally saved with `--save-password`

### Keyring Backends

| Backend          | Description                        | Platforms      |
| ---------------- | ---------------------------------- | -------------- |
| `auto`           | Auto-detect (default)              | All            |
| `keychain`       | macOS Keychain                     | macOS          |
| `secret-service` | GNOME Keyring / KWallet via D-Bus  | Linux (desktop)|
| `kwallet`        | KWallet directly                   | Linux (KDE)    |
| `file`           | Encrypted file (~/.obsync-keyring) | All (headless) |

Set the backend via environment variable:

```bash
export OBSYNC_KEYRING_BACKEND=file
export OBSYNC_KEYRING_PASSWORD=mysecret  # password for the file backend
```

The `file` backend is recommended for headless servers where no desktop keyring is available.

## Configuration

Config is stored at `~/.config/obsync/config.json`:

```json
{
  "email": "user@example.com",
  "device": "my-server"
}
```

| Field    | Description                                      |
| -------- | ------------------------------------------------ |
| `email`  | Obsidian account email (set by `obsync login`)   |
| `device` | Device name sent to sync server (defaults to hostname) |

Override the config path with `--config` or `OBSYNC_CONFIG`.

## Environment Variables

| Variable                  | Description                              |
| ------------------------- | ---------------------------------------- |
| `OBSYNC_CONFIG`           | Path to config file                      |
| `OBSYNC_KEYRING_BACKEND`  | Keyring backend (`auto`, `file`, etc.)   |
| `OBSYNC_KEYRING_PASSWORD` | Password for the `file` keyring backend  |

## Development

### Prerequisites

- Go 1.25+

### Build & Test

```bash
# Build
go build ./cmd/obsync

# Run tests
go test ./...

# Run with verbose logging
obsync -v watch "My Notes" ~/notes
```

### Project Structure

```
cmd/obsync/          Entry point
internal/
  api/               REST API client (auth, vault listing, access tokens)
  cmd/               CLI commands (login, list, pull, push, watch, install, ...)
  config/            Config file management
  crypto/            E2E encryption (AES-256-GCM, scrypt, path encoding)
  hooks/             Hook system (config loading, event dispatch, command runner)
  secrets/           Keyring abstraction (token + E2E password storage)
  sync/              WebSocket sync client (connect, push, pull, heartbeat)
  ui/                Terminal UI (colored output, prompts, spinners)
```

## Disclaimer

This is an unofficial, community-built tool. It is **not affiliated with, endorsed by, or supported by Obsidian** or Dynalist Inc. "Obsidian" is a trademark of Dynalist Inc.

obsync requires a valid [Obsidian Sync](https://obsidian.md/sync) subscription. It does not bypass any authentication or payment — users must log in with their own Obsidian account credentials.

Use at your own risk. The Obsidian Sync protocol is undocumented and may change without notice, which could break this tool at any time.

## License

MIT
