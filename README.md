# obsync

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

## Installation

### From Source

```bash
go install obsync@latest
```

### Build from Source

```bash
git clone https://github.com/bpauli/obsync.git
cd obsync
go build -o obsync ./cmd/obsync
```

Set version info at build time:

```bash
go build -ldflags "-X obsync/internal/cmd.version=1.0.0 -X obsync/internal/cmd.commit=$(git rev-parse --short HEAD) -X obsync/internal/cmd.date=$(date -u +%Y-%m-%d)" -o obsync ./cmd/obsync
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
  secrets/           Keyring abstraction (token + E2E password storage)
  sync/              WebSocket sync client (connect, push, pull, heartbeat)
  ui/                Terminal UI (colored output, prompts, spinners)
```

## License

MIT
