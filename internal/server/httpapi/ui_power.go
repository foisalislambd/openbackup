package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/foisalislambd/openbackup/internal/server/auth"
	"github.com/foisalislambd/openbackup/internal/server/maintenance"
	"github.com/foisalislambd/openbackup/internal/server/store"
)

func (s *Server) handleSearchSnapshot(w http.ResponseWriter, r *http.Request, user *store.User) {
	snapshotID := r.PathValue("id")
	if _, err := s.db.SnapshotByID(r.Context(), user.ID, snapshotID); err != nil {
		writeStoreError(w, err)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		writeError(w, http.StatusBadRequest, "", "enter at least 2 characters")
		return
	}
	if len(q) > 80 {
		q = q[:80]
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	entries, next, err := s.db.Tree(r.Context(), snapshotID, store.TreeQuery{
		Query:  q,
		Cursor: r.URL.Query().Get("cursor"),
		Limit:  limit,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries":     entries,
		"next_cursor": next,
		"query":       q,
	})
}

func (s *Server) handlePinSnapshot(w http.ResponseWriter, r *http.Request, user *store.User) {
	var req struct {
		Pinned *bool `json:"pinned"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Pinned == nil {
		writeError(w, http.StatusBadRequest, "", "pinned is required")
		return
	}
	if err := s.db.SetSnapshotPinned(r.Context(), user.ID, r.PathValue("id"), *req.Pinned); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListAlerts(w http.ResponseWriter, r *http.Request, user *store.User) {
	if err := s.db.ReconcileDeviceAlerts(r.Context(), user.ID); err != nil {
		writeStoreError(w, err)
		return
	}
	alerts, err := s.db.ListOpenAlerts(r.Context(), user.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(alerts))
	for _, a := range alerts {
		out = append(out, map[string]any{
			"id":          a.ID,
			"device_id":   a.DeviceID,
			"device_name": a.DeviceName,
			"kind":        a.Kind,
			"level":       a.Level,
			"message":     a.Message,
			"opened_at":   a.OpenedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": out})
}

func (s *Server) handleAckAlert(w http.ResponseWriter, r *http.Request, user *store.User) {
	if err := s.db.AckAlert(r.Context(), user.ID, r.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request, user *store.User) {
	current := ""
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		current = auth.HashToken(cookie.Value)
	}
	sessions, err := s.db.ListSessions(r.Context(), user.ID, current)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, map[string]any{
			"id":         sess.ID,
			"created_at": sess.CreatedAt,
			"expires_at": sess.ExpiresAt,
			"user_agent": sess.UserAgent,
			"current":    sess.Current,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request, user *store.User) {
	current := ""
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		current = auth.HashToken(cookie.Value)
	}
	if err := s.db.DeleteSessionByID(r.Context(), user.ID, r.PathValue("id"), current); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleIntegrity(w http.ResponseWriter, r *http.Request, _ *store.User) {
	run, err := s.db.LatestIntegrityRun(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, integrityPayload(run))
}

func (s *Server) handleRunIntegrity(w http.ResponseWriter, r *http.Request, _ *store.User) {
	// A dashboard click checks a bounded sample so it returns while the owner
	// is still looking at the page. The hourly maintenance pass keeps going.
	run, err := maintenance.New(s.db, s.blobs, s.log).VerifySample(r.Context(), 64)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, integrityPayload(run))
}

func integrityPayload(run store.IntegrityRun) map[string]any {
	payload := map[string]any{
		"checked": run.Checked,
		"failed":  run.Failed,
		"detail":  run.Detail,
	}
	if !run.FinishedAt.IsZero() {
		payload["finished_at"] = run.FinishedAt
	}
	if !run.StartedAt.IsZero() {
		payload["started_at"] = run.StartedAt
	}
	return payload
}
