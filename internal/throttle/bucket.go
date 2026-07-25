// Package throttle implements the byte-rate limiter used to pace uploads.
//
// Saturating the uplink is the single most common complaint about backup
// software: video calls stutter and the user uninstalls. A token bucket lets the
// agent stay well below the available bandwidth and burst only briefly.
package throttle

import (
	"context"
	"sync"
	"time"
)

// Bucket is a token bucket measured in bytes. A zero rate means unlimited.
type Bucket struct {
	mu       sync.Mutex
	rate     float64 // bytes per second
	capacity float64
	tokens   float64
	last     time.Time
	// now is swappable for tests.
	now func() time.Time
}

// NewBucket returns a limiter allowing bytesPerSec on average. Burst defaults to
// one second of traffic, which is enough to keep throughput smooth without
// noticeably spiking latency for interactive traffic.
func NewBucket(bytesPerSec int64) *Bucket {
	b := &Bucket{now: time.Now}
	b.SetRate(bytesPerSec)
	return b
}

// SetRate changes the limit at runtime; 0 disables limiting.
func (b *Bucket) SetRate(bytesPerSec int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if bytesPerSec <= 0 {
		b.rate, b.capacity, b.tokens = 0, 0, 0
		return
	}
	b.rate = float64(bytesPerSec)
	b.capacity = float64(bytesPerSec)
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	if b.last.IsZero() {
		b.last = b.now()
		b.tokens = b.capacity
	}
}

// Rate reports the current limit in bytes per second; 0 means unlimited.
func (b *Bucket) Rate() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return int64(b.rate)
}

// Wait blocks until n bytes may be sent or ctx is cancelled.
func (b *Bucket) Wait(ctx context.Context, n int) error {
	if n <= 0 {
		return ctx.Err()
	}
	for {
		delay, ok := b.reserve(n)
		if ok {
			return nil
		}
		if delay <= 0 {
			delay = time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// reserve tries to take n tokens, reporting how long to wait when it cannot.
func (b *Bucket) reserve(n int) (time.Duration, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rate == 0 {
		return 0, true
	}
	now := b.now()
	if b.last.IsZero() {
		b.last = now
	}
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * b.rate
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		b.last = now
	}

	need := float64(n)
	// A single chunk can exceed the per-second capacity on a very slow link.
	// Allow it through once the bucket is full rather than deadlocking.
	if need > b.capacity {
		need = b.capacity
	}
	if b.tokens >= need {
		b.tokens -= need
		return 0, true
	}
	missing := need - b.tokens
	return time.Duration(missing / b.rate * float64(time.Second)), false
}
