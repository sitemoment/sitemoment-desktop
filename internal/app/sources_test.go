// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sitemoment/sitemoment-desktop/internal/bing"
	"github.com/sitemoment/sitemoment-desktop/internal/google"
	"github.com/sitemoment/sitemoment-desktop/internal/secret"
)

func TestGAProperties(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Write([]byte(`{"access_token":"gat","expires_in":3600}`))
		case "/accountSummaries":
			if r.Header.Get("Authorization") != "Bearer gat" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"accountSummaries":[{"account":"accounts/1","displayName":"Me","propertySummaries":[{"property":"properties/9","displayName":"example.com"}]}]}`))
		}
	}))
	defer srv.Close()
	sec := secret.NewMemory()
	sec.Set("google:me@example.com", "grt")
	oauth := &google.OAuth{ClientID: "cid", TokenURL: srv.URL + "/token", HTTP: srv.Client()}
	g := &GoogleService{OAuth: oauth, GA: &google.GA{HTTP: srv.Client(), AdminBase: srv.URL}, Tokens: &GoogleTokens{OAuth: oauth, Secrets: sec}, Secrets: sec}
	props, err := g.GAProperties(ctx, "me@example.com")
	if err != nil || len(props) != 1 || props[0].Property != "properties/9" || props[0].AccountName != "Me" {
		t.Fatalf("GAProperties = %+v, %v", props, err)
	}
	if _, err := g.GAProperties(ctx, "nobody@example.com"); !errors.Is(err, google.ErrReauth) {
		t.Fatalf("unknown account: %v", err)
	}
}

func TestBindGAAndBing(t *testing.T) {
	st := openStore(t)
	var synced atomic.Int64
	s := &SiteService{Store: st, Sync: func(id int64) { synced.Add(1) }}
	site, _ := s.Add(ctx, "example.com")
	if err := s.BindGA(ctx, site.ID, "me@example.com", "properties/9"); err != nil {
		t.Fatal(err)
	}
	if err := s.BindBing(ctx, site.ID, "bing-abcd", "https://example.com/"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Site(ctx, site.ID)
	if got.GAProperty != "properties/9" || got.BingSite != "https://example.com/" || synced.Load() != 2 {
		t.Fatalf("site = %+v, syncs = %d", got, synced.Load())
	}
	for _, bad := range [][2]string{{"me@example.com", ""}, {"", "properties/9"}, {"me@example.com", "123"}} {
		if err := s.BindGA(ctx, site.ID, bad[0], bad[1]); err == nil {
			t.Errorf("BindGA(%q, %q) accepted", bad[0], bad[1])
		}
	}
	if err := s.BindBing(ctx, site.ID, "bing-abcd", ""); err == nil {
		t.Error("BindBing without a site accepted")
	}
	if err := s.BindGA(ctx, site.ID, "", ""); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	if synced.Load() != 3 {
		t.Fatalf("syncs after unbind = %d", synced.Load())
	}
}

func TestBingKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("apikey") != "goodkey1234" {
			w.WriteHeader(400)
			w.Write([]byte(`{"ErrorCode":3,"Message":"InvalidApiKey"}`))
			return
		}
		w.Write([]byte(`{"d":[{"Url":"https://example.com/","IsVerified":true}]}`))
	}))
	defer srv.Close()
	st, sec := openStore(t), secret.NewMemory()
	b := &BingService{Bing: &bing.Client{HTTP: srv.Client(), Base: srv.URL}, Store: st, Secrets: sec}

	if _, err := b.AddKey(ctx, "badkey9999"); !errors.Is(err, bing.ErrInvalidKey) {
		t.Fatalf("bad key: %v", err)
	}
	if list, _ := b.Keys(ctx); len(list) != 0 {
		t.Fatalf("a bad key was kept: %+v", list)
	}
	conn, err := b.AddKey(ctx, "  goodkey1234 \n")
	if err != nil || conn.Account != "bing-1234" || conn.Kind != "bing" {
		t.Fatalf("AddKey = %+v, %v", conn, err)
	}
	if v, _ := sec.Get("bing:bing-1234"); v != "goodkey1234" {
		t.Fatalf("keychain = %q", v)
	}
	sites, err := b.Sites(ctx, "bing-1234")
	if err != nil || len(sites) != 1 || !sites[0].Verified {
		t.Fatalf("Sites = %+v, %v", sites, err)
	}
	if k, err := (&BingKeys{Secrets: sec}).BingKey("bing-1234"); err != nil || k != "goodkey1234" {
		t.Fatalf("BingKey = %q, %v", k, err)
	}
	if err := b.RemoveKey(ctx, "bing-1234"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Sites(ctx, "bing-1234"); !errors.Is(err, bing.ErrInvalidKey) {
		t.Fatalf("after remove: %v", err)
	}
	if list, _ := b.Keys(ctx); len(list) != 0 {
		t.Fatalf("keys after remove = %+v", list)
	}
	if _, err := b.AddKey(ctx, "short"); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("short key: %v", err)
	}
}
