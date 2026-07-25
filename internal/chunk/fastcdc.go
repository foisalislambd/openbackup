// Package chunk implements content-defined chunking (FastCDC).
//
// Content-defined chunking is what makes incremental backup cheap: inserting a
// byte in the middle of a 4 GiB video shifts only the chunk it lands in, so the
// agent re-uploads a megabyte instead of the whole file. Fixed-size blocks
// would re-upload everything after the insertion point.
//
// The cut-point predicate follows the FastCDC paper (Xia et al., 2016): a
// rolling "gear" hash over a sliding window, with normalised chunking that
// applies a stricter mask before the target average size and a looser mask
// after it. That keeps the chunk size distribution tight around the average,
// which in turn keeps the chunk index small.
package chunk

import (
	"errors"
	"fmt"
	"io"
	"math/bits"
)

// Default chunk size parameters. The 1 MiB average is a deliberate trade-off:
// smaller chunks deduplicate better but multiply index rows and HTTP requests,
// and the agent must stay under a 100 MiB RAM budget.
const (
	DefaultMin = 256 << 10 // 256 KiB
	DefaultAvg = 1 << 20   // 1 MiB
	DefaultMax = 4 << 20   // 4 MiB

	// Normalization level from the paper. Level 2 is the recommended default.
	normalization = 2
)

// Config controls chunk boundaries.
type Config struct {
	Min int
	Avg int
	Max int
}

// DefaultConfig returns the tuned production defaults.
func DefaultConfig() Config {
	return Config{Min: DefaultMin, Avg: DefaultAvg, Max: DefaultMax}
}

func (c *Config) normalize() error {
	if c.Min == 0 && c.Avg == 0 && c.Max == 0 {
		*c = DefaultConfig()
	}
	if c.Min <= 0 || c.Avg <= 0 || c.Max <= 0 {
		return errors.New("chunk: sizes must be positive")
	}
	if c.Min > c.Avg || c.Avg > c.Max {
		return fmt.Errorf("chunk: need min <= avg <= max, got %d/%d/%d", c.Min, c.Avg, c.Max)
	}
	if bits.Len(uint(c.Avg))+normalization > 63 {
		return errors.New("chunk: average size too large")
	}
	return nil
}

// Chunk describes one content-defined chunk of a stream.
type Chunk struct {
	// Offset is the chunk start within the original stream.
	Offset int64
	// Length is the plaintext chunk length.
	Length int
	// Data points into the chunker's internal buffer and is only valid until
	// the next call to Next.
	Data []byte
}

// Chunker splits a reader into content-defined chunks.
type Chunker struct {
	cfg Config
	r   io.Reader

	buf    []byte
	start  int // first unconsumed byte in buf
	end    int // end of valid data in buf
	offset int64
	eof    bool

	thresholdSmall uint64
	thresholdLarge uint64
}

// NewChunker wraps r. A zero Config selects the defaults.
func NewChunker(r io.Reader, cfg Config) (*Chunker, error) {
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	avgBits := bits.Len(uint(cfg.Avg)) - 1
	c := &Chunker{
		cfg: cfg,
		r:   r,
		buf: make([]byte, cfg.Max*2),
		// A cut point is declared when the rolling fingerprint falls below a
		// threshold; probability 2^-n, so a smaller threshold means longer
		// chunks. Before the average size we demand n = avgBits+2 (rare cut),
		// after it n = avgBits-2 (frequent cut).
		thresholdSmall: 1 << uint(64-(avgBits+normalization)),
		thresholdLarge: 1 << uint(64-(avgBits-normalization)),
	}
	return c, nil
}

// Next returns the next chunk, or io.EOF when the stream is exhausted.
func (c *Chunker) Next() (Chunk, error) {
	if err := c.fill(); err != nil {
		return Chunk{}, err
	}
	avail := c.end - c.start
	if avail == 0 {
		return Chunk{}, io.EOF
	}

	n := c.cutpoint(c.buf[c.start:c.end])
	ch := Chunk{Offset: c.offset, Length: n, Data: c.buf[c.start : c.start+n]}
	c.start += n
	c.offset += int64(n)
	return ch, nil
}

// fill tops up the buffer so it holds at least Max bytes when possible,
// compacting consumed data to the front first.
func (c *Chunker) fill() error {
	if c.end-c.start >= c.cfg.Max || c.eof {
		return nil
	}
	if c.start > 0 {
		copy(c.buf, c.buf[c.start:c.end])
		c.end -= c.start
		c.start = 0
	}
	for c.end < c.cfg.Max && !c.eof {
		n, err := c.r.Read(c.buf[c.end:])
		c.end += n
		if errors.Is(err, io.EOF) {
			c.eof = true
			break
		}
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
	}
	return nil
}

// cutpoint returns the length of the next chunk within data.
func (c *Chunker) cutpoint(data []byte) int {
	n := len(data)
	if n <= c.cfg.Min {
		return n
	}
	if n > c.cfg.Max {
		n = c.cfg.Max
	}
	normal := c.cfg.Avg
	if normal > n {
		normal = n
	}

	var fp uint64
	// The first Min bytes are never a cut point, and the paper skips hashing
	// them entirely, which is where a good part of FastCDC's speed comes from.
	i := c.cfg.Min
	for ; i < normal; i++ {
		fp = (fp << 1) + gear[data[i]]
		if fp < c.thresholdSmall {
			return i + 1
		}
	}
	for ; i < n; i++ {
		fp = (fp << 1) + gear[data[i]]
		if fp < c.thresholdLarge {
			return i + 1
		}
	}
	return n
}

// Split is a convenience helper that chunks an in-memory buffer, invoking fn
// for each chunk. The slice passed to fn aliases b and must not be retained.
func Split(b []byte, cfg Config, fn func(Chunk) error) error {
	if err := cfg.normalize(); err != nil {
		return err
	}
	c, err := NewChunker(nil, cfg)
	if err != nil {
		return err
	}
	var offset int64
	for len(b) > 0 {
		n := c.cutpoint(b)
		if err := fn(Chunk{Offset: offset, Length: n, Data: b[:n]}); err != nil {
			return err
		}
		offset += int64(n)
		b = b[n:]
	}
	return nil
}
