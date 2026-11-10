package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/foisalislambd/openbackup/internal/bufpool"
	"golang.org/x/sync/semaphore"
)

// errUploadTooLarge is returned when the wire bytes plus the declared
// plaintext cannot fit in the in-flight budget. It is not a busy signal:
// retrying the same request will not succeed until the budget is raised.
var errUploadTooLarge = errors.New("httpapi: upload exceeds the in-flight budget")

func (s *Server) uploadBudgetWeight() int64 {
	if s.cfg.UploadBudgetBytes > 0 {
		return s.cfg.UploadBudgetBytes
	}
	return 64 << 20
}

// admitUpload reserves in-flight upload bytes from the Content-Length. The
// returned held count is what Release will free. A request larger than the
// budget is capped at the budget so Acquire does not reject n greater than
// the semaphore weight; widenAdmission then rejects a plaintext total that
// still does not fit.
func (s *Server) admitUpload(r *http.Request, limit int64) (func(), int64, error) {
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
		return nil, 0, err
	}
	return func() { s.uploadBudget.Release(n) }, n, nil
}

// widenAdmission raises a reservation to cover max(body, declared plaintext).
// The previous release is replaced by one that frees the whole reservation.
// Holding the full budget already (an unknown Content-Length) does not try to
// acquire more, which would deadlock on this goroutine's own reservation.
func (s *Server) widenAdmission(ctx context.Context, release func(), held, bodyLen, plainSum int64) (func(), int64, error) {
	want := bodyLen
	if plainSum > want {
		want = plainSum
	}
	if want < 1 {
		want = 1
	}
	if want <= held {
		return release, held, nil
	}
	budget := s.uploadBudgetWeight()
	if want > budget {
		return release, held, errUploadTooLarge
	}
	extra := want - held
	if err := s.uploadBudget.Acquire(ctx, extra); err != nil {
		return release, held, err
	}
	return func() { s.uploadBudget.Release(held + extra) }, held + extra, nil
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
