package httpapi

import (
	"errors"
	"io"
	"net/http"

	"github.com/foisalislambd/openbackup/internal/bufpool"
	"golang.org/x/sync/semaphore"
)

func (s *Server) uploadBudgetWeight() int64 {
	if s.cfg.UploadBudgetBytes > 0 {
		return s.cfg.UploadBudgetBytes
	}
	return 64 << 20
}

// admitUpload reserves in-flight upload bytes. A request larger than the
// budget is rejected by the semaphore (Acquire fails when n exceeds the
// weight), so callers pass a size that fits.
func (s *Server) admitUpload(r *http.Request, limit int64) (func(), error) {
	if s.uploadBudget == nil {
		s.uploadBudget = semaphore.NewWeighted(s.uploadBudgetWeight())
	}
	n := r.ContentLength
	if n <= 0 || n > limit {
		n = limit
	}
	budget := s.uploadBudgetWeight()
	if n > budget {
		n = budget
	}
	if n <= 0 {
		n = 1
	}
	if err := s.uploadBudget.Acquire(r.Context(), n); err != nil {
		return nil, err
	}
	return func() { s.uploadBudget.Release(n) }, nil
}

// readUpload reads a request body. cleanup returns a pooled buffer, if one was
// used, and must run only after the caller has finished with the slice.
func readUpload(r io.Reader, contentLength, limit int64) (data []byte, cleanup func(), err error) {
	cleanup = func() {}
	if limit <= 0 {
		limit = 16 << 20
	}
	if contentLength > limit {
		return nil, cleanup, errors.New("body too large")
	}
	if contentLength > 0 {
		buf := bufpool.Get(int(contentLength))
		buf = buf[:contentLength]
		if _, err := io.ReadFull(r, buf); err != nil {
			bufpool.Put(buf[:0])
			return nil, cleanup, err
		}
		cleanup = func() { bufpool.Put(buf[:0]) }
		return buf, cleanup, nil
	}
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, cleanup, err
	}
	if int64(len(body)) > limit {
		return nil, cleanup, errors.New("body too large")
	}
	return body, cleanup, nil
}
