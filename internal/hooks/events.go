package hooks

// EventType identifies a hook lifecycle event.
type EventType string

const (
	// File-level events.
	PostFileReceived EventType = "PostFileReceived"
	PostFilePushed   EventType = "PostFilePushed"
	PostFileDeleted  EventType = "PostFileDeleted"

	// Operation-level events.
	PrePull  EventType = "PrePull"
	PostPull EventType = "PostPull"
	PrePush  EventType = "PrePush"
	PostPush EventType = "PostPush"

	// Watch-mode events.
	WatchStart         EventType = "WatchStart"
	WatchStop          EventType = "WatchStop"
	ConnectionLost     EventType = "ConnectionLost"
	ConnectionRestored EventType = "ConnectionRestored"
	SyncError          EventType = "SyncError"
)

// FileInfo holds metadata about a file involved in an event.
type FileInfo struct {
	Path      string `json:"path"`
	LocalPath string `json:"local_path"`
	Size      int64  `json:"size"`
	Hash      string `json:"hash,omitempty"`
}

// OperationStats holds statistics about a completed operation.
type OperationStats struct {
	FilesSynced  int `json:"files_synced"`
	FilesDeleted int `json:"files_deleted"`
}

// Event is the input payload passed to hooks on stdin.
type Event struct {
	Event     EventType       `json:"event"`
	VaultName string          `json:"vault_name"`
	VaultID   string          `json:"vault_id"`
	VaultPath string          `json:"vault_path"`
	File      *FileInfo       `json:"file,omitempty"`
	Stats     *OperationStats `json:"stats,omitempty"`
	Error     string          `json:"error,omitempty"`
}
