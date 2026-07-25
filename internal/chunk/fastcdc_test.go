package chunk

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"testing"
)

func chunkAll(t *testing.T, data []byte, cfg Config) [][]byte {
	t.Helper()
	c, err := NewChunker(bytes.NewReader(data), cfg)
	if err != nil {
		t.Fatalf("NewChunker: %v", err)
	}
	var out [][]byte
	for {
		ch, err := c.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, bytes.Clone(ch.Data))
	}
	return out
}

func testConfig() Config {
	// Small sizes keep the tests fast while exercising the same code paths.
	return Config{Min: 512, Avg: 2048, Max: 8192}
}

func TestChunkerRoundTrip(t *testing.T) {
	data := make([]byte, 5<<20)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	chunks := chunkAll(t, data, cfg)

	var joined []byte
	for _, c := range chunks {
		if len(c) > cfg.Max {
			t.Fatalf("chunk of %d bytes exceeds max %d", len(c), cfg.Max)
		}
		joined = append(joined, c...)
	}
	if !bytes.Equal(joined, data) {
		t.Fatal("reassembled stream differs from input")
	}
	if len(chunks) < 100 {
		t.Fatalf("expected many chunks for 5 MiB at 2 KiB average, got %d", len(chunks))
	}
}

func TestChunkerIsDeterministic(t *testing.T) {
	data := make([]byte, 1<<20)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	a := chunkAll(t, data, testConfig())
	b := chunkAll(t, data, testConfig())
	if len(a) != len(b) {
		t.Fatalf("chunk counts differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			t.Fatalf("chunk %d differs between runs", i)
		}
	}
}

// TestInsertionShiftsFewChunks is the property the whole incremental upload
// design depends on: a small edit must only invalidate nearby chunks.
func TestInsertionShiftsFewChunks(t *testing.T) {
	original := make([]byte, 4<<20)
	if _, err := rand.Read(original); err != nil {
		t.Fatal(err)
	}
	modified := make([]byte, 0, len(original)+16)
	modified = append(modified, original[:1<<20]...)
	modified = append(modified, []byte("OPENBACKUP-INSERT")...)
	modified = append(modified, original[1<<20:]...)

	cfg := testConfig()
	before := chunkAll(t, original, cfg)
	after := chunkAll(t, modified, cfg)

	seen := make(map[string]struct{}, len(before))
	for _, c := range before {
		seen[string(c)] = struct{}{}
	}
	var novel int
	for _, c := range after {
		if _, ok := seen[string(c)]; !ok {
			novel++
		}
	}
	// Fixed-size blocking would make every chunk after the insertion novel.
	if novel > len(after)/10 {
		t.Fatalf("insertion invalidated %d of %d chunks; content-defined chunking should localise the change", novel, len(after))
	}
}

func TestChunkerSmallInput(t *testing.T) {
	data := []byte("hello openbackup")
	chunks := chunkAll(t, data, testConfig())
	if len(chunks) != 1 || !bytes.Equal(chunks[0], data) {
		t.Fatalf("expected one chunk equal to the input, got %d chunks", len(chunks))
	}
}

func TestChunkerEmptyInput(t *testing.T) {
	if chunks := chunkAll(t, nil, testConfig()); len(chunks) != 0 {
		t.Fatalf("expected no chunks, got %d", len(chunks))
	}
}

func TestSplitMatchesChunker(t *testing.T) {
	data := make([]byte, 1<<20)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	want := chunkAll(t, data, cfg)

	var got [][]byte
	if err := Split(data, cfg, func(c Chunk) error {
		got = append(got, bytes.Clone(c.Data))
		return nil
	}); err != nil {
		t.Fatalf("Split: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Split produced %d chunks, chunker produced %d", len(got), len(want))
	}
	for i := range got {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("chunk %d differs", i)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	if _, err := NewChunker(bytes.NewReader(nil), Config{Min: 100, Avg: 50, Max: 10}); err == nil {
		t.Fatal("expected error for min > avg > max")
	}
}

func BenchmarkChunker(b *testing.B) {
	data := make([]byte, 32<<20)
	if _, err := rand.Read(data); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c, err := NewChunker(bytes.NewReader(data), DefaultConfig())
		if err != nil {
			b.Fatal(err)
		}
		for {
			if _, err := c.Next(); err != nil {
				break
			}
		}
	}
}
