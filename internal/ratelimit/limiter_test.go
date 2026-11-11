// SPDX-License-Identifier: AGPL-3.0-or-later
package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var ctx = context.Background()

func TestAcquireCapsConcurrencyPerKey(t *testing.T) {
	l := &Limiter{Max: 2}
	var active, max int32
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := l.Acquire(ctx, "acct@example.com")
			if err != nil {
				t.Error(err)
				return
			}
			n := atomic.AddInt32(&active, 1)
			mu.Lock()
			if n > max {
				max = n
			}
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			atomic.AddInt32(&active, -1)
			release()
		}()
	}
	wg.Wait()
	if max > 2 {
		t.Fatalf("max concurrency = %d, want <= 2", max)
	}
	if max < 2 {
		t.Fatalf("max concurrency = %d, want == 2 (never contended)", max)
	}
}

func TestAcquireDoesNotMixKeys(t *testing.T) {
	l := &Limiter{Max: 1}
	releaseA, err := l.Acquire(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseA()
	done := make(chan struct{})
	go func() {
		release, err := l.Acquire(ctx, "b")
		if err != nil {
			t.Error(err)
			return
		}
		release()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a different key was blocked by key \"a\"'s slot")
	}
}

// fakeClock lets the rate-limit test advance time deterministically: Sleep
// moves the clock forward by the requested duration instead of waiting.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	waits int
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.waits++
	c.mu.Unlock()
	return nil
}

func TestAcquirePacesByRate(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)}
	l := &Limiter{Rate: 2, Period: time.Minute, Now: clock.Now, Sleep: clock.Sleep}

	// The bucket starts full: the first Rate acquisitions are immediate.
	for i := 0; i < 2; i++ {
		if _, err := l.Acquire(ctx, "k"); err != nil {
			t.Fatal(err)
		}
	}
	if clock.waits != 0 {
		t.Fatalf("waited %d times before the bucket was empty, want 0", clock.waits)
	}

	// The 3rd call needs to wait out part of a token's refill time.
	if _, err := l.Acquire(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if clock.waits == 0 {
		t.Fatal("3rd call did not wait, want it paced by the rate")
	}
}

func TestAcquireStopsOnCanceledContext(t *testing.T) {
	l := &Limiter{Max: 1}
	release, err := l.Acquire(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := l.Acquire(cctx, "k"); err == nil {
		t.Fatal("Acquire with an already-canceled context returned nil error")
	}
}

func TestNilLimiterIsANoop(t *testing.T) {
	var l *Limiter
	release, err := l.Acquire(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	release()
}
