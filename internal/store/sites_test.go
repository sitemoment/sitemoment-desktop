// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"errors"
	"testing"
)

func TestNormalizeHost(t *testing.T) {
	good := map[string]string{
		"Example.com":                    "example.com",
		"https://www.example.com/path?q": "www.example.com",
		"  blog.example.co.uk  ":         "blog.example.co.uk",
	}
	for in, want := range good {
		got, err := NormalizeHost(in)
		if err != nil || got != want {
			t.Errorf("NormalizeHost(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "localhost", "not a host", "http://", "ex_ample.com"} {
		if _, err := NormalizeHost(in); !errors.Is(err, ErrBadHost) {
			t.Errorf("NormalizeHost(%q) err = %v, want ErrBadHost", in, err)
		}
	}
}

func TestSites(t *testing.T) {
	st := open(t)
	a, err := st.AddSite(ctx, "https://b.example.com/", 100)
	if err != nil {
		t.Fatal(err)
	}
	again, err := st.AddSite(ctx, "b.example.com", 200)
	if err != nil || again.ID != a.ID || again.CreatedAt != 100 {
		t.Fatalf("re-adding returned %+v, %v", again, err)
	}
	if _, err := st.AddSite(ctx, "a.example.com", 300); err != nil {
		t.Fatal(err)
	}
	list, err := st.Sites(ctx)
	if err != nil || len(list) != 2 || list[0].Host != "a.example.com" {
		t.Fatalf("Sites = %+v, %v", list, err)
	}
	if err := st.BindGSC(ctx, a.ID, "me@example.com", "sc-domain:b.example.com"); err != nil {
		t.Fatal(err)
	}
	got, err := st.Site(ctx, a.ID)
	if err != nil || got.GoogleAccount != "me@example.com" || got.GSCProperty != "sc-domain:b.example.com" {
		t.Fatalf("Site = %+v, %v", got, err)
	}
	if err := st.DeleteSite(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Site(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted site: %v", err)
	}
	if err := st.BindGSC(ctx, a.ID, "x", "y"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bind deleted site: %v", err)
	}
}

func TestConnections(t *testing.T) {
	st := open(t)
	for _, acct := range []string{"b@example.com", "a@example.com", "a@example.com"} {
		if err := st.UpsertConnection(ctx, "google", acct, 1); err != nil {
			t.Fatal(err)
		}
	}
	list, err := st.Connections(ctx, "google")
	if err != nil || len(list) != 2 || list[0].Account != "a@example.com" {
		t.Fatalf("Connections = %+v, %v", list, err)
	}
	if err := st.DeleteConnection(ctx, "google", "a@example.com"); err != nil {
		t.Fatal(err)
	}
	list, _ = st.Connections(ctx, "google")
	if len(list) != 1 {
		t.Fatalf("after delete: %+v", list)
	}
}
