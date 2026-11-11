// SPDX-License-Identifier: AGPL-3.0-or-later

package webdata

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestWithProxy: the clients send their requests to the proxy WithProxy
// picks (the app passes netproxy's system-proxy resolver).
func TestWithProxy(t *testing.T) {
	var seen []string
	px := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`["q",["q one","q two"]]`))
	}))
	defer px.Close()
	pu, _ := url.Parse(px.URL)
	s := NewSuggest("http://suggest.example/complete", WithProxy(http.ProxyURL(pu)))
	got, err := s.Complete(bg, "q", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(seen) != 1 || seen[0] != "http://suggest.example/complete?client=firefox&q=q" {
		t.Fatalf("got %v, proxy saw %v", got, seen)
	}
}
