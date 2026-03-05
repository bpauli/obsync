package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	stdpath "path"
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
	Err        string `json:"err,omitempty"`
}

func (r serverResponse) errMsg() string {
	if r.Error != "" {
		return r.Error
	}
	return r.Err
}

// pushMetadata is the metadata sent with a push operation.
type pushMetadata struct {
	Op          string `json:"op"`
	Path        string `json:"path"`
	Extension   string `json:"extension"`
	Hash        string `json:"hash"`
	Size        int64  `json:"size"`
	CTime       int64  `json:"ctime"`
	MTime       int64  `json:"mtime"`
	Folder      bool   `json:"folder"`
	Deleted     bool   `json:"deleted"`
	Pieces      int    `json:"pieces"`
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
	encVer     int
	perFileMax int64
	stopPing   chan struct{}

	// pushCh and binaryCh are used by the reader goroutine to dispatch
	// incoming messages. pushCh receives push/ready/pong text messages.
	// responseCh receives other JSON responses (push acks, pull size, etc.).
	// binaryCh receives binary frames (file content chunks).
	pushCh     chan []byte
	responseCh chan []byte
	binaryCh   chan []byte
	readerDone chan struct{}
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

	wsURL := "wss://" + host
	return connectToURL(ctx, wsURL, params)
}

// ConnectToURL connects to a raw WebSocket URL without host validation.
// This is intended for testing.
func ConnectToURL(ctx context.Context, rawURL string, params ConnectParams) (*Client, error) {
	return connectToURL(ctx, rawURL, params)
}

func connectToURL(ctx context.Context, wsURL string, params ConnectParams) (*Client, error) {
	slog.Debug("connecting to sync server", "url", wsURL)

	dialer := *websocket.DefaultDialer
	dialer.EnableCompression = true
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("sync: connect: %w", err)
	}

	c := &Client{
		conn:       conn,
		key:        params.Key,
		encVer:     params.EncryptionVersion,
		stopPing:   make(chan struct{}),
		pushCh:     make(chan []byte, 16),
		responseCh: make(chan []byte, 16),
		binaryCh:   make(chan []byte, 16),
		readerDone: make(chan struct{}),
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

	// Read the server's initial response (before reader goroutine starts).
	var resp serverResponse
	if err := conn.ReadJSON(&resp); err != nil {
		conn.Close()
		return nil, fmt.Errorf("sync: read init response: %w", err)
	}

	if errMsg := resp.errMsg(); errMsg != "" {
		conn.Close()
		return nil, fmt.Errorf("sync: server error: %s", errMsg)
	}

	if resp.Res != "ok" {
		conn.Close()
		return nil, fmt.Errorf("sync: unexpected init response: res=%q error=%q", resp.Res, resp.errMsg())
	}

	c.perFileMax = resp.PerFileMax
	slog.Debug("connected", "perFileMax", c.perFileMax)

	// Start the reader goroutine that dispatches messages to channels.
	go c.readLoop()

	return c, nil
}

// readLoop runs in a goroutine and dispatches all incoming WebSocket messages
// to the appropriate channel: pushCh for push/ready/pong, responseCh for other
// JSON messages, binaryCh for binary frames.
func (c *Client) readLoop() {
	defer close(c.readerDone)
	for {
		msgType, data, err := c.conn.ReadMessage()
		if err != nil {
			// Connection closed or error — close channels to signal consumers.
			close(c.pushCh)
			close(c.responseCh)
			close(c.binaryCh)
			return
		}

		if msgType == websocket.BinaryMessage {
			c.binaryCh <- data
			continue
		}

		// Text message — check op to decide which channel.
		var raw serverResponse
		if err := json.Unmarshal(data, &raw); err != nil {
			slog.Warn("failed to decode message", "error", err)
			continue
		}

		switch raw.Op {
		case "pong":
			// Ignore pong messages.
		case "push", "ready":
			c.pushCh <- data
		default:
			c.responseCh <- data
		}
	}
}

// ReceivePush reads the next push message from the server.
// When a "ready" message is received, it returns a PushMessage with Op="ready".
func (c *Client) ReceivePush(ctx context.Context) (*PushMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case data, ok := <-c.pushCh:
		if !ok {
			return nil, fmt.Errorf("sync: connection closed")
		}

		var raw serverResponse
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("sync: decode message: %w", err)
		}

		if raw.Op == "ready" {
			return &PushMessage{Op: "ready", UID: raw.Version}, nil
		}

		var pm PushMessage
		if err := json.Unmarshal(data, &pm); err != nil {
			return nil, fmt.Errorf("sync: decode push: %w", err)
		}
		return &pm, nil
	}
}

// ErrFileDeleted is returned when a pull request finds the file was deleted.
var ErrFileDeleted = fmt.Errorf("sync: file deleted on server")

