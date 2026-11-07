package chunk

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestSmallBufferMatchesFullWindow(t *testing.T) {
	data := bytes.Repeat([]byte("the quick brown fox "), 80) // 1600 bytes, under Max
	cfg := testConfig()
	full := chunkAll(t, data, cfg)

	c, err := NewChunkerSize(bytes.NewReader(data), cfg, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Release()
	var small [][]byte
	for {
		ch, err := c.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		small = append(small, bytes.Clone(ch.Data))
	}
	if len(small) != len(full) {
		t.Fatalf("chunk count %d, full window %d", len(small), len(full))
	}
	for i := range full {
		if !bytes.Equal(small[i], full[i]) {
			t.Fatalf("chunk %d differs", i)
		}
	}
	if BufferSize(cfg, int64(len(data))) >= cfg.Max*2 {
		t.Fatalf("a file under Max should not take the full window, got %d", BufferSize(cfg, int64(len(data))))
	}
}
