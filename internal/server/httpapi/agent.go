package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/foisalislambd/openbackup/internal/api"
	"github.com/foisalislambd/openbackup/internal/codec"
	"github.com/foisalislambd/openbackup/internal/hash"
	"github.com/foisalislambd/openbackup/internal/idgen"
	"github.com/foisalislambd/openbackup/internal/server/auth"
	"github.com/foisalislambd/openbackup/internal/server/store"
)

// maxDigestsPerRequest bounds a missing-chunks query so one agent cannot pin a
// CPU with a million digests.
const maxDigestsPerRequest = 4096

// maxEntriesPerRequest bounds a snapshot entry batch.
const maxEntriesPerRequest = 2000

// handleEnroll exchanges a join code for device credentials.
func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	var req api.EnrollRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "", "invalid request body")
		return
	}
	code := idgen.NormalizeJoinCode(req.JoinToken)
	if code == "" {
		writeError(w, http.StatusBadRequest, "", "join code is required")
		return
	}
	// Enrolment guesses are rate limited per source address: join codes are
	// short enough to be worth guessing otherwise.
	if !s.loginLimiter.Allow("enroll:" + s.clientIP(r)) {
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, api.CodeRateLimited, "too many enrolment attempts, try again later")
		return
	}

	deviceID := idgen.NewPrefixed("dev")
	userID, err := s.db.ConsumeJoinToken(r.Context(), auth.HashToken(code), deviceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, api.CodeInvalidToken, "join code is invalid, already used, or expired")
			return
		}
		writeStoreError(w, err)
		return
	}

	escrow, err := s.db.Escrow(r.Context(), userID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	// If the account already has an encryption key, a device arriving with a
	// different one would write blobs the other devices cannot read. Refuse now
	// rather than discovering it at restore time.
	if escrow != nil && req.KeyID != "" && escrow.KeyID != req.KeyID {
		writeError(w, http.StatusConflict, api.CodeKeyMismatch,
			"this account already uses a different encryption key; enter the account passphrase to reuse it")
		return
	}

	token, err := idgen.Secret(32)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	name := strings.TrimSpace(req.DeviceName)
	if name == "" {
		name = strings.TrimSpace(req.Hostname)
	}
	if name == "" {
		name = "Unnamed device"
	}
	device, err := s.db.CreateDevice(r.Context(), store.Device{
		ID:           deviceID,
		UserID:       userID,
		Name:         name,
		Hostname:     req.Hostname,
		Platform:     req.Platform,
		OSVersion:    req.OSVersion,
		AgentVersion: req.AgentVersion,
		KeyID:        req.KeyID,
	}, auth.HashToken(token))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.loginLimiter.Reset("enroll:" + s.clientIP(r))

	policy, err := s.policyFor(r, userID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.log.Info("device enrolled", "device", device.ID, "user", userID, "platform", req.Platform, "name", name)
	writeJSON(w, http.StatusOK, api.EnrollResponse{
		DeviceID:    device.ID,
		DeviceToken: token,
		UserID:      userID,
		Policy:      policy,
		KeyEscrow:   escrow,
	})
}

// policyFor builds the policy an agent must honour: chunking parameters that
// must match across the fleet for deduplication to work, plus the account
// settings the owner controls from the dashboard.
func (s *Server) policyFor(r *http.Request, userID string) (api.Policy, error) {
	policy := api.DefaultPolicy()
	policy.RetentionDays = s.cfg.RetentionDays
	policy.QuotaBytes = s.cfg.QuotaBytes
	user, err := s.db.UserByID(r.Context(), userID)
	if err != nil {
		return policy, err
	}
	return applyUserPolicy(policy, user.Policy), nil
}

// applyUserPolicy overlays an account's choices on the server defaults. Config
// values act as the floor: an environment variable can set a quota for a fresh
// install, and the dashboard can then narrow it per account.
func applyUserPolicy(policy api.Policy, user store.UserPolicy) api.Policy {
	policy.RetentionDays = user.RetentionDays
	if user.QuotaBytes > 0 {
		policy.QuotaBytes = user.QuotaBytes
	}
	if user.MaxUploadBytesPerSec > 0 {
		policy.MaxUploadBytesPerSec = user.MaxUploadBytesPerSec
	}
	if user.RequireEncryption {
		policy.RequireEncryption = true
	}
	return policy
}

func (s *Server) handleDeviceSelf(w http.ResponseWriter, r *http.Request, device *store.Device) {
	policy, err := s.policyFor(r, device.UserID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	parent, err := s.db.LatestSnapshotID(r.Context(), device.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"device_id":       device.ID,
		"name":            device.Name,
		"policy":          policy,
		"latest_snapshot": parent,
		"server_time":     time.Now().UTC(),
	})
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request, device *store.Device) {
	var req api.HeartbeatRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "", "invalid request body")
		return
	}
	if err := s.db.TouchDevice(r.Context(), device.ID, req); err != nil {
		writeStoreError(w, err)
		return
	}
	policy, err := s.policyFor(r, device.UserID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.HeartbeatResponse{
		Policy:     policy,
		Commands:   s.takeCommands(device.ID),
		ServerTime: time.Now().UTC(),
	})
}

