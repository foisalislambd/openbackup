// Package api defines the wire contract between agents (desktop and mobile),
// the server, and the web dashboard.
//
// The protocol is deliberately plain HTTP/JSON with binary bodies for chunk
// transfer: it works through every corporate proxy and reverse proxy, is
// debuggable with curl, and is trivial to reimplement in Kotlin or Swift for
// the mobile clients. Everything is content-addressed, so every request is
// idempotent and safe to retry after a network drop.
package api

import "time"

// Version is the protocol version. The server rejects agents that speak a
// different major version.
const Version = "1"

// HTTP paths. Agents use /api/v1/agent/*, browsers use /api/v1/ui/*.
const (
	PathHealth = "/api/v1/health"

	PathEnroll         = "/api/v1/agent/enroll"
	PathDeviceSelf     = "/api/v1/agent/device"
	PathHeartbeat      = "/api/v1/agent/heartbeat"
	PathChunksMissing  = "/api/v1/agent/chunks/missing"
	PathChunk          = "/api/v1/agent/chunks/" // + digest
	PathSnapshots      = "/api/v1/agent/snapshots"
	PathKeyEscrow      = "/api/v1/agent/key"
	PathEvents         = "/api/v1/agent/events"
	PathSnapshotEntry  = "/entries"  // suffix of /snapshots/{id}
	PathSnapshotFinish = "/complete" // suffix of /snapshots/{id}
)

// Header names used by the protocol.
const (
	HeaderDeviceToken   = "Authorization" // "Bearer <device token>"
	HeaderProtocol      = "X-OpenBackup-Protocol"
	HeaderChunkPlainLen = "X-OpenBackup-Plain-Length"
	HeaderEncrypted     = "X-OpenBackup-Encrypted"
)

// Platform identifies an agent's operating system.
type Platform string

// Supported platforms.
const (
	PlatformWindows Platform = "windows"
	PlatformLinux   Platform = "linux"
	PlatformDarwin  Platform = "darwin"
	PlatformAndroid Platform = "android"
	PlatformIOS     Platform = "ios"
)

// ErrorResponse is returned for every non-2xx response.
type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Details string `json:"details,omitempty"`
}

// Error codes that clients act on.
const (
	CodeInvalidToken   = "invalid_token"
	CodeQuotaExceeded  = "quota_exceeded"
	CodeKeyMismatch    = "key_mismatch"
	CodeProtocolTooOld = "protocol_too_old"
	CodeNotFound       = "not_found"
	CodeRateLimited    = "rate_limited"
	// CodeEncryptionRequired tells an agent to turn on end-to-end encryption
	// before retrying, rather than to keep resending a chunk that will never be
	// accepted.
	CodeEncryptionRequired = "encryption_required"
)

// EnrollRequest pairs a new device with an account using a join token that the
// dashboard generates. One short-lived token per device keeps the flow to a
// single copy-paste (or QR scan on mobile) with no server URL editing.
type EnrollRequest struct {
	JoinToken    string   `json:"join_token"`
	DeviceName   string   `json:"device_name"`
	Hostname     string   `json:"hostname"`
	Platform     Platform `json:"platform"`
	OSVersion    string   `json:"os_version,omitempty"`
	AgentVersion string   `json:"agent_version"`
	// KeyID identifies the end-to-end encryption key this device holds, so the
	// server can reject a device that would write blobs nobody can read.
	KeyID string `json:"key_id,omitempty"`
}

// EnrollResponse returns the long-lived device credentials.
type EnrollResponse struct {
	DeviceID    string  `json:"device_id"`
	DeviceToken string  `json:"device_token"`
	UserID      string  `json:"user_id"`
	Policy      Policy  `json:"policy"`
	KeyEscrow   *Escrow `json:"key_escrow,omitempty"`
}

// Escrow carries the passphrase-wrapped master key so a reinstalled or new
// device can decrypt existing backups after the user types their passphrase.
// The server stores it as an opaque blob and cannot open it.
type Escrow struct {
	KeyID      string    `json:"key_id"`
	WrappedKey []byte    `json:"wrapped_key"`
	Salt       []byte    `json:"salt"`
	CreatedAt  time.Time `json:"created_at"`
}

// Policy is the server-side configuration an agent must honour. Keeping these
// on the server means an admin can fix a misbehaving fleet without touching
// each machine.
type Policy struct {
	// RetentionDays is how long snapshots are kept; 0 means keep everything.
	RetentionDays int `json:"retention_days"`
	// ChunkMinSize, ChunkAvgSize and ChunkMaxSize must match across devices or
	// deduplication degrades, so the server is the single source of truth.
	ChunkMinSize int `json:"chunk_min_size"`
	ChunkAvgSize int `json:"chunk_avg_size"`
	ChunkMaxSize int `json:"chunk_max_size"`
	// MaxUploadBytesPerSec throttles agents; 0 means unlimited.
	MaxUploadBytesPerSec int64 `json:"max_upload_bytes_per_sec"`
	// QuotaBytes is the account storage limit; 0 means unlimited.
	QuotaBytes int64 `json:"quota_bytes"`
	// RequireEncryption refuses unencrypted blobs.
	RequireEncryption bool `json:"require_encryption"`
	// HeartbeatSeconds is how often agents report in.
	HeartbeatSeconds int `json:"heartbeat_seconds"`
}

