// Package httpapi implements the server's HTTP surface: the agent protocol, the
// dashboard API, and the embedded web UI.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/foisalislambd/openbackup/internal/api"
	"github.com/foisalislambd/openbackup/internal/codec"
	"github.com/foisalislambd/openbackup/internal/server/config"
	"github.com/foisalislambd/openbackup/internal/server/store"
)

// Server holds the HTTP dependencies.
type Server struct {
	cfg   config.Config
	db    *store.DB
	blobs store.Blobs
	log   *slog.Logger
	// codec decodes unencrypted blobs so the dashboard can offer direct
	// downloads. End-to-end encrypted blobs are never decodable here, by design.
	codec *codec.Codec
	// webFS serves the built dashboard; nil disables the UI.
	webFS http.Handler

	// commands is the pending instruction queue per device. Commands are
	// transient by design: they are delivered on the next heartbeat, and a
	// server restart simply drops them rather than replaying stale orders.
	commandsMu sync.Mutex
	commands   map[string][]api.Command

	// usageCache avoids a quota query per uploaded chunk.
	usageMu    sync.Mutex
	usageCache map[string]cachedUsage

	loginLimiter *attemptLimiter
}

type cachedUsage struct {
	bytes     int64
	expiresAt time.Time
}

// usageCacheTTL bounds how stale a quota decision can be. Uploads keep the
// cached figure current as they go, so this only matters after deletions.
const usageCacheTTL = 15 * time.Second

// Options configures a Server.
type Options struct {
	Config config.Config
	DB     *store.DB
	Blobs  store.Blobs
	Logger *slog.Logger
	// WebFS serves the dashboard assets. Optional.
	WebFS http.Handler
}

