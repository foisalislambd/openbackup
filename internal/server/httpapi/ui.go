package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/foisalislambd/openbackup/internal/api"
	"github.com/foisalislambd/openbackup/internal/idgen"
	"github.com/foisalislambd/openbackup/internal/ignore"
	"github.com/foisalislambd/openbackup/internal/server/auth"
	"github.com/foisalislambd/openbackup/internal/server/store"
	"github.com/foisalislambd/openbackup/internal/version"
)

// handleBootstrap tells the dashboard what to render before anyone signs in:
// the first-run setup screen or the login form.
func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	count, err := s.db.CountUsers(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// The dashboard is a static SPA with no server-rendered redirect, so this
	// one unauthenticated call is what tells it whether to show the first-run
	// form, the sign-in form, or the dashboard itself.
	_, sessionErr := s.currentUser(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"needs_setup":   count == 0,
		"authenticated": sessionErr == nil,
		"allow_signup":  s.cfg.AllowSignup && count == 0,
		"public_url":    s.cfg.PublicURL,
		"version":       version.Version,
		"protocol":      api.Version,
	})
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// decoyPasswordHash is verified against when the email is unknown, so the
// endpoint spends the same Argon2id time either way and response timing cannot
// be used to enumerate which addresses have an account.
var decoyPasswordHash, _ = auth.HashPassword("openbackup timing decoy")

