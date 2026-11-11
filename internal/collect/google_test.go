// SPDX-License-Identifier: AGPL-3.0-or-later
package collect

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGoogleResolver(t *testing.T) {
	var followed atomic.Int32
	var mu sync.Mutex
	seen := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		mu.Lock()
		seen[q]++
		n := seen[q]
		mu.Unlock()
		switch {
		case r.URL.Path == "/url" && q == "TOKEN1":
			w.Header().Set("Location", "https://www.kapwing.com/tools/make/youtube-thumbnail")
			w.WriteHeader(302)
		case r.URL.Path == "/url" && q == "THROTTLED" && n == 1:
			// Google's throttle page right after a search.
			w.Write([]byte(`<!doctype html><html><head><title>Google</title><meta content="5;url=/goto?url=THROTTLED" http-equiv="refresh"></head></html>`))
		case r.URL.Path == "/url" && q == "THROTTLED":
			w.Header().Set("Location", "https://pixteller.com/thumbnail-maker")
			w.WriteHeader(302)
		case r.URL.Path == "/url" && q == "TOKEN2":
			w.Header().Set("Location", "javascript:alert(1)")
			w.WriteHeader(302)
		case r.URL.Path == "/url":
			w.Write([]byte("<html>no redirect</html>"))
		default:
			followed.Add(1)
		}
	}))
	defer srv.Close()
	in := `{"url":"https://www.google.com/search?q=x","organic":[` +
		`{"rank":1,"title":"Kapwing","redirect":"/url?q=TOKEN1&sa=U","breadcrumb":"www.kapwing.com › Tools › Make"},` +
		`{"rank":2,"title":"Fliki","redirect":"/url?q=TOKEN2&sa=U","breadcrumb":"fliki.ai › Features"},` +
		`{"rank":3,"title":"Play","url":"https://play.google.com/store/apps/details?id=x","urlFrom":"redirect","breadcrumb":"play.google.com › store"},` +
		`{"rank":4,"title":"Odd","redirect":"https://evil.example/url?q=1","breadcrumb":"no site here"},` +
		`{"rank":5,"title":"PixTeller","redirect":"/url?q=THROTTLED&sa=U","breadcrumb":"pixteller.com › thumbnail-maker"},` +
		`{"rank":6,"title":"Gone","redirect":"/url?q=GONE&sa=U","breadcrumb":"www.renderforest.com › youtube"}]}`
	var sleeps []time.Duration
	g := &GoogleResolver{Base: srv.URL, HTTP: srv.Client(), Sleep: func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	}}
	out, err := g.Post(ctx, json.RawMessage(in))
	if err != nil {
		t.Fatal(err)
	}
	var s SERP
	json.Unmarshal(out, &s)
	want := []struct{ url, from, domain string }{
		{"https://www.kapwing.com/tools/make/youtube-thumbnail", "redirect", "kapwing.com"},
		{"https://fliki.ai/", "breadcrumb", "fliki.ai"},
		{"https://play.google.com/store/apps/details?id=x", "redirect", "play.google.com"},
		{"", "", ""},
		{"https://pixteller.com/thumbnail-maker", "redirect", "pixteller.com"},
		{"https://www.renderforest.com/", "breadcrumb", "renderforest.com"},
	}
	for i, w := range want {
		o := s.Organic[i]
		if o.URL != w.url || o.URLFrom != w.from || o.Domain != w.domain || o.Redirect != "" {
			t.Errorf("organic %d = %+v, want %+v", i, o, w)
		}
	}
	if followed.Load() != 0 {
		t.Fatal("the resolver followed a redirect")
	}
	// First pass: 4 links one second apart; then a 10s pause and the throttled one alone.
	wantSleeps := []time.Duration{time.Second, time.Second, time.Second, 10 * time.Second}
	if len(sleeps) != len(wantSleeps) {
		t.Fatalf("sleeps = %v", sleeps)
	}
	for i := range wantSleeps {
		if sleeps[i] != wantSleeps[i] {
			t.Fatalf("sleeps = %v, want %v", sleeps, wantSleeps)
		}
	}
	if seen["GONE"] != 1 || seen["THROTTLED"] != 2 {
		t.Fatalf("requests = %v", seen)
	}
}
