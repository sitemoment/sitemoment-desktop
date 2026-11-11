// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sitemoment/sitemoment-desktop/internal/netproxy"
)

func TestNetworkStatusAndTest(t *testing.T) {
	px := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Host {
		case "www.google.example":
			w.WriteHeader(http.StatusNoContent)
		case "api.example":
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	defer px.Close()
	pu, _ := url.Parse(px.URL)
	r := &netproxy.Resolver{
		Getenv:     func(string) string { return "" },
		ReadSystem: func() (netproxy.System, error) { return netproxy.System{HTTP: pu.Host, HTTPS: pu.Host}, nil },
	}
	n := &NetworkService{Proxy: r, Probes: []string{"http://www.google.example/generate_204", "http://api.example/healthz"}}

	st := n.Status()
	if st.Source != netproxy.SourceSystem || st.HTTPProxy != pu.Host {
		t.Fatalf("Status = %+v", st)
	}
	res := n.Test(ctx)
	if len(res) != 2 {
		t.Fatalf("Test = %+v", res)
	}
	if g := res[0]; !g.OK || g.Status != 204 || g.Via != "HTTP "+pu.Host || g.URL != n.Probes[0] {
		t.Errorf("google probe %+v", g)
	}
	if a := res[1]; a.OK || a.Status != 502 || a.Error == "" {
		t.Errorf("api probe %+v", a)
	}

	// The proxy going away is reported as such, in Chinese.
	px.Close()
	res = n.Test(ctx)
	if res[0].OK || !strings.Contains(res[0].Error, "连不上代理") {
		t.Errorf("proxy down: %+v", res[0])
	}
}

func TestNetworkTestDirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()
	r := &netproxy.Resolver{Getenv: func(string) string { return "" }, ReadSystem: func() (netproxy.System, error) { return netproxy.System{}, nil }}
	n := &NetworkService{Proxy: r, Probes: []string{srv.URL + "/generate_204"}}
	res := n.Test(ctx)
	if len(res) != 1 || !res[0].OK || res[0].Via != "直连" {
		t.Fatalf("Test = %+v", res)
	}
}