// PullFile sends a pull request for a file UID and reads the binary response frames,
// reassembling and decrypting the content.
func (c *Client) PullFile(ctx context.Context, uid int64) ([]byte, error) {
	req := pullRequest{Op: "pull", UID: uid}
	if err := c.writeJSON(req); err != nil {
		return nil, fmt.Errorf("sync: send pull: %w", err)
	}

	// Read size response from responseCh.
	var sizeResp struct {
		Op      string `json:"op,omitempty"`
		Size    int64  `json:"size"`
		Pieces  int    `json:"pieces"`
		Deleted bool   `json:"deleted,omitempty"`
		Error   string `json:"error,omitempty"`
		Res     string `json:"res,omitempty"`
	}
	data, err := c.readResponse(ctx)
	if err != nil {
		return nil, fmt.Errorf("sync: read pull size: %w", err)
	}
	if err := json.Unmarshal(data, &sizeResp); err != nil {
		return nil, fmt.Errorf("sync: decode pull size: %w", err)
	}

	slog.Debug("pull response", "uid", uid, "size", sizeResp.Size, "pieces", sizeResp.Pieces,
		"deleted", sizeResp.Deleted, "error", sizeResp.Error, "res", sizeResp.Res)

	if sizeResp.Error != "" {
		return nil, fmt.Errorf("sync: pull error: %s", sizeResp.Error)
	}

	if sizeResp.Deleted {
		return nil, ErrFileDeleted
	}

	// Empty file — no binary frames to read.
	if sizeResp.Size == 0 && sizeResp.Pieces == 0 {
		return []byte{}, nil
	}

	if sizeResp.Pieces == 0 {
		sizeResp.Pieces = 1
	}

	// Read binary frames from binaryCh.
	var encrypted []byte
	for i := 0; i < sizeResp.Pieces; i++ {
		chunk, err := c.readBinary(ctx)
		if err != nil {
			return nil, fmt.Errorf("sync: read pull chunk %d: %w", i, err)
		}
		encrypted = append(encrypted, chunk...)
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

	encryptedPath, err := crypto.EncodePath(c.key, path, c.encVer)
	if err != nil {
		return fmt.Errorf("sync: encrypt path: %w", err)
	}

	encryptedHash, err := crypto.EncodePath(c.key, hash, c.encVer)
	if err != nil {
		return fmt.Errorf("sync: encrypt hash: %w", err)
	}

	pieces := (len(encrypted) + ChunkSize - 1) / ChunkSize
	if pieces == 0 {
		pieces = 1
	}

	ext := strings.TrimPrefix(stdpath.Ext(path), ".")

	meta := pushMetadata{
		Op:        "push",
		Path:      encryptedPath,
		Extension: ext,
		Hash:      encryptedHash,
		Size:      int64(len(encrypted)),
		CTime:     ctime,
		MTime:     mtime,
		Folder:    folder,
		Pieces:    pieces,
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if err := c.writeJSON(meta); err != nil {
		return fmt.Errorf("sync: send push metadata: %w", err)
	}

	// Read metadata response — server may say "ok" (already has this version).
	respData, err := c.readResponse(ctx)
	if err != nil {
		return fmt.Errorf("sync: read push meta response: %w", err)
	}

	var metaResp serverResponse
	if err := json.Unmarshal(respData, &metaResp); err != nil {
		return fmt.Errorf("sync: decode push meta response: %w", err)
	}

	if errMsg := metaResp.errMsg(); errMsg != "" {
		return fmt.Errorf("sync: push error: %s", errMsg)
	}

	if metaResp.Res == "ok" || metaResp.Op == "ok" {
		slog.Debug("push skipped (server has file)", "path", path)
		return nil
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

		// Read chunk acknowledgement.
		chunkData, err := c.readResponse(ctx)
		if err != nil {
			return fmt.Errorf("sync: read chunk %d response: %w", i, err)
		}

		var chunkResp serverResponse
		if err := json.Unmarshal(chunkData, &chunkResp); err != nil {
			return fmt.Errorf("sync: decode chunk %d response: %w", i, err)
		}

		if errMsg := chunkResp.errMsg(); errMsg != "" {
			return fmt.Errorf("sync: push chunk %d error: %s", i, errMsg)
		}
	}

	return nil
}

// PushDelete sends a delete notification for a path.
func (c *Client) PushDelete(ctx context.Context, path string) error {
	encryptedPath, err := crypto.EncodePath(c.key, path, c.encVer)
	if err != nil {
		return fmt.Errorf("sync: encrypt path: %w", err)
	}

	ext := strings.TrimPrefix(stdpath.Ext(path), ".")

	meta := pushMetadata{
		Op:        "push",
		Path:      encryptedPath,
		Extension: ext,
		Deleted:   true,
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
	respData, err := c.readResponse(ctx)
	if err != nil {
		return fmt.Errorf("sync: read delete response: %w", err)
	}

	var resp serverResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return fmt.Errorf("sync: decode delete response: %w", err)
	}

	if errMsg := resp.errMsg(); errMsg != "" {
		return fmt.Errorf("sync: delete error: %s", errMsg)
	}

	return nil
}

// readResponse reads the next JSON response from the responseCh.
func (c *Client) readResponse(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case data, ok := <-c.responseCh:
		if !ok {
			return nil, fmt.Errorf("sync: connection closed")
		}
		return data, nil
	}
}

// readBinary reads the next binary frame from the binaryCh.
func (c *Client) readBinary(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case data, ok := <-c.binaryCh:
		if !ok {
			return nil, fmt.Errorf("sync: connection closed")
		}
		return data, nil
	}
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
