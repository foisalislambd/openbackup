// Package ipc lets the CLI and the tray talk to a running agent.
//
// The transport is an HTTP server bound to the loopback interface on a random
// port, with a shared secret written to a file only the current user can read.
// A Unix socket would be the obvious choice, but Windows named pipes and Unix
// sockets need two different implementations and Windows support for the latter
// is patchy on older builds; loopback HTTP behaves identically everywhere, and
// the token stops another local user from driving the agent.
package ipc

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openbackup/openbackup/internal/idgen"
)

// controlFile holds the endpoint the CLI should connect to.
const controlFile = "control.json"

// control is the contents of the control file.
type control struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

// Handler is implemented by the engine.
type Handler interface {
	// Status returns the agent's current state as JSON-serialisable data.
	Status() any
	// BackupNow requests an immediate backup.
	BackupNow(reason string)
	// Pause stops work; a zero duration pauses indefinitely.
	Pause(d time.Duration)
	// Resume restarts work.
	Resume()
	// Reload re-reads the configuration file and applies it. It is how the
	// desktop app and the command line make a settings change take effect on the
	// running agent instead of at the next restart.
	Reload(ctx context.Context) error
}

// Server exposes a Handler on loopback.
type Server struct {
	handler Handler
	token   string
	path    string
	http    *http.Server
	ln      net.Listener
}

// Listen starts the control server and writes the control file into stateDir.
func Listen(stateDir string, handler Handler) (*Server, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	token, err := idgen.Secret(24)
	if err != nil {
		return nil, err
	}
	// Port 0 lets the OS pick a free port; binding to 127.0.0.1 keeps it off the
	// network entirely.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	s := &Server{
		handler: handler,
		token:   token,
		path:    filepath.Join(stateDir, controlFile),
		ln:      ln,
	}
	raw, err := json.Marshal(control{
		Port:  ln.Addr().(*net.TCPAddr).Port,
		Token: token,
		PID:   os.Getpid(),
	})
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	if err := os.WriteFile(s.path, raw, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", s.auth(s.handleStatus))
	mux.HandleFunc("POST /backup", s.auth(s.handleBackup))
	mux.HandleFunc("POST /pause", s.auth(s.handlePause))
	mux.HandleFunc("POST /resume", s.auth(s.handleResume))
	mux.HandleFunc("POST /reload", s.auth(s.handleReload))

	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = s.http.Serve(ln) }()
	return s, nil
}

// Close stops the server and removes the control file.
func (s *Server) Close() error {
	_ = os.Remove(s.path)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.http.Shutdown(ctx)
}

// auth checks the shared secret in constant time.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-OpenBackup-Control")
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1 {
			http.Error(w, "unauthorised", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.handler.Status())
}

func (s *Server) handleBackup(w http.ResponseWriter, _ *http.Request) {
	s.handler.BackupNow("requested from the command line")
	writeJSON(w, map[string]string{"status": "backup requested"})
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	var d time.Duration
	if raw := r.URL.Query().Get("for"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			http.Error(w, "invalid duration", http.StatusBadRequest)
			return
		}
		d = parsed
	}
	s.handler.Pause(d)
	writeJSON(w, map[string]string{"status": "paused"})
}

func (s *Server) handleResume(w http.ResponseWriter, _ *http.Request) {
	s.handler.Resume()
	writeJSON(w, map[string]string{"status": "resumed"})
}

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if err := s.handler.Reload(r.Context()); err != nil {
		// The message is written as the body so the caller can show the agent's
		// own explanation rather than "reload failed".
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]string{"status": "reloaded"})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// ErrNotRunning means no agent is listening.
var ErrNotRunning = errors.New("the OpenBackup agent does not appear to be running")

// Client talks to a running agent.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// Dial connects to the agent described by the control file in stateDir.
func Dial(stateDir string) (*Client, error) {
	raw, err := os.ReadFile(filepath.Join(stateDir, controlFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotRunning
	}
	if err != nil {
		return nil, err
	}
	var c control
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, ErrNotRunning
	}
	return &Client{
		base:  fmt.Sprintf("http://127.0.0.1:%d", c.Port),
		token: c.Token,
		http:  &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// Status fetches the agent's status into out.
func (c *Client) Status(ctx context.Context, out any) error {
	return c.call(ctx, http.MethodGet, "/status", out)
}

// BackupNow asks the agent to back up immediately.
func (c *Client) BackupNow(ctx context.Context) error {
	return c.call(ctx, http.MethodPost, "/backup", nil)
}

// Pause stops the agent, optionally for a fixed period.
func (c *Client) Pause(ctx context.Context, d time.Duration) error {
	path := "/pause"
	if d > 0 {
		path += "?for=" + d.String()
	}
	return c.call(ctx, http.MethodPost, path, nil)
}

// Resume restarts the agent.
func (c *Client) Resume(ctx context.Context) error {
	return c.call(ctx, http.MethodPost, "/resume", nil)
}

// Reload asks the agent to re-read its configuration.
func (c *Client) Reload(ctx context.Context) error {
	return c.call(ctx, http.MethodPost, "/reload", nil)
}

func (c *Client) call(ctx context.Context, method, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-OpenBackup-Control", c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		// A stale control file from a crashed agent looks exactly like this.
		return ErrNotRunning
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Carry the agent's explanation through, so a refused reload says why.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		if msg := strings.TrimSpace(string(body)); msg != "" {
			return errors.New(msg)
		}
		return fmt.Errorf("agent returned %s", resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