// New builds a Server.
func New(opts Options) (*Server, error) {
	if opts.DB == nil || opts.Blobs == nil {
		return nil, errors.New("httpapi: db and blob store are required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	c, err := codec.New(codec.Options{})
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:          opts.Config,
		db:           opts.DB,
		blobs:        opts.Blobs,
		log:          opts.Logger,
		codec:        c,
		webFS:        opts.WebFS,
		commands:     make(map[string][]api.Command),
		usageCache:   make(map[string]cachedUsage),
		loginLimiter: newAttemptLimiter(10, 15*time.Minute),
	}, nil
}

// Close releases resources.
func (s *Server) Close() { s.codec.Close() }

// Handler returns the fully wired HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET "+api.PathHealth, s.handleHealth)

	// Agent protocol.
	mux.HandleFunc("POST "+api.PathEnroll, s.handleEnroll)
	mux.Handle("GET "+api.PathDeviceSelf, s.agentOnly(s.handleDeviceSelf))
	mux.Handle("POST "+api.PathHeartbeat, s.agentOnly(s.handleHeartbeat))
	mux.Handle("POST "+api.PathChunksMissing, s.agentOnly(s.handleMissingChunks))
	mux.Handle("PUT /api/v1/agent/chunks/{digest}", s.agentOnly(s.handlePutChunk))
	mux.Handle("GET /api/v1/agent/chunks/{digest}", s.agentOnly(s.handleGetChunk))
	mux.Handle("POST "+api.PathSnapshots, s.agentOnly(s.handleStartSnapshot))
	mux.Handle("GET "+api.PathSnapshots, s.agentOnly(s.handleAgentListSnapshots))
	mux.Handle("POST /api/v1/agent/snapshots/{id}/entries", s.agentOnly(s.handleAddEntries))
	mux.Handle("GET /api/v1/agent/snapshots/{id}/entries", s.agentOnly(s.handleAgentTree))
	mux.Handle("POST /api/v1/agent/snapshots/{id}/complete", s.agentOnly(s.handleCompleteSnapshot))
	mux.Handle("PUT "+api.PathKeyEscrow, s.agentOnly(s.handlePutEscrow))
	mux.Handle("GET "+api.PathKeyEscrow, s.agentOnly(s.handleGetEscrow))
	mux.Handle("POST "+api.PathEvents, s.agentOnly(s.handleAgentEvents))

	// Dashboard API.
	mux.HandleFunc("GET /api/v1/ui/bootstrap", s.handleBootstrap)
	mux.HandleFunc("POST /api/v1/ui/setup", s.handleSetup)
	mux.HandleFunc("POST /api/v1/ui/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/ui/logout", s.handleLogout)
	mux.Handle("GET /api/v1/ui/me", s.userOnly(s.handleMe))
	mux.Handle("POST /api/v1/ui/password", s.userOnly(s.handleChangePassword))
	mux.Handle("GET /api/v1/ui/devices", s.userOnly(s.handleListDevices))
	mux.Handle("PATCH /api/v1/ui/devices/{id}", s.userOnly(s.handleUpdateDevice))
	mux.Handle("DELETE /api/v1/ui/devices/{id}", s.userOnly(s.handleDeleteDevice))
	mux.Handle("POST /api/v1/ui/devices/{id}/commands", s.userOnly(s.handleQueueCommand))
	mux.Handle("GET /api/v1/ui/usage", s.userOnly(s.handleUsage))
	mux.Handle("GET /api/v1/ui/history", s.userOnly(s.handleHistory))
	mux.Handle("GET /api/v1/ui/snapshots", s.userOnly(s.handleListSnapshots))
	mux.Handle("GET /api/v1/ui/snapshots/{id}", s.userOnly(s.handleGetSnapshot))
	mux.Handle("DELETE /api/v1/ui/snapshots/{id}", s.userOnly(s.handleDeleteSnapshot))
	mux.Handle("GET /api/v1/ui/snapshots/{id}/browse", s.userOnly(s.handleBrowse))
	mux.Handle("GET /api/v1/ui/files/versions", s.userOnly(s.handleFileVersions))
	mux.Handle("GET /api/v1/ui/snapshots/{id}/download", s.userOnly(s.handleDownloadFile))
	mux.Handle("GET /api/v1/ui/snapshots/{id}/archive", s.userOnly(s.handleDownloadArchive))
	mux.Handle("GET /api/v1/ui/events", s.userOnly(s.handleListEvents))
	mux.Handle("GET /api/v1/ui/join-tokens", s.userOnly(s.handleListJoinTokens))
	mux.Handle("POST /api/v1/ui/join-tokens", s.userOnly(s.handleCreateJoinToken))
	mux.Handle("GET /api/v1/ui/settings", s.userOnly(s.handleGetSettings))
	mux.Handle("PUT /api/v1/ui/settings", s.userOnly(s.handleUpdateSettings))
	mux.HandleFunc("GET /api/v1/ui/ignore-rules", s.handleIgnoreRules)

	if s.webFS != nil {
		mux.Handle("/", s.webFS)
	} else {
		mux.HandleFunc("/", s.handleNoUI)
	}

	var h http.Handler = mux
	h = s.withBodyLimit(h)
	h = s.withSecurityHeaders(h)
	h = s.withLogging(h)
	h = s.withRecovery(h)
	return h
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"protocol": api.Version,
		"time":     time.Now().UTC(),
	})
}

func (s *Server) handleNoUI(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, api.CodeNotFound, "unknown endpoint")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "OpenBackup server is running, but this build has no bundled dashboard. " +
			"Build the web UI with 'make web' or use the CLI.",
	})
}

// treeQuery reads the paging options a snapshot listing accepts. Browsers pass
// children=1 to walk one folder at a time; restores omit it and take the whole
// subtree.
func treeQuery(r *http.Request) store.TreeQuery {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	return store.TreeQuery{
		Prefix:     q.Get("prefix"),
		Cursor:     q.Get("cursor"),
		Limit:      limit,
		DirectOnly: q.Get("children") == "1",
	}
}

// decodeJSON reads a JSON body with a size limit already applied by middleware.
func decodeJSON(r *http.Request, out any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	return nil
}

// writeJSON sends a JSON response.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already written, so there is nothing to do but
		// note it; the client will see a truncated body and retry.
		slog.Default().Debug("write json response", "error", err)
	}
}

// writeError sends a structured error.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, api.ErrorResponse{Error: message, Code: code})
}

// writeStoreError maps store errors onto HTTP status codes.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrBlobNotFound):
		writeError(w, http.StatusNotFound, api.CodeNotFound, "not found")
	case errors.Is(err, context.Canceled):
		// The client hung up; no response will be read anyway.
	default:
		writeError(w, http.StatusInternalServerError, "", "internal error")
	}
}
