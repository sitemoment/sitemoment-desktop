// SPDX-License-Identifier: AGPL-3.0-or-later

package webdata

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

// mockServer is a tiny wiremock: routes by method + path, optional request
// checks, canned responses, and call counting.
type mockServer struct {
	*httptest.Server
	t      *testing.T
	mu     sync.Mutex
	routes []*route
}

type route struct {
	method, path string
	check        func(r *http.Request, body []byte) bool
	status       int
	headers      map[string]string
	body         string
	calls        int
}

func newMock(t *testing.T) *mockServer {
	m := &mockServer{t: t}
	m.Server = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.Close)
	return m
}

func (m *mockServer) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	m.mu.Lock()
	var hit *route
	for _, rt := range m.routes {
		if rt.method == r.Method && rt.path == r.URL.Path && (rt.check == nil || rt.check(r, body)) {
			hit = rt
			rt.calls++
			break
		}
	}
	m.mu.Unlock()
	if hit == nil {
		http.Error(w, "no route", http.StatusNotFound)
		return
	}
	for k, v := range hit.headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(hit.status)
	_, _ = io.WriteString(w, hit.body)
}

func (m *mockServer) on(method, path string, status int, body any, headers map[string]string, check func(*http.Request, []byte) bool) *route {
	s := ""
	switch b := body.(type) {
	case nil:
	case string:
		s = b
	default:
		raw, _ := json.Marshal(b)
		s = string(raw)
		if headers == nil {
			headers = map[string]string{}
		}
		headers["Content-Type"] = "application/json"
	}
	rt := &route{method: method, path: path, check: check, status: status, headers: headers, body: s}
	m.mu.Lock()
	m.routes = append(m.routes, rt)
	m.mu.Unlock()
	return rt
}

func (rt *route) count(m *mockServer) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return rt.calls
}

// partialJSON reports whether body contains want (objects: subset of keys,
// arrays: same length with element-wise partial match), like wiremock's
// body_partial_json.
func partialJSON(want any) func(*http.Request, []byte) bool {
	return func(_ *http.Request, body []byte) bool {
		var got any
		if json.Unmarshal(body, &got) != nil {
			return false
		}
		w, _ := json.Marshal(want)
		var wv any
		_ = json.Unmarshal(w, &wv)
		return subset(wv, got)
	}
}

func subset(want, got any) bool {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range w {
			if !subset(v, g[k]) {
				return false
			}
		}
		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for i := range w {
			if !subset(w[i], g[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(want, got)
	}
}

func header(name, value string) func(*http.Request, []byte) bool {
	return func(r *http.Request, _ []byte) bool { return r.Header.Get(name) == value }
}

func all(checks ...func(*http.Request, []byte) bool) func(*http.Request, []byte) bool {
	return func(r *http.Request, b []byte) bool {
		for _, c := range checks {
			if !c(r, b) {
				return false
			}
		}
		return true
	}
}

func kindOf(err error) (ErrorKind, bool) {
	if e, ok := err.(*Error); ok {
		return e.Kind, true
	}
	return 0, false
}

var bg = context.Background()
