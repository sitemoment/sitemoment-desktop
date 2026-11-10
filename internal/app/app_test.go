// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/cloud"
	"github.com/sitemoment/sitemoment-desktop/internal/google"
	"github.com/sitemoment/sitemoment-desktop/internal/secret"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

var ctx = context.Background()

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return st
}

// browser plays the user's browser: it follows the auth URL straight back
// to the loopback redirect with the given code.
func browser(t *testing.T, code string) Opener {
	return func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		q := u.Query()
		go func() {
			res, err := http.Get(q.Get("redirect_uri") + "?" + url.Values{"code": {code}, "state": {q.Get("state")}}.Encode())
			if err != nil {
				t.Error(err)
				return
			}
			res.Body.Close()
		}()
		return nil
	}
}

func TestAccountLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/desktop/token":
			var req map[string]string
			json.NewDecoder(r.Body).Decode(&req)
			if req["code"] != "c1" || req["code_verifier"] == "" {
				w.WriteHeader(400)
				return
			}
			w.Write([]byte(`{"access_token":"a1","refresh_token":"r1","expires_in":3600}`))
		case "/v1/desktop/me":
			w.Write([]byte(`{"user":{"email":"me@example.com","name":"Me"},"plan":{"name":"free"}}`))
		}
	}))
	defer srv.Close()
	a := &AccountService{Cloud: &cloud.Client{Base: srv.URL, HTTP: srv.Client(), Secrets: secret.NewMemory()}, Open: browser(t, "c1")}
	if acct, err := a.Get(ctx); err != nil || acct.SignedIn {
		t.Fatalf("before login: %+v, %v", acct, err)
	}
	acct, err := a.Login(ctx)
	if err != nil || !acct.SignedIn || acct.Email != "me@example.com" || acct.Plan != "free" {
		t.Fatalf("Login = %+v, %v", acct, err)
	}
}

func idToken(email string) string {
	return "h." + base64.RawURLEncoding.EncodeToString([]byte(`{"email":"`+email+`"}`)) + ".s"
}