// handleSetup creates the first administrator. It only works while no account
// exists, so an exposed server cannot be taken over later.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var req credentials
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "", "invalid request body")
		return
	}
	if !s.loginLimiter.Allow("setup:" + s.clientIP(r)) {
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, api.CodeRateLimited, "too many setup attempts, try again later")
		return
	}
	if !strings.Contains(req.Email, "@") {
		writeError(w, http.StatusBadRequest, "", "a valid email address is required")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	// The conditional insert makes "no users yet" part of the write itself: two
	// concurrent setup requests cannot both win, so a fresh internet-facing
	// server cannot be raced into having a second, attacker-chosen admin.
	user, err := s.db.CreateFirstUser(r.Context(), req.Email, hash)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "", "this server is already set up")
			return
		}
		writeStoreError(w, err)
		return
	}
	s.log.Info("first account created", "user", user.ID, "email", user.Email)
	if err := s.startSession(w, r, user); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": publicUser(user)})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req credentials
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "", "invalid request body")
		return
	}
	key := "login:" + s.clientIP(r)
	if !s.loginLimiter.Allow(key) {
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, api.CodeRateLimited, "too many sign-in attempts, try again later")
		return
	}
	user, err := s.db.UserByEmail(r.Context(), req.Email)
	if err != nil {
		// Identical response for unknown email and wrong password, and the same
		// Argon2id work either way, so the endpoint can neither enumerate
		// accounts by body nor by timing.
		_ = auth.VerifyPassword(req.Password, decoyPasswordHash)
		writeError(w, http.StatusUnauthorized, "", "email or password is incorrect")
		return
	}
	if err := auth.VerifyPassword(req.Password, user.PasswordHash); err != nil {
		writeError(w, http.StatusUnauthorized, "", "email or password is incorrect")
		return
	}
	s.loginLimiter.Reset(key)
	if err := s.startSession(w, r, user); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": publicUser(user)})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		if err := s.db.DeleteSession(r.Context(), auth.HashToken(cookie.Value)); err != nil {
			// The browser drops the cookie either way; the row is cleaned up by
			// the maintenance pass if this failed.
			s.log.Warn("delete session on logout", "error", err)
		}
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// startSession creates a session row and sets the cookie. It returns an error
// instead of writing a response on failure, so callers can emit exactly one
// response; call writeStoreError at the call site when it fails.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user *store.User) error {
	token, err := idgen.Secret(32)
	if err != nil {
		return err
	}
	if err := s.db.CreateSession(r.Context(), user.ID, auth.HashToken(token),
		r.UserAgent(), s.cfg.SessionTTL); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies,
		// Lax rather than Strict: the dashboard is often reached by clicking a
		// link from the agent's tray menu, and Strict would drop the session on
		// that first navigation.
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(s.cfg.SessionTTL),
	})
	return nil
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func publicUser(u *store.User) map[string]any {
	return map[string]any{
		"id":             u.ID,
		"email":          u.Email,
		"is_admin":       u.IsAdmin,
		"retention_days": u.Policy.RetentionDays,
		"quota_bytes":    u.Policy.QuotaBytes,
		"created_at":     u.CreatedAt,
	}
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, user *store.User) {
	writeJSON(w, http.StatusOK, map[string]any{
		"user":    publicUser(user),
		"version": version.Version,
	})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request, user *store.User) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "", "invalid request body")
		return
	}
	if !s.loginLimiter.Allow("password:" + user.ID) {
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, api.CodeRateLimited, "too many password attempts, try again later")
		return
	}
	if err := auth.VerifyPassword(req.Current, user.PasswordHash); err != nil {
		writeError(w, http.StatusUnauthorized, "", "current password is incorrect")
		return
	}
	s.loginLimiter.Reset("password:" + user.ID)
	hash, err := auth.HashPassword(req.New)
	if err != nil {
		writeError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	if err := s.db.UpdateUserPassword(r.Context(), user.ID, hash); err != nil {
		writeStoreError(w, err)
		return
	}
	// Rotating a password must also kill any sessions an attacker may have
	// copied; otherwise the old cookies stay valid for the full session TTL.
	// The caller's own session is kept so they are not logged out mid-action.
	keepHash := ""
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		keepHash = auth.HashToken(cookie.Value)
	}
	if n, err := s.db.DeleteUserSessions(r.Context(), user.ID, keepHash); err != nil {
		s.log.Warn("revoke sessions after password change", "user", user.ID, "error", err)
	} else if n > 0 {
		s.log.Info("revoked sessions after password change", "user", user.ID, "sessions", n)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request, user *store.User) {
	devices, err := s.db.ListDevices(r.Context(), user.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

func (s *Server) handleUpdateDevice(w http.ResponseWriter, r *http.Request, user *store.User) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "", "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "", "name is required")
		return
	}
	if err := s.db.RenameDevice(r.Context(), user.ID, r.PathValue("id"), strings.TrimSpace(req.Name)); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteDevice revokes or deletes a device.
//
// The default is revoke, which keeps the backups restorable and only stops the
// device from writing. Deleting the data is a separate, explicit choice.
func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request, user *store.User) {
	deviceID := r.PathValue("id")
	purge := r.URL.Query().Get("purge") == "true"
	var err error
	if purge {
		err = s.db.DeleteDevice(r.Context(), user.ID, deviceID)
	} else {
		err = s.db.RevokeDevice(r.Context(), user.ID, deviceID)
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Tell the agent to forget its credentials next time it calls in.
	s.queueCommand(deviceID, api.Command{ID: idgen.New(), Kind: api.CommandForget})
	s.invalidateUsage(user.ID)
	s.log.Info("device removed", "device", deviceID, "user", user.ID, "purge", purge)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleQueueCommand(w http.ResponseWriter, r *http.Request, user *store.User) {
	deviceID := r.PathValue("id")
	device, err := s.db.DeviceByID(r.Context(), deviceID)
	if err != nil || device.UserID != user.ID {
		writeError(w, http.StatusNotFound, api.CodeNotFound, "device not found")
		return
	}
	var req struct {
		Kind api.CommandKind   `json:"kind"`
		Args map[string]string `json:"args,omitempty"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "", "invalid request body")
		return
	}
	switch req.Kind {
	case api.CommandRescan, api.CommandPause, api.CommandResume, api.CommandBackupNow, api.CommandReloadConf:
	default:
		writeError(w, http.StatusBadRequest, "", "unsupported command")
		return
	}
	cmd := api.Command{ID: idgen.New(), Kind: req.Kind, Args: req.Args}
	s.queueCommand(deviceID, cmd)
	writeJSON(w, http.StatusAccepted, map[string]any{"command": cmd})
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request, user *store.User) {
	stats, err := s.db.Usage(r.Context(), user.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if stats.QuotaBytes == 0 {
		stats.QuotaBytes = s.cfg.QuotaBytes
	}
	stats.FreeDiskBytes = s.blobs.FreeBytes()
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request, user *store.User) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	history, err := s.db.UploadHistory(r.Context(), user.ID, days)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": history})
}

func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request, user *store.User) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	snapshots, err := s.db.ListSnapshots(r.Context(), user.ID, r.URL.Query().Get("device_id"), limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snapshots})
}

func (s *Server) handleGetSnapshot(w http.ResponseWriter, r *http.Request, user *store.User) {
	snap, err := s.db.SnapshotByID(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) handleDeleteSnapshot(w http.ResponseWriter, r *http.Request, user *store.User) {
	removed, err := s.db.DeleteSnapshot(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.invalidateUsage(user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"deleted_snapshots": removed})
}

// handleBrowse lists a snapshot directory level, which is what the restore file
// picker walks.
func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request, user *store.User) {
	snapshotID := r.PathValue("id")
	snap, err := s.db.SnapshotByID(r.Context(), user.ID, snapshotID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	entries, next, err := s.db.Tree(r.Context(), snapshotID, treeQuery(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries":     entries,
		"next_cursor": next,
		"encrypted":   snap.Encrypted,
	})
}

// handleFileVersions lists distinct versions of a path across completed backups.
func (s *Server) handleFileVersions(w http.ResponseWriter, r *http.Request, user *store.User) {
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		writeError(w, http.StatusBadRequest, "", "path is required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	versions, err := s.db.FileVersions(r.Context(), user.ID, path, r.URL.Query().Get("device_id"), limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "versions": versions})
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request, user *store.User) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := s.db.ListEvents(r.Context(), user.ID,
		r.URL.Query().Get("device_id"), r.URL.Query().Get("level"), limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) handleListJoinTokens(w http.ResponseWriter, r *http.Request, user *store.User) {
	tokens, err := s.db.ListJoinTokens(r.Context(), user.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Only metadata is returned: the code itself is unrecoverable by design, so
	// a stolen database yields no working enrolment codes.
	out := make([]map[string]any, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, map[string]any{
			"label":      t.Label,
			"created_at": t.CreatedAt,
			"expires_at": t.ExpiresAt,
			"used_at":    t.UsedAt,
			"device_id":  t.DeviceID,
			"used":       t.UsedAt != nil,
			"expired":    time.Now().After(t.ExpiresAt),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

// handleCreateJoinToken issues a one-time enrolment code and returns the exact
// command to run on the new device. That command string is the entire
// "zero-configuration" promise: copy, paste, done.
func (s *Server) handleCreateJoinToken(w http.ResponseWriter, r *http.Request, user *store.User) {
	var req struct {
		Label string `json:"label"`
	}
	// A body is optional here.
	_ = decodeJSON(r, &req)

	code, err := idgen.JoinCode()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	token, err := s.db.CreateJoinToken(r.Context(), user.ID, auth.HashToken(idgen.NormalizeJoinCode(code)),
		req.Label, s.cfg.JoinTokenTTL)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	serverURL := s.cfg.PublicURL
	if serverURL == "" {
		// Derive the scheme from the actual connection instead of assuming TLS:
		// a plain-HTTP install must not print enrolment commands pointing at an
		// https:// URL that does not exist.
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		serverURL = scheme + "://" + r.Host
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"code":       code,
		"expires_at": token.ExpiresAt,
		"server_url": serverURL,
		"commands": map[string]string{
			"windows": "openbackup connect --server " + serverURL + " --code " + code,
			"unix":    "openbackup connect --server " + serverURL + " --code " + code,
		},
	})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request, user *store.User) {
	writeJSON(w, http.StatusOK, settingsPayload(user.Policy, s.cfg.PublicURL, s.cfg.GCInterval.String()))
}

// handleUpdateSettings applies a partial update: the dashboard saves one field at
// a time as it is changed, so anything absent from the body must be left alone.
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request, user *store.User) {
	var req struct {
		RetentionDays        *int   `json:"retention_days"`
		QuotaBytes           *int64 `json:"quota_bytes"`
		MaxUploadBytesPerSec *int64 `json:"max_upload_bytes_per_sec"`
		RequireEncryption    *bool  `json:"require_encryption"`
		KeepDaily            *int   `json:"keep_daily"`
		KeepWeekly           *int   `json:"keep_weekly"`
		KeepMonthly          *int   `json:"keep_monthly"`
		AlertAfterHours      *int   `json:"alert_after_hours"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "", "invalid request body")
		return
	}
	policy := user.Policy
	if req.RetentionDays != nil {
		policy.RetentionDays = *req.RetentionDays
	}
	if req.QuotaBytes != nil {
		policy.QuotaBytes = *req.QuotaBytes
	}
	if req.MaxUploadBytesPerSec != nil {
		policy.MaxUploadBytesPerSec = *req.MaxUploadBytesPerSec
	}
	if req.RequireEncryption != nil {
		policy.RequireEncryption = *req.RequireEncryption
	}
	if req.KeepDaily != nil {
		policy.KeepDaily = *req.KeepDaily
	}
	if req.KeepWeekly != nil {
		policy.KeepWeekly = *req.KeepWeekly
	}
	if req.KeepMonthly != nil {
		policy.KeepMonthly = *req.KeepMonthly
	}
	if req.AlertAfterHours != nil {
		policy.AlertAfterHours = *req.AlertAfterHours
	}

	if policy.RetentionDays < 0 || policy.RetentionDays > 3650 {
		writeError(w, http.StatusBadRequest, "", "retention must be between 0 (keep forever) and 3650 days")
		return
	}
	if policy.KeepDaily < 0 || policy.KeepDaily > 3650 || policy.KeepWeekly < 0 || policy.KeepWeekly > 3650 ||
		policy.KeepMonthly < 0 || policy.KeepMonthly > 3650 {
		writeError(w, http.StatusBadRequest, "", "history counts must be between 0 and 3650")
		return
	}
	if policy.AlertAfterHours < 0 || policy.AlertAfterHours > 24*365 {
		writeError(w, http.StatusBadRequest, "", "alert window must be between 0 (off) and 8760 hours")
		return
	}
	if policy.QuotaBytes < 0 {
		writeError(w, http.StatusBadRequest, "", "quota must not be negative")
		return
	}
	if policy.MaxUploadBytesPerSec < 0 {
		writeError(w, http.StatusBadRequest, "", "upload limit must not be negative")
		return
	}
	// Turning the requirement on later would silently orphan whatever is already
	// stored in plaintext, so say so instead of pretending it applies backwards.
	if req.RequireEncryption != nil && *req.RequireEncryption && !user.Policy.RequireEncryption {
		stored, err := s.db.HasSnapshots(r.Context(), user.ID)
		if err == nil && stored {
			writeError(w, http.StatusConflict, "",
				"backups already exist on this account; encryption can only be required before the first backup")
			return
		}
	}

	if err := s.db.UpdateUserPolicy(r.Context(), user.ID, policy); err != nil {
		writeStoreError(w, err)
		return
	}
	// Agents pick the new policy up on their next heartbeat, which is within a
	// minute, so there is nothing for the user to do on each machine.
	writeJSON(w, http.StatusOK, settingsPayload(policy, s.cfg.PublicURL, s.cfg.GCInterval.String()))
}

func settingsPayload(p store.UserPolicy, publicURL, gcInterval string) map[string]any {
	return map[string]any{
		"retention_days":           p.RetentionDays,
		"quota_bytes":              p.QuotaBytes,
		"max_upload_bytes_per_sec": p.MaxUploadBytesPerSec,
		"require_encryption":       p.RequireEncryption,
		"keep_daily":               p.KeepDaily,
		"keep_weekly":              p.KeepWeekly,
		"keep_monthly":             p.KeepMonthly,
		"alert_after_hours":        p.AlertAfterHours,
		"public_url":               publicURL,
		"gc_interval":              gcInterval,
	}
}

// handleIgnoreRules publishes the default exclusion rules with their reasons.
//
// Being able to see exactly what a backup tool skips, and why, is the difference
// between trusting it and hoping. This endpoint needs no authentication because
// the rules are the same for everyone and contain nothing private.
func (s *Server) handleIgnoreRules(w http.ResponseWriter, r *http.Request) {
	ruleSet := ignore.DefaultRuleSet()
	out := make(map[string][]map[string]string, len(ruleSet))
	for category, rules := range ruleSet {
		items := make([]map[string]string, 0, len(rules))
		for _, rule := range rules {
			items = append(items, map[string]string{"pattern": rule.Pattern, "reason": rule.Reason})
		}
		out[string(category)] = items
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"categories":      out,
		"project_markers": ignore.MarkerNames(),
		"max_file_size":   ignore.DefaultMaxFileSize,
	})
}