// queueCommand adds a pending instruction for a device.
func (s *Server) queueCommand(deviceID string, cmd api.Command) {
	s.commandsMu.Lock()
	defer s.commandsMu.Unlock()
	queue := s.commands[deviceID]
	// Cap the queue: a device that is offline for a week should not receive
	// fifty stale "backup now" orders when it returns.
	if len(queue) >= 8 {
		queue = queue[len(queue)-7:]
	}
	s.commands[deviceID] = append(queue, cmd)
}

// takeCommands drains the queue for a device.
func (s *Server) takeCommands(deviceID string) []api.Command {
	s.commandsMu.Lock()
	defer s.commandsMu.Unlock()
	queue := s.commands[deviceID]
	if len(queue) == 0 {
		return nil
	}
	delete(s.commands, deviceID)
	return queue
}

func (s *Server) handleMissingChunks(w http.ResponseWriter, r *http.Request, device *store.Device) {
	var req api.MissingChunksRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "", "invalid request body")
		return
	}
	if len(req.Digests) > maxDigestsPerRequest {
		writeError(w, http.StatusRequestEntityTooLarge, "",
			"too many digests in one request; send at most "+strconv.Itoa(maxDigestsPerRequest))
		return
	}
	for _, d := range req.Digests {
		if err := hash.Validate(d); err != nil {
			writeError(w, http.StatusBadRequest, "", "malformed digest: "+d)
			return
		}
	}
	missing, err := s.db.MissingChunks(r.Context(), req.Digests)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.MissingChunksResponse{Missing: missing})
}

func (s *Server) handlePutChunk(w http.ResponseWriter, r *http.Request, device *store.Device) {
	digest := r.PathValue("digest")
	if err := hash.Validate(digest); err != nil {
		writeError(w, http.StatusBadRequest, "", "malformed digest")
		return
	}
	if _, exists := s.blobs.Has(r.Context(), digest); exists {
		// Idempotent: the agent may be retrying after a timeout that actually
		// succeeded, or another device may have uploaded the same content.
		if err := s.registerChunk(r, digest, nil); err != nil {
			writeStoreError(w, err)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	if err := s.checkQuota(r, device.UserID); err != nil {
		writeError(w, http.StatusInsufficientStorage, api.CodeQuotaExceeded, err.Error())
		return
	}

	blob, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "", "could not read request body")
		return
	}
	if len(blob) == 0 {
		writeError(w, http.StatusBadRequest, "", "empty chunk")
		return
	}

	// Verify content addressing whenever possible. For unencrypted blobs the
	// server can decode and re-hash, which catches a broken agent or a corrupt
	// proxy before the data is committed. Encrypted blobs carry their own AEAD
	// tag and are verified by the client on restore, since the server has no key.
	if !codec.IsEncrypted(blob) {
		// A plaintext chunk can be verified against its digest, which catches a
		// corrupted upload before it is stored under the wrong name. Encrypted
		// chunks are opaque by design, so integrity there rests on the AEAD tag
		// the agent checks when restoring.
		plain, err := s.codec.Decode(blob)
		if err != nil {
			writeError(w, http.StatusBadRequest, "", "chunk could not be decoded: "+err.Error())
			return
		}
		if !store.VerifyDigest(digest, plain) {
			writeError(w, http.StatusBadRequest, "", "chunk content does not match its digest")
			return
		}
		if s.requiresEncryption(r, device) {
			writeError(w, http.StatusBadRequest, api.CodeEncryptionRequired,
				"this account only accepts end-to-end encrypted backups")
			return
		}
	}

	written, err := s.blobs.Put(r.Context(), digest, blob)
	if err != nil {
		s.log.Error("store blob", "digest", digest, "error", err)
		writeStoreError(w, err)
		return
	}
	if err := s.registerChunk(r, digest, blob); err != nil {
		writeStoreError(w, err)
		return
	}
	// Add the new bytes to the cached usage rather than re-running the aggregate
	// query, which would otherwise execute once per uploaded chunk. Without this
	// the quota would only be noticed after the cache expired, by which point an
	// agent could have written far past the limit.
	s.addUsage(device.UserID, written)
	w.WriteHeader(http.StatusCreated)
}

