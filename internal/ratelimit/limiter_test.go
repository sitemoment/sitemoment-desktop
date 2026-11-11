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

func TestAcquireStartsWithASmallBurstNotTheFullRate(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)}
	// Rate is big (120/min); Burst is small (4): a cold start must not be
	// able to immediately fire near Rate requests before any pacing kicks
	// in (the bug this guards: the bucket used to seed at the full Rate).
	l := &Limiter{Rate: 120, Period: time.Minute, Burst: 4, Now: clock.Now, Sleep: clock.Sleep}

	for i := 0; i < 4; i++ {
		if _, err := l.Acquire(ctx, "k"); err != nil {
			t.Fatal(err)
		}
	}
	if clock.waits != 0 {
		t.Fatalf("waited %d times within the burst of 4, want 0", clock.waits)
	}
	if _, err := l.Acquire(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if clock.waits == 0 {
		t.Fatal("the 5th call did not wait, want it paced once the burst is spent")
	}
}

func TestAcquireBurstDefaultsToMaxThenToOne(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)}
	withMax := &Limiter{Max: 3, Rate: 60, Period: time.Minute, Now: clock.Now, Sleep: clock.Sleep}
	for i := 0; i < 3; i++ {
		if _, err := withMax.Acquire(ctx, "k"); err != nil {
			t.Fatal(err)
		}
	}
	if clock.waits != 0 {
		t.Fatalf("Max=3 as the burst: waited %d times in the first 3 calls, want 0", clock.waits)
	}

	clock2 := &fakeClock{now: time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)}
	bare := &Limiter{Rate: 60, Period: time.Minute, Now: clock2.Now, Sleep: clock2.Sleep}
	if _, err := bare.Acquire(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if clock2.waits != 0 {
		t.Fatal("no Max or Burst: the 1st call should still be immediate")
	}
	if _, err := bare.Acquire(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if clock2.waits == 0 {
		t.Fatal("no Max or Burst: the 2nd call should already be paced (burst defaults to 1)")
	}
}

// TestAcquireRollingWindowStaysNearRate is the regression test for the
// startup-burst bug: it drives the limiter continuously and checks that no
// Period-long rolling window ever allows much more than Rate+Burst calls,
// including — this is what used to fail — the very first window.
func TestAcquireRollingWindowStaysNearRate(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)}
	l := &Limiter{Rate: 120, Period: time.Minute, Burst: 4, Now: clock.Now, Sleep: clock.Sleep}

	start := clock.now
	n := 0
	for clock.Now().Sub(start) < time.Minute {
		if _, err := l.Acquire(ctx, "k"); err != nil {
			t.Fatal(err)
		}
		n++
		if n > 10000 {
			t.Fatal("runaway loop: Acquire never paced")
		}
	}
	if n > 130 {
		t.Fatalf("%d acquisitions within the first rolling minute, want <= 130 (burst 4 + rate 120, with margin)", n)
	}

	// A second rolling minute, after the key has been idle a while (lots of
	// refill accumulated): it must be bounded exactly the same way, not
	// just the first one.
	clock.mu.Lock()
	clock.now = clock.now.Add(10 * time.Minute)
	clock.mu.Unlock()
	start2 := clock.Now()
	n2 := 0
	for clock.Now().Sub(start2) < time.Minute {
		if _, err := l.Acquire(ctx, "k"); err != nil {
			t.Fatal(err)
		}
		n2++
		if n2 > 10000 {
			t.Fatal("runaway loop: Acquire never paced")
		}
	}
	if n2 > 130 {
		t.Fatalf("%d acquisitions in a later rolling minute (after being idle), want <= 130", n2)
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
