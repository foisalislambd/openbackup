package uploader

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/foisalislambd/openbackup/internal/api"
	"github.com/foisalislambd/openbackup/internal/bufpool"
	"github.com/foisalislambd/openbackup/internal/codec"
)

func TestFailedFlushReturnsEachBufferOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/chunks/missing":
			var req api.MissingChunksRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(api.MissingChunksResponse{Missing: req.Digests})
		default:
			http.Error(w, "flush failed", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	client, err := api.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	client.MaxRetries = 1
	client.Backoff = 0
	cdc, err := codec.New(codec.Options{})
	if err != nil {
		t.Fatal(err)
	}
	up, err := New(Options{Client: client, Codec: cdc, Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	bufpool.Release()
	dir := t.TempDir()
	// One more file than a batch, so the next file flushes the full batch and
	// that flush fails. Each pooled copy must come back exactly once.
	for i := 0; i < 33; i++ {
		body := []byte{'a' + byte(i%26), byte(i), byte(i >> 8), 'x'}
		path := filepath.Join(dir, "f"+string(rune('a'+i%26))+string(rune(i)))
		// Unique path without depending on rune range.
		path = filepath.Join(dir, filepath.Base(t.Name())+"-"+itoa(i))
		if err := os.WriteFile(path, append(body, byte(i)), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := up.UploadFile(context.Background(), path, int64(len(body)+1))
		if i < 32 && err != nil {
			t.Fatalf("file %d: %v", i, err)
		}
		if i == 32 && err == nil {
			t.Fatal("expected the flush of the full batch to fail")
		}
	}

	held := make([][]byte, 0, 8)
	seen := map[uintptr]int{}
	for i := 0; i < 8; i++ {
		buf := bufpool.Get(64)
		ptr := uintptr(unsafe.Pointer(unsafe.SliceData(buf)))
		if prev, ok := seen[ptr]; ok {
			t.Fatalf("buffer %d aliases buffer %d", i, prev)
		}
		seen[ptr] = i
		for j := range buf {
			buf[j] = byte(i + 1)
		}
		held = append(held, buf)
	}
	for i, buf := range held {
		if buf[0] != byte(i+1) {
			t.Fatalf("buffer %d changed after writing the others", i)
		}
		bufpool.Put(buf[:0])
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