// requiresEncryption reports whether this device's account refuses plaintext.
// A database error errs on the side of accepting the upload: losing a backup
// because a query failed would be the worse outcome, and the setting is about
// the account's own privacy rather than the server's safety.
func (s *Server) requiresEncryption(r *http.Request, device *store.Device) bool {
	if s.cfg.RequireEncryption {
		return true
	}
	user, err := s.db.UserByID(r.Context(), device.UserID)
	if err != nil {
		return false
	}
	return user.Policy.RequireEncryption
}

// registerChunk records chunk metadata, deriving sizes from the blob when it is
// available and from the header hint otherwise.
func (s *Server) registerChunk(r *http.Request, digest string, blob []byte) error {
	stored, _ := s.blobs.Has(r.Context(), digest)
	plainLen, _ := strconv.ParseInt(r.Header.Get(api.HeaderChunkPlainLen), 10, 64)
	if plainLen <= 0 {
		plainLen = stored
	}
	encrypted := len(blob) > 0 && codec.IsEncrypted(blob)
	return s.db.RegisterChunk(r.Context(), digest, stored, plainLen, encrypted)
}

func (s *Server) handleGetChunk(w http.ResponseWriter, r *http.Request, device *store.Device) {
	digest := r.PathValue("digest")
	if err := hash.Validate(digest); err != nil {
		writeError(w, http.StatusBadRequest, "", "malformed digest")
		return
	}
	f, size, err := s.blobs.Open(r.Context(), digest)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	// Content-addressed data is immutable, so it can be cached forever.
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	if _, err := io.Copy(w, f); err != nil {
		// The response is already streaming, so the only useful action is to log
		// it; the agent will notice the short read and retry.
		s.log.Debug("stream chunk to agent", "digest", digest, "error", err)
	}
}

func (s *Server) handleStartSnapshot(w http.ResponseWriter, r *http.Request, device *store.Device) {
	var req api.StartSnapshotRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "", "invalid request body")
		return
	}
	if device.KeyID != "" && req.KeyID != "" && device.KeyID != req.KeyID {
		writeError(w, http.StatusConflict, api.CodeKeyMismatch, "encryption key does not match the enrolled key")
		return
	}
	id, err := s.db.StartSnapshot(r.Context(), device.UserID, device.ID, req)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, api.StartSnapshotResponse{SnapshotID: id})
}

func (s *Server) handleAddEntries(w http.ResponseWriter, r *http.Request, device *store.Device) {
	snapshotID := r.PathValue("id")
	if err := s.assertSnapshotOwner(r, device, snapshotID); err != nil {
		writeStoreError(w, err)
		return
	}
	var req api.AddEntriesRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "", "invalid request body")
		return
	}
	if len(req.Entries) > maxEntriesPerRequest {
		writeError(w, http.StatusRequestEntityTooLarge, "",
			"too many entries in one batch; send at most "+strconv.Itoa(maxEntriesPerRequest))
		return
	}
	for _, e := range req.Entries {
		for _, d := range e.Chunks {
			if err := hash.Validate(d); err != nil {
				writeError(w, http.StatusBadRequest, "", "entry "+e.Path+" has a malformed chunk digest")
				return
			}
		}
	}
	if err := s.db.AddEntries(r.Context(), snapshotID, req.Entries, req.Deleted); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCompleteSnapshot(w http.ResponseWriter, r *http.Request, device *store.Device) {
	snapshotID := r.PathValue("id")
	if err := s.assertSnapshotOwner(r, device, snapshotID); err != nil {
		writeStoreError(w, err)
		return
	}
	var req api.CompleteSnapshotRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "", "invalid request body")
		return
	}
	if err := s.db.CompleteSnapshot(r.Context(), snapshotID, req); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusConflict, "", "snapshot is not open")
			return
		}
		// A failed completion is a real answer, not a server fault: the snapshot
		// was recorded as failed and the agent should report why to the user.
		writeError(w, http.StatusUnprocessableEntity, "", err.Error())
		return
	}
	s.invalidateUsage(device.UserID)
	writeJSON(w, http.StatusOK, map[string]string{"snapshot_id": snapshotID, "status": api.SnapshotStatusComplete})
}

