package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"obsync/internal/crypto"
)

const (
	// ChunkSize is the maximum size per binary frame (2MB).
	ChunkSize = 2 * 1024 * 1024

	pingInterval = 20 * time.Second
	writeTimeout = 30 * time.Second
	readTimeout  = 60 * time.Second
)

// PushMessage represents a server-sent push notification about a file change.
type PushMessage struct {
	Op      string `json:"op"`
	Path    string `json:"path"`
	Hash    string `json:"hash"`
	Size    int64  `json:"size"`
	CTime   int64  `json:"ctime"`
	MTime   int64  `json:"mtime"`
	Folder  bool   `json:"folder"`
	Deleted bool   `json:"deleted"`
	UID     int64  `json:"uid"`
	Device  string `json:"device"`
	Pieces  int    `json:"pieces"`
}

// initMessage is sent to the server after connecting.
type initMessage struct {
	Op                string `json:"op"`
	Token             string `json:"token"`
	ID                string `json:"id"`
	KeyHash           string `json:"keyhash"`
	Version           int64  `json:"version"`
	Initial           bool   `json:"initial"`
	Device            string `json:"device"`
	EncryptionVersion int    `json:"encryption_version"`
}

// readyMessage is sent by the server when initial sync is complete.
type readyMessage struct {
	Op      string `json:"op"`
	Version int64  `json:"version"`
}

// serverResponse is the generic response envelope from the server.
type serverResponse struct {
	Res        string `json:"res,omitempty"`
	Op         string `json:"op,omitempty"`
	PerFileMax int64  `json:"perFileMax,omitempty"`
	Version    int64  `json:"version,omitempty"`
	Error      string `json:"error,omitempty"`
}

// pushMetadata is the metadata sent with a push operation.
type pushMetadata struct {
	Op      string `json:"op"`
	Path    string `json:"path"`
	Hash    string `json:"hash"`
	Size    int64  `json:"size"`
	CTime   int64  `json:"ctime"`
	MTime   int64  `json:"mtime"`
	Folder  bool   `json:"folder"`
	Deleted bool   `json:"deleted"`
	Pieces  int    `json:"pieces"`
}

// pullRequest requests a file by its UID.
type pullRequest struct {
	Op  string `json:"op"`
	UID int64  `json:"uid"`
}

// pingMessage is sent to keep the connection alive.
type pingMessage struct {
	Op string `json:"op"`
}

// Client manages a WebSocket connection to the Obsidian Sync server.
type Client struct {
	conn       *websocket.Conn
	key        []byte
	perFileMax int64
	stopPing   chan struct{}
}

// ConnectParams holds parameters for connecting to the sync server.
type ConnectParams struct {
	Host              string
	Token             string
	VaultUID          string
	KeyHash           string
	Version           int64
	Initial           bool
	Device            string
	EncryptionVersion int
	Key               []byte
}

// Connect establishes a WebSocket connection and sends the init message.
// It waits for the server's "ok" response before returning.
func Connect(ctx context.Context, params ConnectParams) (*Client, error) {
	host := params.Host
	if !strings.HasSuffix(host, ".obsidian.md") {
		return nil, fmt.Errorf("sync: invalid host %q: must end with .obsidian.md", host)
	}

	url := "wss://" + host

	slog.Debug("connecting to sync server", "host", host)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("sync: connect: %w", err)
	}

	c := &Client{
		conn:     conn,
		key:      params.Key,
		stopPing: make(chan struct{}),
	}

	init := initMessage{
		Op:                "init",
		Token:             params.Token,
		ID:                params.VaultUID,
		KeyHash:           params.KeyHash,
		Version:           params.Version,
		Initial:           params.Initial,
		Device:            params.Device,
		EncryptionVersion: params.EncryptionVersion,
	}

	if err := c.writeJSON(init); err != nil {
		conn.Close()
		return nil, fmt.Errorf("sync: send init: %w", err)
	}

	// Read the server's initial response.
	var resp serverResponse
	if err := conn.ReadJSON(&resp); err != nil {
		conn.Close()
		return nil, fmt.Errorf("sync: read init response: %w", err)
	}

	if resp.Error != "" {
		conn.Close()
		return nil, fmt.Errorf("sync: server error: %s", resp.Error)
	}

	if resp.Res != "ok" {
		conn.Close()
		return nil, fmt.Errorf("sync: unexpected init response: %q", resp.Res)
	}

	c.perFileMax = resp.PerFileMax
	slog.Debug("connected", "perFileMax", c.perFileMax)

	return c, nil
}

// ReceivePush reads the next push message from the server.
// It handles pong messages internally and returns push messages and ready signals.
// When a "ready" message is received, it returns a PushMessage with Op="ready" and
// the server's version.
func (c *Client) ReceivePush(ctx context.Context) (*PushMessage, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		msgType, data, err := c.conn.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("sync: read message: %w", err)
		}

		if msgType != websocket.TextMessage {
			continue
		}

		var raw serverResponse
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("sync: decode message: %w", err)
		}

		switch {
		case raw.Op == "pong":
			continue
		case raw.Op == "ready":
			return &PushMessage{Op: "ready", UID: raw.Version}, nil
		case raw.Op == "push":
			var pm PushMessage
			if err := json.Unmarshal(data, &pm); err != nil {
				return nil, fmt.Errorf("sync: decode push: %w", err)
			}
			return &pm, nil
		default:
			slog.Debug("ignoring message", "op", raw.Op, "res", raw.Res)
		}
	}
}