// DefaultPolicy returns the policy a fresh server hands out.
func DefaultPolicy() Policy {
	return Policy{
		RetentionDays:     30,
		ChunkMinSize:      256 << 10,
		ChunkAvgSize:      1 << 20,
		ChunkMaxSize:      4 << 20,
		HeartbeatSeconds:  60,
		RequireEncryption: false,
	}
}

// HeartbeatRequest reports agent liveness and progress.
type HeartbeatRequest struct {
	State         string  `json:"state"`
	StateReason   string  `json:"state_reason,omitempty"`
	QueuedFiles   int64   `json:"queued_files"`
	QueuedBytes   int64   `json:"queued_bytes"`
	UploadedBytes int64   `json:"uploaded_bytes"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryBytes   int64   `json:"memory_bytes"`
	AgentVersion  string  `json:"agent_version"`
	OSVersion     string  `json:"os_version,omitempty"`
	BatteryPct    int     `json:"battery_pct,omitempty"`
	OnMetered     bool    `json:"on_metered,omitempty"`
	LastError     string  `json:"last_error,omitempty"`
}

// HeartbeatResponse lets the server steer the agent without a push channel.
type HeartbeatResponse struct {
	Policy Policy `json:"policy"`
	// Commands are one-shot instructions such as "run a full rescan now" or
	// "pause". They are delivered on the heartbeat the agent already makes,
	// which avoids maintaining a websocket per device.
	Commands   []Command `json:"commands,omitempty"`
	ServerTime time.Time `json:"server_time"`
}

// Command is a server-issued instruction.
type Command struct {
	ID   string            `json:"id"`
	Kind CommandKind       `json:"kind"`
	Args map[string]string `json:"args,omitempty"`
}

// CommandKind enumerates server-issued instructions.
type CommandKind string

// Supported commands.
const (
	CommandRescan     CommandKind = "rescan"
	CommandPause      CommandKind = "pause"
	CommandResume     CommandKind = "resume"
	CommandBackupNow  CommandKind = "backup_now"
	CommandReloadConf CommandKind = "reload_config"
	CommandForget     CommandKind = "forget" // device removed; wipe local credentials
)

// MissingChunksRequest asks which chunks the server still needs. This single
// round trip is what makes cross-device deduplication work: a file already
// uploaded from a laptop costs the phone nothing but the digest.
type MissingChunksRequest struct {
	Digests []string `json:"digests"`
}

// MissingChunksResponse lists the digests the agent must upload.
type MissingChunksResponse struct {
	Missing []string `json:"missing"`
}

// EntryType distinguishes tree entries.
type EntryType string

// Entry types.
const (
	EntryFile    EntryType = "file"
	EntryDir     EntryType = "dir"
	EntrySymlink EntryType = "symlink"
)

// Entry is one node in a snapshot tree.
type Entry struct {
	// Path is relative to the snapshot root, slash separated.
	Path string    `json:"path"`
	Type EntryType `json:"type"`
	Size int64     `json:"size,omitempty"`
	// Mode holds POSIX permission bits; ignored on Windows restores.
	Mode uint32 `json:"mode,omitempty"`
	// ModTime is truncated to the second: sub-second precision is not portable
	// across filesystems and would cause spurious re-uploads.
	ModTime time.Time `json:"mtime"`
	// Chunks lists the content-defined chunk digests in order.
	Chunks []string `json:"chunks,omitempty"`
	// Digest is the whole-file digest, used to detect changes cheaply.
	Digest string `json:"digest,omitempty"`
	// LinkTarget is set for symlinks.
	LinkTarget string `json:"link_target,omitempty"`
}

// StartSnapshotRequest opens a snapshot. Entries are streamed afterwards in
// batches so an agent with a million files never has to hold the tree in RAM.
type StartSnapshotRequest struct {
	// Roots are the backup roots included in this snapshot.
	Roots []SnapshotRoot `json:"roots"`
	// ParentID is the previous snapshot for this device, when known. The server
	// uses it to present incremental diffs.
	ParentID  string    `json:"parent_id,omitempty"`
	StartedAt time.Time `json:"started_at"`
	// Kind distinguishes a full walk from a watcher-triggered delta.
	Kind  SnapshotKind `json:"kind"`
	KeyID string       `json:"key_id,omitempty"`
}

// SnapshotKind describes how a snapshot was produced.
type SnapshotKind string

// Snapshot kinds.
const (
	// SnapshotFull is a complete walk of all roots.
	SnapshotFull SnapshotKind = "full"
	// SnapshotDelta contains only entries the watcher reported as changed, and
	// inherits everything else from its parent.
	SnapshotDelta SnapshotKind = "delta"
)

// SnapshotRoot describes one backed-up root.
type SnapshotRoot struct {
	// Name is a stable identifier such as "documents" or "custom-1".
	Name string `json:"name"`
	// Path is the absolute path on the device.
	Path string `json:"path"`
}

// StartSnapshotResponse returns the new snapshot id.
type StartSnapshotResponse struct {
	SnapshotID string `json:"snapshot_id"`
}

// AddEntriesRequest appends a batch of entries to an open snapshot.
type AddEntriesRequest struct {
	Entries []Entry `json:"entries"`
	// Deleted lists paths removed since the parent snapshot, used by deltas.
	Deleted []string `json:"deleted,omitempty"`
}

// CompleteSnapshotRequest closes a snapshot.
type CompleteSnapshotRequest struct {
	CompletedAt time.Time `json:"completed_at"`
	FileCount   int64     `json:"file_count"`
	DirCount    int64     `json:"dir_count"`
	TotalBytes  int64     `json:"total_bytes"`
	// UploadedBytes is what actually crossed the network after dedup and
	// compression, which is the number users care about.
	UploadedBytes int64  `json:"uploaded_bytes"`
	SkippedCount  int64  `json:"skipped_count"`
	Error         string `json:"error,omitempty"`
}

// Snapshot is the server's view of a snapshot.
type Snapshot struct {
	ID            string         `json:"id"`
	DeviceID      string         `json:"device_id"`
	DeviceName    string         `json:"device_name,omitempty"`
	Kind          SnapshotKind   `json:"kind"`
	Status        string         `json:"status"`
	ParentID      string         `json:"parent_id,omitempty"`
	StartedAt     time.Time      `json:"started_at"`
	CompletedAt   *time.Time     `json:"completed_at,omitempty"`
	FileCount     int64          `json:"file_count"`
	DirCount      int64          `json:"dir_count"`
	TotalBytes    int64          `json:"total_bytes"`
	UploadedBytes int64          `json:"uploaded_bytes"`
	SkippedCount  int64          `json:"skipped_count"`
	Roots         []SnapshotRoot `json:"roots"`
	Error         string         `json:"error,omitempty"`
}

// Snapshot statuses.
const (
	SnapshotStatusRunning  = "running"
	SnapshotStatusComplete = "complete"
	SnapshotStatusFailed   = "failed"
)

// Event is a log line an agent reports for the dashboard's activity feed.
type Event struct {
	At      time.Time `json:"at"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	Path    string    `json:"path,omitempty"`
	// Reason explains skipped paths, so the dashboard can answer "why is this
	// folder not in my backup?".
	Reason string `json:"reason,omitempty"`
}

