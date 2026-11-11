// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ratelimit paces outbound API calls per key (a Google account's
// email, or a Bing API key): at most Max calls in flight at once, and at
// most Rate calls every Period, with one token bucket and one concurrency
// semaphore per key. This runs in front of (not instead of) a client's own
// 429 backoff (internal/google, internal/bing): it exists to make a 429
// unlikely in the first place when one site, or several sites sharing the
// same account or key, sync at once.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Limiter paces calls keyed by an account or API key. The zero value, or a
// nil *Limiter, is a no-op (Acquire always returns immediately).
type Limiter struct {
	Max    int           // concurrent calls in flight per key; <=0 means unlimited
	Rate   int           // calls per Period per key; <=0 means unlimited
	Period time.Duration // default time.Minute

	// Now and Sleep are overridden in tests; nil uses real time.
	Now   func() time.Time
	Sleep func(context.Context, time.Duration) error

	mu   sync.Mutex
	keys map[string]*keyState
}

// keyState is one key's concurrency slots and token bucket.
type keyState struct {
	sem chan struct{} // nil: concurrency is unlimited for this key

	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func (l *Limiter) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Limiter) sleep(ctx context.Context, d time.Duration) error {
	if l.Sleep != nil {
		return l.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (l *Limiter) state(key string) *keyState {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.keys == nil {
		l.keys = map[string]*keyState{}
	}
	if ks, ok := l.keys[key]; ok {
		return ks
	}
	ks := &keyState{}
	if l.Max > 0 {
		ks.sem = make(chan struct{}, l.Max)
	}
	l.keys[key] = ks
	return ks
}

// Acquire blocks until a concurrency slot and a rate-limit token are free
// for key, or ctx ends. The caller must call the returned release func
// (exactly once) when the call is done, freeing the concurrency slot; the
// token itself is spent, not returned.
func (l *Limiter) Acquire(ctx context.Context, key string) (release func(), err error) {
	if l == nil {
		return func() {}, nil
	}
	ks := l.state(key)

	if ks.sem != nil {
		select {
		case ks.sem <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	release = func() {
		if ks.sem != nil {
			<-ks.sem
		}
	}

	if l.Rate > 0 {
		period := l.Period
		if period <= 0 {
			period = time.Minute
		}
		perSecond := float64(l.Rate) / period.Seconds()
		for {
			ks.mu.Lock()
			now := l.now()
			if ks.last.IsZero() {
				ks.last, ks.tokens = now, float64(l.Rate)
			} else if elapsed := now.Sub(ks.last).Seconds(); elapsed > 0 {
				ks.tokens = min(float64(l.Rate), ks.tokens+elapsed*perSecond)
				ks.last = now
			}
			if ks.tokens >= 1 {
				ks.tokens--
				ks.mu.Unlock()
				break
			}
			wait := time.Duration((1 - ks.tokens) / perSecond * float64(time.Second))
			ks.mu.Unlock()
			if err := l.sleep(ctx, wait); err != nil {
				release()
				return nil, err
			}
		}
	}
	return release, nil
}