func (s *Server) handleAgentListSnapshots(w http.ResponseWriter, r *http.Request, device *store.Device) {
	snapshots, err := s.db.ListSnapshots(r.Context(), device.UserID, device.ID, 50)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snapshots})
}

// handleAgentTree serves snapshot entries so the agent can restore.
func (s *Server) handleAgentTree(w http.ResponseWriter, r *http.Request, device *store.Device) {
	snapshotID := r.PathValue("id")
	// Restores may read any snapshot on the account, not just this device's:
	// that is the point of restoring a dead laptop onto a new one.
	if _, err := s.db.SnapshotByID(r.Context(), device.UserID, snapshotID); err != nil {
		writeStoreError(w, err)
		return
	}
	entries, next, err := s.db.Tree(r.Context(), snapshotID, treeQuery(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "next_cursor": next})
}

func (s *Server) handlePutEscrow(w http.ResponseWriter, r *http.Request, device *store.Device) {
	var req api.Escrow
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "", "invalid request body")
		return
	}
	if req.KeyID == "" || len(req.WrappedKey) == 0 || len(req.Salt) == 0 {
		writeError(w, http.StatusBadRequest, "", "key_id, wrapped_key and salt are required")
		return
	}
	existing, err := s.db.Escrow(r.Context(), device.UserID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	// Overwriting an existing escrow with a different key would orphan every
	// blob already stored under the old one.
	if existing != nil && existing.KeyID != req.KeyID {
		writeError(w, http.StatusConflict, api.CodeKeyMismatch,
			"this account already has a different encryption key stored")
		return
	}
	if err := s.db.PutEscrow(r.Context(), device.UserID, req); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetEscrow(w http.ResponseWriter, r *http.Request, device *store.Device) {
	escrow, err := s.db.Escrow(r.Context(), device.UserID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, escrow)
}

func (s *Server) handleAgentEvents(w http.ResponseWriter, r *http.Request, device *store.Device) {
	var req api.EventsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "", "invalid request body")
		return
	}
	if len(req.Events) > 500 {
		req.Events = req.Events[:500]
	}
	if err := s.db.AddEvents(r.Context(), device.UserID, device.ID, req.Events); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// assertSnapshotOwner checks that a snapshot belongs to the calling device.
func (s *Server) assertSnapshotOwner(r *http.Request, device *store.Device, snapshotID string) error {
	snap, err := s.db.SnapshotByID(r.Context(), device.UserID, snapshotID)
	if err != nil {
		return err
	}
	if snap.DeviceID != device.ID {
		return store.ErrNotFound
	}
	return nil
}

// checkQuota enforces the account storage limit, with a short cache so a backup
// of ten thousand chunks does not run ten thousand aggregate queries.
func (s *Server) checkQuota(r *http.Request, userID string) error {
	user, err := s.db.UserByID(r.Context(), userID)
	if err != nil {
		return err
	}
	quota := user.Policy.QuotaBytes
	if quota == 0 {
		quota = s.cfg.QuotaBytes
	}
	if quota <= 0 {
		return nil
	}

	s.usageMu.Lock()
	cached, ok := s.usageCache[userID]
	s.usageMu.Unlock()
	used := cached.bytes
	if !ok || time.Now().After(cached.expiresAt) {
		used, err = s.db.AccountStoredBytes(r.Context(), userID)
		if err != nil {
			return err
		}
		s.usageMu.Lock()
		s.usageCache[userID] = cachedUsage{bytes: used, expiresAt: time.Now().Add(usageCacheTTL)}
		s.usageMu.Unlock()
	}
	if used >= quota {
		return errors.New("storage quota reached; delete old snapshots or raise the quota")
	}
	return nil
}

// addUsage accounts newly stored bytes against the cached total.
func (s *Server) addUsage(userID string, n int64) {
	if n <= 0 {
		return
	}
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	if cached, ok := s.usageCache[userID]; ok {
		cached.bytes += n
		s.usageCache[userID] = cached
	}
}

func (s *Server) invalidateUsage(userID string) {
	s.usageMu.Lock()
	delete(s.usageCache, userID)
	s.usageMu.Unlock()
}
