// Package bufpool reuses byte slices across the backup pipeline.
//
// FastCDC, compression and chunk uploads used to allocate a fresh buffer for
// every file and every chunk. A small set of size classes keeps a few buffers
// of each size warm so a backup of thousands of tiny files does not allocate
// an 8 MiB window per file.
package bufpool

import "sync"

// classes are capacities, smallest to largest. A request is rounded up to the
// next class so a buffer can be reused for a later request of a similar size.
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

var pools [len(classes)]sync.Pool

func init() {
	for i, n := range classes {
		size := n
		pools[i].New = func() any {
			b := make([]byte, size)
			return &b
		}
	}
}

// classIndex returns the pool bucket that can hold n bytes, or -1 when n is
// larger than every class (the caller allocates directly).
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

// Get returns a buffer whose length and capacity are at least n.
// The contents are undefined. Pair every Get with Put.
func Get(n int) []byte {
	if n < 0 {
		n = 0
	}
	idx := classIndex(n)
	if idx < 0 {
		return make([]byte, n)
	}
	b := pools[idx].Get().(*[]byte)
	buf := *b
	if cap(buf) < classes[idx] {
		buf = make([]byte, classes[idx])
	}
	return buf[:n]
}

// Put returns a buffer acquired from Get. Buffers that did not come from a
// class, or that have been grown past their class, are left for the garbage
// collector.
func Put(b []byte) {
	if b == nil {
		return
	}
	idx := classIndex(cap(b))
	if idx < 0 || cap(b) != classes[idx] {
		return
	}
	// Drop the caller's view of the bytes so a retained slice cannot pin the
	// buffer, then hand the storage back. The pointer indirection is what
	// sync.Pool requires to avoid allocating on Put.
	full := b[:cap(b)]
	pools[idx].Put(&full)
}
