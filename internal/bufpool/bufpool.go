// Package bufpool reuses byte slices across the backup pipeline.
//
// The pool is small on purpose. An idle agent may not garbage-collect for a
// long time, and an unbounded cache of multi-megabyte windows is what kept
// the process large after a backup finished. Large classes are not retained.
// Release drops the rest when a run ends.
package bufpool

import "sync"

var classes = [...]int{
	4 << 10,
	16 << 10,
	64 << 10,
	256 << 10,
	1 << 20,
	4 << 20,
	8 << 20,
	16 << 20,
}

// retained caps how many idle buffers each class may hold. Zero means the
// buffer is never cached.
var retained = [...]int{8, 4, 4, 2, 2, 1, 0, 0}

type bucket struct {
	mu    sync.Mutex
	bufs  [][]byte
	limit int
}

var pools [len(classes)]bucket

func init() {
	for i := range pools {
		pools[i].limit = retained[i]
	}
}

func classIndex(n int) int {
	if n < 0 {
		n = 0
	}
	for i, size := range classes {
		if n <= size {
			return i
		}
	}
	return -1
}

// Get returns a buffer of length n. Pair it with Put exactly once.
func Get(n int) []byte {
	if n < 0 {
		n = 0
	}
	idx := classIndex(n)
	if idx < 0 {
		return make([]byte, n)
	}
	b := &pools[idx]
	b.mu.Lock()
	if nbuf := len(b.bufs); nbuf > 0 {
		buf := b.bufs[nbuf-1]
		b.bufs[nbuf-1] = nil
		b.bufs = b.bufs[:nbuf-1]
		b.mu.Unlock()
		if cap(buf) < classes[idx] {
			buf = make([]byte, classes[idx])
		}
		return buf[:n]
	}
	b.mu.Unlock()
	return make([]byte, classes[idx])[:n]
}

// Put returns a buffer from Get. Putting the same slice twice aliases later
// Gets. Oversized or non-class buffers are dropped.
func Put(b []byte) {
	if b == nil {
		return
	}
	idx := classIndex(cap(b))
	if idx < 0 || cap(b) != classes[idx] || pools[idx].limit == 0 {
		return
	}
	p := &pools[idx]
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.bufs) >= p.limit {
		return
	}
	p.bufs = append(p.bufs, b[:cap(b)])
}

// Release drops every cached buffer.
func Release() {
	for i := range pools {
		p := &pools[i]
		p.mu.Lock()
		p.bufs = nil
		p.mu.Unlock()
	}
}
