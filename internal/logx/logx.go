// Package logx provides the small structured logging setup shared by the
// agent and the server. It intentionally wraps log/slog instead of pulling a
// third party logger so the agent binary stays small.
package logx

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Options configures a logger.
type Options struct {
	// Level is one of debug, info, warn, error.
	Level string
	// JSON emits machine readable logs (used by the server / containers).
	JSON bool
	// File, when set, mirrors logs into a size-capped rotating file.
	File string
	// MaxFileBytes is the rotation threshold. Defaults to 4 MiB.
	MaxFileBytes int64
}

// New builds a logger according to opts.
func New(opts Options) *slog.Logger {
	var writers []io.Writer
	writers = append(writers, os.Stderr)

	if opts.File != "" {
		if opts.MaxFileBytes <= 0 {
			opts.MaxFileBytes = 4 << 20
		}
		if w, err := newRotator(opts.File, opts.MaxFileBytes); err == nil {
			writers = append(writers, w)
		}
	}

	out := io.Writer(os.Stderr)
	if len(writers) > 1 {
		out = io.MultiWriter(writers...)
	}

	handlerOpts := &slog.HandlerOptions{Level: parseLevel(opts.Level)}
	var h slog.Handler
	if opts.JSON {
		h = slog.NewJSONHandler(out, handlerOpts)
	} else {
		h = slog.NewTextHandler(out, handlerOpts)
	}
	return slog.New(h)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// rotator is a minimal single-file rotating writer: when the file grows past
// max it is renamed to "<name>.1" and a fresh file is started. One backup is
// kept, which is plenty for a background agent.
type rotator struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	size int64
}

func newRotator(path string, max int64) (*rotator, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	r := &rotator{path: path, max: max}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotator) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	r.f = f
	r.size = st.Size()
	return nil
}

func (r *rotator) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return len(p), nil
	}
	if r.size+int64(len(p)) > r.max {
		_ = r.f.Close()
		_ = os.Remove(r.path + ".1")
		_ = os.Rename(r.path, r.path+".1")
		if err := r.open(); err != nil {
			r.f = nil
			return len(p), nil
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}