// EventsRequest uploads a batch of events.
type EventsRequest struct {
	Events []Event `json:"events"`
}

// Device is the dashboard's view of a device.
type Device struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Hostname     string     `json:"hostname"`
	Platform     Platform   `json:"platform"`
	OSVersion    string     `json:"os_version,omitempty"`
	AgentVersion string     `json:"agent_version"`
	KeyID        string     `json:"key_id,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	LastSeen     *time.Time `json:"last_seen,omitempty"`
	State        string     `json:"state"`
	StateReason  string     `json:"state_reason,omitempty"`
	QueuedFiles  int64      `json:"queued_files"`
	QueuedBytes  int64      `json:"queued_bytes"`
	LastError    string     `json:"last_error,omitempty"`
	// LogicalBytes is the size of the newest snapshot's contents.
	LogicalBytes  int64  `json:"logical_bytes"`
	SnapshotCount int64  `json:"snapshot_count"`
	Health        string `json:"health"`
}

// Device health values shown in the dashboard.
const (
	HealthOK       = "ok"
	HealthStale    = "stale"
	HealthError    = "error"
	HealthNeverRun = "never_run"
)

// AgentState is the coarse state an agent reports. It is an alias rather than a
// distinct type so it can be stored and compared as the plain string it is on
// the wire.
type AgentState = string

// Agent state values reported in heartbeats.
const (
	StateIdle      = "idle"
	StateScanning  = "scanning"
	StateUploading = "uploading"
	StatePaused    = "paused"
	StateError     = "error"
)

// UsageStats summarises storage for the dashboard.
type UsageStats struct {
	// LogicalBytes is the total size of the files users think they stored.
	LogicalBytes int64 `json:"logical_bytes"`
	// StoredBytes is the on-disk size after deduplication and compression.
	StoredBytes   int64 `json:"stored_bytes"`
	ChunkCount    int64 `json:"chunk_count"`
	DeviceCount   int64 `json:"device_count"`
	SnapshotCount int64 `json:"snapshot_count"`
	QuotaBytes    int64 `json:"quota_bytes"`
	// DedupRatio is LogicalBytes / StoredBytes, the headline efficiency number.
	DedupRatio    float64 `json:"dedup_ratio"`
	FreeDiskBytes int64   `json:"free_disk_bytes"`
}