// PullFile sends a pull request for a file UID and reads the binary response frames,
// reassembling and decrypting the content.
func (c *Client) PullFile(ctx context.Context, uid int64) ([]byte, error) {
	req := pullRequest{Op: "pull", UID: uid}
	if err := c.writeJSON(req); err != nil {
		return nil, fmt.Errorf("sync: send pull: %w", err)
	}

	// Read size response.
	var sizeResp struct {
		Op    string `json:"op"`
		Size  int64  `json:"size"`
		Pieces int   `json:"pieces"`
	}
	if err := c.conn.ReadJSON(&sizeResp); err != nil {
		return nil, fmt.Errorf("sync: read pull size: %w", err)
	}

	if sizeResp.Pieces == 0 {
		sizeResp.Pieces = 1
	}

	// Read binary frames.
	var encrypted []byte
	for i := 0; i < sizeResp.Pieces; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		msgType, data, err := c.conn.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("sync: read pull chunk %d: %w", i, err)
		}
		if msgType != websocket.BinaryMessage {
			return nil, fmt.Errorf("sync: expected binary frame, got type %d", msgType)
		}
		encrypted = append(encrypted, data...)
	}

	plaintext, err := crypto.Decrypt(c.key, encrypted)
	if err != nil {
		return nil, fmt.Errorf("sync: decrypt file: %w", err)
	}

	return plaintext, nil
}

// PushFile encrypts and sends a file to the server in 2MB chunks.
func (c *Client) PushFile(ctx context.Context, path string, data []byte, hash string, size int64, ctime, mtime int64, folder bool) error {
	encrypted, err := crypto.Encrypt(c.key, data)
	if err != nil {
		return fmt.Errorf("sync: encrypt file: %w", err)
	}

	encryptedPath, err := crypto.EncryptPath(c.key, path)
	if err != nil {
		return fmt.Errorf("sync: encrypt path: %w", err)
	}

	encryptedHash, err := crypto.EncryptPath(c.key, hash)
	if err != nil {
		return fmt.Errorf("sync: encrypt hash: %w", err)
	}

	pieces := (len(encrypted) + ChunkSize - 1) / ChunkSize
	if pieces == 0 {
		pieces = 1
	}

	meta := pushMetadata{
		Op:     "push",
		Path:   encryptedPath,
		Hash:   encryptedHash,
		Size:   int64(len(encrypted)),
		CTime:  ctime,
		MTime:  mtime,
		Folder: folder,
		Pieces: pieces,
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if err := c.writeJSON(meta); err != nil {
		return fmt.Errorf("sync: send push metadata: %w", err)
	}

	// Send binary chunks.
	for i := 0; i < pieces; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		start := i * ChunkSize
		end := start + ChunkSize
		if end > len(encrypted) {
			end = len(encrypted)
		}

		if err := c.conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
			return fmt.Errorf("sync: set write deadline: %w", err)
		}

		if err := c.conn.WriteMessage(websocket.BinaryMessage, encrypted[start:end]); err != nil {
			return fmt.Errorf("sync: send push chunk %d: %w", i, err)
		}
	}

	// Read push acknowledgement.
	var resp serverResponse
	if err := c.conn.ReadJSON(&resp); err != nil {
		return fmt.Errorf("sync: read push response: %w", err)
	}

	if resp.Error != "" {
		return fmt.Errorf("sync: push error: %s", resp.Error)
	}

	return nil
}

// PushDelete sends a delete notification for a path.
func (c *Client) PushDelete(ctx context.Context, path string) error {
	encryptedPath, err := crypto.EncryptPath(c.key, path)
	if err != nil {
		return fmt.Errorf("sync: encrypt path: %w", err)
	}

	meta := pushMetadata{
		Op:      "push",
		Path:    encryptedPath,
		Deleted: true,
		Pieces:  0,
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if err := c.writeJSON(meta); err != nil {
		return fmt.Errorf("sync: send delete: %w", err)
	}

	// Read acknowledgement.
	var resp serverResponse
	if err := c.conn.ReadJSON(&resp); err != nil {
		return fmt.Errorf("sync: read delete response: %w", err)
	}

	if resp.Error != "" {
		return fmt.Errorf("sync: delete error: %s", resp.Error)
	}

	return nil
}

// Ping sends a ping message to the server.
func (c *Client) Ping(ctx context.Context) error {
	msg := pingMessage{Op: "ping"}
	return c.writeJSON(msg)
}

// StartHeartbeat starts a goroutine that pings the server every 20 seconds.
// It stops when Close() is called or the context is cancelled.
func (c *Client) StartHeartbeat(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-c.stopPing:
				return
			case <-ticker.C:
				if err := c.Ping(ctx); err != nil {
					slog.Warn("heartbeat ping failed", "error", err)
					return
				}
			}
		}
	}()
}

// Close cleanly shuts down the WebSocket connection.
func (c *Client) Close() error {
	close(c.stopPing)

	err := c.conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
	)
	if err != nil {
		c.conn.Close()
		return fmt.Errorf("sync: close: %w", err)
	}
	return c.conn.Close()
}

// writeJSON writes a JSON message with a write deadline.
func (c *Client) writeJSON(v any) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	return c.conn.WriteJSON(v)
}
