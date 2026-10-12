package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"

	"obsync/internal/api"
	"obsync/internal/crypto"
	"obsync/internal/sync"
	"obsync/internal/ui"
)

// Stop watch only after its completion message, which follows the state save.
type uploadCompletionWriter struct {
	message string
	cancel  context.CancelFunc
}

func (w uploadCompletionWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), w.message) {
		w.cancel()
	}
	return len(p), nil
}

func TestUploadMetadata(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	key, err := crypto.DeriveKey("testpass", "testsalt")
	if err != nil {
		t.Fatal(err)
	}

	for _, mode := range []string{"push", "watch-initial", "watch-event"} {
		for _, tc := range []struct {
			name      string
			tracked   bool
			ctime     int64
			wantCTime int64
		}{
			{name: "existing", tracked: true, ctime: 1700000000000, wantCTime: 1700000000000},
			{name: "new", wantCTime: 1701000000000},
			{name: "unknown-creation-time", tracked: true, wantCTime: 1701000000000},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "note.md")
				state := sync.State{VaultUID: "vault-1", Version: 42, Files: make(map[string]sync.FileState)}
				if tc.tracked {
					state.Files["note.md"] = sync.FileState{
						Hash: "previous-hash", SyncHash: "previous-hash",
						CTime: tc.ctime, MTime: 1700000001000, Size: 12,
					}
				}
				if err := state.Save(dir); err != nil {
					t.Fatal(err)
				}

				// A second upload must retain the first upload's creation time,
				// including after a new file becomes a tracked file.
				for edit, content := range []string{"PC edit\n", "Next PC edit\n"} {
					mtime := int64(1701000000000) + int64(edit)*1000
					if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
						t.Fatal(err)
					}
					stamp := time.UnixMilli(mtime)
					if err := os.Chtimes(path, stamp, stamp); err != nil {
						t.Fatal(err)
					}

					uploaded := make(chan sync.PushMessage, 1)
					serverDone := make(chan struct{})
					cleanup := mockSyncServer(t, []api.Vault{{
						ID: "vault-1", Name: "My Notes", Password: "x", Salt: "testsalt", EncryptionVersion: 3,
					}}, func(conn *websocket.Conn) {
						defer close(serverDone)
						if err := conn.WriteJSON(map[string]any{"op": "ready", "version": 42}); err != nil {
							t.Error(err)
							return
						}
						var meta sync.PushMessage
						if err := conn.ReadJSON(&meta); err != nil {
							t.Error(err)
							return
						}
						uploaded <- meta
						if err := conn.WriteJSON(map[string]any{}); err != nil {
							t.Error(err)
							return
						}
						var encrypted []byte
						for i := 0; i < meta.Pieces; i++ {
							kind, chunk, err := conn.ReadMessage()
							if err != nil {
								t.Error(err)
								return
							}
							if kind != websocket.BinaryMessage {
								t.Errorf("frame type = %d, want binary", kind)
							}
							encrypted = append(encrypted, chunk...)
							if err := conn.WriteJSON(map[string]any{"res": "ok"}); err != nil {
								t.Error(err)
								return
							}
						}
						plain, err := crypto.Decrypt(key, encrypted)
						if err != nil || string(plain) != content {
							t.Errorf("uploaded content = %q, error = %v, want %q", plain, err, content)
						}
						if meta.Size != int64(len(encrypted)) {
							t.Errorf("wire size = %d, encrypted size = %d", meta.Size, len(encrypted))
						}
					})

					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					u, err := ui.New(ui.Options{
						Stdout: uploadCompletionWriter{message: map[string]string{
							"push": "Push complete", "watch-initial": "Initial sync:", "watch-event": "Pushed 1 files",
						}[mode], cancel: cancel},
						Stderr: io.Discard, Color: "never",
					})
					if err != nil {
						t.Fatal(err)
					}
					ctx = ui.WithUI(ctx, u)
					flags := &RootFlags{Config: writeConfig(t, "user@example.com")}
					switch mode {
					case "push":
						err = (&PushCmd{Vault: "vault-1", Path: dir, Password: "testpass"}).Run(ctx, flags)
					case "watch-initial":
						err = (&WatchCmd{Vault: "vault-1", Path: dir, Password: "testpass"}).Run(ctx, flags)
					case "watch-event":
						err = runUploadWatchEvent(ctx, dir, path, key, u)
					}
					cancel()
					cleanup()
					if err != nil {
						t.Fatalf("upload: %v", err)
					}
					select {
					case <-serverDone:
					case <-time.After(10 * time.Second):
						t.Fatal("upload server did not finish")
					}

					var meta sync.PushMessage
					select {
					case meta = <-uploaded:
					default:
						t.Fatal("no upload metadata")
					}
					if meta.Op != "push" || meta.Deleted || meta.Folder {
						t.Errorf("expected file upload, got %+v", meta)
					}
					plainPath, err := crypto.DecodePath(key, meta.Path, 3)
					if err != nil || plainPath != "note.md" {
						t.Errorf("uploaded path = %q, error = %v", plainPath, err)
					}
					hash, err := crypto.DecodePath(key, meta.Hash, 3)
					if err != nil || hash != sha256Hex([]byte(content)) {
						t.Errorf("uploaded hash = %q, error = %v", hash, err)
					}
					if meta.CTime != tc.wantCTime || meta.MTime != mtime {
						t.Errorf("upload timestamps = (%d, %d), want (%d, %d)", meta.CTime, meta.MTime, tc.wantCTime, mtime)
					}
					saved, err := sync.LoadState(dir)
					if err != nil {
						t.Fatal(err)
					}
					want := sync.FileState{Hash: hash, SyncHash: hash, CTime: tc.wantCTime, MTime: mtime, Size: int64(len(content))}
					if got := saved.Files["note.md"]; got != want {
						t.Errorf("saved state = %+v, want %+v", got, want)
					}
				}
			})
		}
	}
}

func runUploadWatchEvent(ctx context.Context, dir, path string, key []byte, u *ui.UI) error {
	state, err := sync.LoadState(dir)
	if err != nil {
		return err
	}
	sc, err := syncConnectFunc(ctx, sync.ConnectParams{Key: key, EncryptionVersion: 3, Version: state.Version})
	if err != nil {
		return err
	}
	defer sc.Close()
	if _, err := sc.ReceivePush(ctx); err != nil {
		return err
	}

	// Inject a file event, not an OS scheduling delay. The real watch loop
	// performs the upload and saves state after its debounce timer expires.
	watcher := &fsnotify.Watcher{Events: make(chan fsnotify.Event, 1), Errors: make(chan error)}
	watcher.Events <- fsnotify.Event{Name: path, Op: fsnotify.Write}
	oldDebounce := debounceDuration
	debounceDuration = 0
	defer func() { debounceDuration = oldDebounce }()
	return (&WatchCmd{Path: dir}).watchLoop(ctx, watcher, sc, key, &state, u)
}
