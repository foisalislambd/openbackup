package throttle

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeClock lets the rate limiter be tested without sleeping.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestUnlimitedNeverBlocks(t *testing.T) {
	b := NewBucket(0)
	if err := b.Wait(context.Background(), 1<<30); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if b.Rate() != 0 {
		t.Fatalf("Rate() = %d, want 0", b.Rate())
	}
}

func TestBucketPacesTraffic(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1700000000, 0)}
	b := NewBucket(1000)
	b.now = clock.now
	b.last = clock.now()
	b.tokens = b.capacity

	// The full burst is available immediately.
	if d, ok := b.reserve(1000); !ok {
		t.Fatalf("expected the initial burst to be granted, wait=%v", d)
	}
	// The bucket is now empty, so the next byte has to wait.
	d, ok := b.reserve(100)
	if ok {
		t.Fatal("expected the empty bucket to defer")
	}
	if d < 90*time.Millisecond || d > 110*time.Millisecond {
		t.Fatalf("expected roughly 100ms of wait for 100 bytes at 1000 B/s, got %v", d)
	}

	clock.advance(200 * time.Millisecond)
	if _, ok := b.reserve(100); !ok {
		t.Fatal("expected tokens to refill after time passes")
	}
}

func TestOversizedRequestIsNotDeadlocked(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1700000000, 0)}
	b := NewBucket(100)
	b.now = clock.now
	b.last = clock.now()
	b.tokens = b.capacity

	// 4 MiB chunk on a 100 B/s link: it must eventually pass, not hang forever.
	if _, ok := b.reserve(4 << 20); !ok {
		t.Fatal("expected an oversized request to be granted from a full bucket")
	}
}

func TestWaitRespectsContext(t *testing.T) {
	b := NewBucket(1) // 1 byte per second
	if err := b.Wait(context.Background(), 1); err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := b.Wait(ctx, 1); err == nil {
		t.Fatal("expected the context deadline to interrupt the wait")
	}
}

func TestSetRateAtRuntime(t *testing.T) {
	b := NewBucket(1 << 20)
	b.SetRate(0)
	if err := b.Wait(context.Background(), 1<<30); err != nil {
		t.Fatalf("Wait after disabling the limit: %v", err)
	}
	b.SetRate(2048)
	if got := b.Rate(); got != 2048 {
		t.Fatalf("Rate() = %d, want 2048", got)
	}
}
