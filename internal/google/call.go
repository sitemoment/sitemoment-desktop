// SPDX-License-Identifier: AGPL-3.0-or-later
package google

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// maxAttempts is how many times callJSON tries a request that fails with a
// retryable error (429, 5xx, or a network error) before giving up.
const maxAttempts = 4

// baseBackoff and maxBackoff bound the exponential backoff between retries
// when the server does not send a Retry-After header.
const (
	baseBackoff = 2 * time.Second
	maxBackoff  = 30 * time.Second
)

var (
	// ErrRateLimited means a Google API asked us to slow down on every attempt.
	ErrRateLimited = errors.New("google: rate limited, try again later")
	// ErrServerError means a Google API returned a 5xx on every attempt.
	ErrServerError = errors.New("google: server error")
)

// caller is what callJSON needs from a client.
type caller struct {
	http *http.Client
	// sleep waits out a retry backoff; nil uses realSleep.
	sleep func(context.Context, time.Duration) error
	// product names the API in error messages ("Search Console", "Analytics").
	product string
	// forbidden turns a 403's error message into the error to return.
	forbidden func(message string) error
}

// realSleep waits for d or until ctx is done, whichever comes first.
func realSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// backoff picks how long to wait before the next attempt (1-based: the
// delay before the 2nd attempt is backoff(1, 0)). retryAfter, when > 0,
// takes precedence over the exponential schedule, but is still clamped to
// maxBackoff: a server-supplied (or bogus) Retry-After must not be able to
// park a sync slot for an arbitrarily long time.
func backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > maxBackoff {
			return maxBackoff
		}
		return retryAfter
	}
	d := baseBackoff
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= maxBackoff {
			return maxBackoff
		}
	}
	return d
}

// retryAfterHeader parses a Retry-After response header given in seconds;
// anything else (missing, "0", non-numeric, HTTP-date) is ignored, falling
// back to the exponential schedule.
func retryAfterHeader(h http.Header) time.Duration {
	s := h.Get("Retry-After")
	if s == "" {
		return 0
	}
	secs, err := strconv.Atoi(s)
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// googleMessage is error.message of a Google API error body, or "".
func googleMessage(data []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(data, &e)
	return e.Error.Message
}

// callJSON sends a JSON request with a bearer token and decodes the JSON
// answer into out, retrying 429s, 5xx and network errors.
func callJSON(ctx context.Context, c caller, method, endpoint, token string, body, out any) error {
	sleep := c.sleep
	if sleep == nil {
		sleep = realSleep
	}
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		raw = b
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var rd io.Reader
		if raw != nil {
			rd = bytes.NewReader(raw) // fresh reader per attempt: body must be re-sent on retry
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, rd)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if raw != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		res, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = err
			if attempt == maxAttempts {
				return err
			}
			if serr := sleep(ctx, backoff(attempt, 0)); serr != nil {
				return serr
			}
			continue
		}

		data, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
		res.Body.Close()
		if err != nil {
			return err
		}

		switch {
		case res.StatusCode == http.StatusUnauthorized:
			return ErrReauth
		case res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500:
			if res.StatusCode == http.StatusTooManyRequests {
				lastErr = ErrRateLimited
			} else {
				lastErr = fmt.Errorf("google: %s HTTP %d: %s: %w", c.product, res.StatusCode, googleMessage(data), ErrServerError)
			}
			if attempt == maxAttempts {
				return lastErr
			}
			if serr := sleep(ctx, backoff(attempt, retryAfterHeader(res.Header))); serr != nil {
				return serr
			}
			continue
		case res.StatusCode == http.StatusForbidden:
			return c.forbidden(googleMessage(data))
		case res.StatusCode >= 400:
			return fmt.Errorf("google: %s HTTP %d: %s", c.product, res.StatusCode, googleMessage(data))
		}
		return json.Unmarshal(data, out)
	}
	return lastErr
}