func TestGoogleConnectAndProperties(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Write([]byte(`{"access_token":"gat","refresh_token":"grt","expires_in":3600,"scope":"openid email https://www.googleapis.com/auth/webmasters.readonly","id_token":"` + idToken("me@example.com") + `"}`))
		case "/sites":
			if r.Header.Get("Authorization") != "Bearer gat" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"siteEntry":[{"siteUrl":"sc-domain:example.com","permissionLevel":"siteOwner"}]}`))
		}
	}))
	defer srv.Close()
	st, sec := openStore(t), secret.NewMemory()
	oauth := &google.OAuth{ClientID: "cid", TokenURL: srv.URL + "/token", HTTP: srv.Client()}
	g := &GoogleService{OAuth: oauth, GSC: &google.GSC{HTTP: srv.Client(), Base: srv.URL}, Tokens: &GoogleTokens{OAuth: oauth, Secrets: sec},
		Store: st, Secrets: sec, Open: browser(t, "g1")}

	conn, err := g.Connect(ctx)
	if err != nil || conn.Account != "me@example.com" {
		t.Fatalf("Connect = %+v, %v", conn, err)
	}
	if v, _ := sec.Get("google:me@example.com"); v != "grt" {
		t.Fatalf("refresh token in keychain = %q", v)
	}
	props, err := g.Properties(ctx, "me@example.com")
	if err != nil || len(props) != 1 || props[0].SiteURL != "sc-domain:example.com" {
		t.Fatalf("Properties = %+v, %v", props, err)
	}
	if err := g.Disconnect(ctx, "me@example.com"); err != nil {
		t.Fatal(err)
	}
	if list, _ := g.Connections(ctx); len(list) != 0 {
		t.Fatalf("connections after disconnect: %+v", list)
	}
}

func TestBindTriggersSync(t *testing.T) {
	st := openStore(t)
	var synced atomic.Int64
	s := &SiteService{Store: st, Sync: func(id int64) { synced.Store(id) }}
	site, err := s.Add(ctx, "https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BindGSC(ctx, site.ID, "me@example.com", "sc-domain:example.com"); err != nil {
		t.Fatal(err)
	}
	if synced.Load() != site.ID {
		t.Fatalf("sync not triggered for %d", site.ID)
	}
}

func TestOverviewRanges(t *testing.T) {
	st := openStore(t)
	site, _ := st.AddSite(ctx, "example.com", 1)
	o := &OverviewService{Store: st}
	ov, err := o.Get(ctx, site.ID, 7)
	if err != nil || ov.Synced {
		t.Fatalf("unsynced overview = %+v, %v", ov, err)
	}
	var rows []store.GSCRow
	for d := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC); !d.After(time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)); d = d.AddDate(0, 0, 1) {
		rows = append(rows, store.GSCRow{Day: d.Format(store.DayLayout), Clicks: 1, Impressions: 10, Position: 5})
	}
	if err := st.ReplaceGSC(ctx, site.ID, "date", "2026-10-01", "2026-11-08", rows, 1); err != nil {
		t.Fatal(err)
	}
	ov, err = o.Get(ctx, site.ID, 7)
	if err != nil || !ov.Synced || ov.To != "2026-11-08" || ov.From != "2026-11-02" || ov.PrevTo != "2026-11-01" || ov.PrevFrom != "2026-10-26" {
		t.Fatalf("ranges = %+v, %v", ov, err)
	}
	if ov.Current.Clicks != 7 || len(ov.Daily) != 7 || len(ov.PrevDaily) != 7 {
		t.Fatalf("data = %+v", ov)
	}
	if ov, _ := o.Get(ctx, site.ID, 13); ov.Days != 28 {
		t.Fatalf("odd range not normalized: %d", ov.Days)
	}
}

// TestOverviewFillsMissingDays covers GSC omitting days with zero data: the
// overview chart compares current vs previous period by index, so Daily and
// PrevDaily must have exactly one DayPoint per day in range, even when a day
// has no facts row at all.
func TestOverviewFillsMissingDays(t *testing.T) {
	st := openStore(t)
	site, _ := st.AddSite(ctx, "example.com", 1)
	o := &OverviewService{Store: st}

	// Window: 2026-11-02..2026-11-08 (7 days). Skip 2026-11-05 entirely, as
	// GSC does for a day with no clicks or impressions.
	var rows []store.GSCRow
	missing := "2026-11-05"
	for d := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC); !d.After(time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)); d = d.AddDate(0, 0, 1) {
		day := d.Format(store.DayLayout)
		if day == missing {
			continue
		}
		rows = append(rows, store.GSCRow{Day: day, Clicks: 2, Impressions: 20, Position: 5})
	}
	// ReplaceGSC still records the missing day as fetched (with 0 rows), so
	// LatestDay sees 2026-11-08 as synced.
	if err := st.ReplaceGSC(ctx, site.ID, "date", "2026-11-02", "2026-11-08", rows, 1); err != nil {
		t.Fatal(err)
	}

	ov, err := o.Get(ctx, site.ID, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.Daily) != 7 {
		t.Fatalf("len(Daily) = %d, want 7: %+v", len(ov.Daily), ov.Daily)
	}
	var found bool
	for i, p := range ov.Daily {
		if i > 0 && p.Day <= ov.Daily[i-1].Day {
			t.Fatalf("Daily not in ascending order at %d: %+v", i, ov.Daily)
		}
		if p.Day == missing {
			found = true
			if p.Clicks != 0 || p.Impressions != 0 {
				t.Fatalf("missing day not zeroed: %+v", p)
			}
		}
	}
	if !found {
		t.Fatalf("missing day %s not present in Daily: %+v", missing, ov.Daily)
	}
}
