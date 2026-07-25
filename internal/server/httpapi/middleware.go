package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/openbackup/openbackup/internal/api"
	"github.com/openbackup/openbackup/internal/server/auth"
	"github.com/openbackup/openbackup/internal/server/store"
)

// contextKey is the private key type for request-scoped values.
type contextKey string

const (
	ctxDevice contextKey = "device"
	ctxUser   contextKey = "user"
)

// sessionCookieName is the dashboard session cookie.
const sessionCookieName = "openbackup_session"

// agentOnly authenticates a device token.
func (s *Server) agentOnly(next func(http.ResponseWriter, *http.Request, *store.Device)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := auth.BearerToken(r.Header.Get(api.HeaderDeviceToken))
		if token == "" {
			writeError(w, http.StatusUnauthorized, api.CodeInvalidToken, "missing device token")
			return
		}
		device, err := s.db.DeviceByTokenHash(r.Context(), auth.HashToken(token))
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				// A revoked or deleted device gets a definitive answer so the
				// agent can stop retrying and tell the user to re-enrol.
				writeError(w, http.StatusUnauthorized, api.CodeInvalidToken, "device token is not valid")
				return
			}
			writeStoreError(w, err)
			return
		}
		ctx := context.WithValue(r.Context(), ctxDevice, device)
		next(w, r.WithContext(ctx), device)
	})
}

// userOnly authenticates a dashboard session.
func (s *Server) userOnly(next func(http.ResponseWriter, *http.Request, *store.User)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, api.CodeInvalidToken, "not signed in")
			return
		}
		user, err := s.db.SessionUser(r.Context(), auth.HashToken(cookie.Value))
		if err != nil {
			s.clearSessionCookie(w)
			writeError(w, http.StatusUnauthorized, api.CodeInvalidToken, "session expired")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUser, user)
		next(w, r.WithContext(ctx), user)
	})
}

// withBodyLimit caps request bodies. Chunk uploads get the larger chunk limit;
// everything else gets the JSON limit, so a malicious client cannot make the
// server buffer gigabytes of JSON.
func (s *Server) withBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := s.cfg.MaxBodyBytes
		if strings.HasPrefix(r.URL.Path, "/api/v1/agent/chunks/") {
			limit = s.cfg.MaxChunkBytes
		}
		if limit > 0 && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

// withSecurityHeaders applies conservative defaults.
func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// The dashboard is fully self-hosted with no third-party assets, so it
		// can run under a strict policy. 'unsafe-inline' covers the single style
		// block Vite emits for the initial paint.
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; "+
				"script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if s.cfg.SecureCookies {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the response status for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

// Flush forwards flushes so streaming archive downloads work through the
// recorder.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		level := "debug"
		if rec.status >= 500 {
			level = "error"
		} else if rec.status >= 400 {
			level = "warn"
		}
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", s.clientIP(r),
		}
		switch level {
		case "error":
			s.log.Error("request", attrs...)
		case "warn":
			s.log.Warn("request", attrs...)
		default:
			s.log.Debug("request", attrs...)
		}
	})
}

// withRecovery turns a panic into a 500 instead of killing the process. A single
// malformed request must never take a backup server down.
func (s *Server) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				// net/http uses this sentinel to abort a response deliberately;
				// it must keep propagating.
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}
				s.log.Error("panic serving request",
					"path", r.URL.Path, "panic", rec, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "", "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// clientIP resolves the caller address, honouring proxy headers only when the
// deployment says a proxy is in front. Trusting them unconditionally would let
// anyone spoof the address used for rate limiting.
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			if first, _, found := strings.Cut(v, ","); found {
				return strings.TrimSpace(first)
			}
			return strings.TrimSpace(v)
		}
		if v := r.Header.Get("X-Real-Ip"); v != "" {
			return strings.TrimSpace(v)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// attemptLimiter is a small fixed-window limiter for login attempts.
type attemptLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	limit    int
	window   time.Duration
}

func newAttemptLimiter(limit int, window time.Duration) *attemptLimiter {
	return &attemptLimiter{attempts: make(map[string][]time.Time), limit: limit, window: window}
}

// Allow records an attempt and reports whether it is within the limit.
func (l *attemptLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-l.window)
	kept := l.attempts[key][:0]
	for _, t := range l.attempts[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	// Opportunistically drop idle keys so the map cannot grow without bound.
	if len(kept) == 0 && len(l.attempts) > 10000 {
		l.attempts = make(map[string][]time.Time)
	}
	if len(kept) >= l.limit {
		l.attempts[key] = kept
		return false
	}
	l.attempts[key] = append(kept, now)
	return true
}

// Reset clears a key after a successful login.
func (l *attemptLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}
