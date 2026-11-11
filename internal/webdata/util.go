// SPDX-License-Identifier: AGPL-3.0-or-later

package webdata

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Option configures a client built by NewPSI, NewWayback or NewSuggest.
type Option func(*http.Transport)

// WithProxy sets the proxy function (the app passes netproxy's, which
// follows the system proxy); without it proxies come from the environment.
func WithProxy(proxy func(*http.Request) (*url.URL, error)) Option {
	return func(t *http.Transport) { t.Proxy = proxy }
}

// newClient has a 10 s connect timeout and an overall request timeout.
// Proxies come from the environment, like reqwest's default, unless an
// option says otherwise.
func newClient(timeout time.Duration, opts ...Option) *http.Client {
	t := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	}
	for _, o := range opts {
		o(t)
	}
	return &http.Client{Timeout: timeout, Transport: t}
}

// truncate keeps at most maxChars characters of s, for embedding upstream
// error bodies into error messages.
func truncate(s string, maxChars int) string {
	n := 0
	for i := range s {
		if n == maxChars {
			return s[:i]
		}
		n++
	}
	return s
}

// statusText renders a status like "500 Internal Server Error".
func statusText(code int) string {
	t := http.StatusText(code)
	if t == "" {
		t = "<unknown status code>"
	}
	return fmt.Sprintf("%d %s", code, t)
}

func success(code int) bool { return code >= 200 && code < 300 }

// headerU64 parses an unsigned integer header.
func headerU64(h http.Header, name string) (uint64, bool) {
	v, err := strconv.ParseUint(h.Get(name), 10, 64)
	return v, err == nil
}

func retryAfter(h http.Header, def, max uint64) uint64 {
	v, ok := headerU64(h, "Retry-After")
	if !ok {
		v = def
	}
	if v > max {
		v = max
	}
	return v
}

// decodeJSON parses a single JSON value, keeping numbers as json.Number so
// integers and floats can be told apart (like serde_json's Value).
func decodeJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing characters after JSON value")
	}
	return v, nil
}

// get is obj[key], or nil when v is not an object or has no such key.
func get(v any, key string) any {
	if m, ok := v.(map[string]any); ok {
		return m[key]
	}
	return nil
}

// at is arr[i], or nil.
func at(v any, i int) any {
	if a, ok := v.([]any); ok && i >= 0 && i < len(a) {
		return a[i]
	}
	return nil
}

func path(v any, keys ...string) any {
	for _, k := range keys {
		v = get(v, k)
	}
	return v
}

func asStr(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func asBool(v any) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

func asArr(v any) ([]any, bool) {
	a, ok := v.([]any)
	return a, ok
}

func isObj(v any) bool {
	_, ok := v.(map[string]any)
	return ok
}

func asF64(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil
}

func asU64(v any) (uint64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	u, err := strconv.ParseUint(string(n), 10, 64)
	return u, err == nil
}

func asI64(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := strconv.ParseInt(string(n), 10, 64)
	return i, err == nil
}

// clampU8 clamps to 0..100 and truncates like Rust's `as u8`.
func clampPct(f float64) uint8 {
	if f != f || f < 0 {
		return 0
	}
	if f > 100 {
		f = 100
	}
	return uint8(f)
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}

func readBody(r io.Reader) []byte {
	b, _ := io.ReadAll(r)
	return b
}
