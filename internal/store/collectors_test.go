// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"errors"
	"reflect"
	"testing"
)

func TestCollectorRunsCacheAndCounts(t *testing.T) {
	s := open(t)
	id, err := s.InsertCollectorRun(ctx, "google-serp", "h1", `{"q":"a"}`, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CachedCollectorRun(ctx, "google-serp", "h1", 1001); !errors.Is(err, ErrNotFound) {
		t.Fatalf("running run is not a cache entry: %v", err)
	}
	if err := s.SetCollectorRunStatus(ctx, id, RunNeedHuman); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.CollectorRuns(ctx, RunNeedHuman, 10); len(l) != 1 || l[0].ID != id {
		t.Fatalf("need_human list = %+v", l)
	}
	if err := s.FinishCollectorRun(ctx, id, RunOK, `{"organic":[]}`, "", 1010, 2000); err != nil {
		t.Fatal(err)
	}
	r, err := s.CachedCollectorRun(ctx, "google-serp", "h1", 1999)
	if err != nil || r.ID != id || r.Result != `{"organic":[]}` || r.FinishedAt != 1010 {
		t.Fatalf("cached = %+v, %v", r, err)
	}
	if _, err := s.CachedCollectorRun(ctx, "google-serp", "h1", 2000); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired run served: %v", err)
	}
	if _, err := s.CachedCollectorRun(ctx, "google-serp", "h2", 1500); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other input served: %v", err)
	}
	s.InsertCollectorRun(ctx, "google-serp", "h2", `{}`, 900)
	s.InsertCollectorRun(ctx, "google-trends", "h3", `{}`, 1000)
	if n, err := s.CollectorRunsSince(ctx, "google-serp", 950); err != nil || n != 1 {
		t.Fatalf("since = %d, %v", n, err)
	}
	if l, _ := s.CollectorRuns(ctx, "", 10); len(l) != 3 || l[0].Collector != "google-trends" {
		t.Fatalf("all runs = %+v", l)
	}
	if err := s.CancelUnfinishedCollectorRuns(ctx, 3000); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.CollectorRuns(ctx, RunCancelled, 10); len(l) != 2 {
		t.Fatalf("cancelled = %+v", l)
	}
	if r, _ := s.CollectorRun(ctx, id); r.Status != RunOK {
		t.Fatalf("finished run was cancelled: %+v", r)
	}
}

func TestCollectorGrants(t *testing.T) {
	s := open(t)
	if _, err := s.CollectorGrant(ctx, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing grant: %v", err)
	}
	if err := s.GrantCollector(ctx, "x", "1.0.0", []string{"a.example.com"}, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantCollector(ctx, "x", "1.1.0", []string{"a.example.com", "b.example.com"}, 6); err != nil {
		t.Fatal(err)
	}
	g, err := s.CollectorGrant(ctx, "x")
	if err != nil || g.Version != "1.1.0" || !reflect.DeepEqual(g.Hosts, []string{"a.example.com", "b.example.com"}) || g.GrantedAt != 6 {
		t.Fatalf("grant = %+v, %v", g, err)
	}
}
