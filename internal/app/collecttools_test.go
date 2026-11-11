// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/sitemoment/sitemoment-desktop/collectors"
	"github.com/sitemoment/sitemoment-desktop/internal/agent"
	"github.com/sitemoment/sitemoment-desktop/internal/collect"
)

type fakeRunner struct {
	res collect.Result
	err error
	id  string
}

func (f *fakeRunner) Run(_ context.Context, id string, _ map[string]any) (collect.Result, error) {
	f.id = id
	return f.res, f.err
}

func TestCollectorToolsListsAndRuns(t *testing.T) {
	cat := &collect.Catalog{Builtin: collectors.FS, Store: openStore(t)}
	if err := cat.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	run := &fakeRunner{res: collect.Result{Data: json.RawMessage(`{"organic":[]}`), Cached: true, FetchedAt: 42}}
	ct := &CollectorTools{Catalog: cat, Runner: run}
	tools := map[string]agent.CollectorTool{}
	for _, tl := range ct.Tools() {
		tools[tl.Tool] = tl
	}
	serp, ok := tools["google_serp"]
	if !ok || serp.ID != "google-serp" || serp.Name == "" || serp.Description == "" || !json.Valid(serp.Params) {
		t.Fatalf("tools = %+v", tools)
	}
	if _, ok := tools["google_trends"]; !ok {
		t.Fatalf("no google_trends in %+v", tools)
	}
	r, err := ct.Run(ctx, "google-serp", map[string]any{"q": "x"})
	if err != nil || run.id != "google-serp" || !r.Cached || r.FetchedAt != 42 || string(r.Data) != `{"organic":[]}` {
		t.Fatalf("run = %+v, %v", r, err)
	}
}

func TestCollectorToolsWrapsErrors(t *testing.T) {
	for _, c := range []struct {
		err  error
		want error
	}{
		{collect.ErrDailyLimit, agent.ErrCollectorLimit},
		{collect.ErrHumanTimeout, agent.ErrCollectorHuman},
		{collect.ErrClosed, agent.ErrCollectorHuman},
		{context.Canceled, context.Canceled},
		{collect.ErrTimeout, collect.ErrTimeout},
	} {
		ct := &CollectorTools{Runner: &fakeRunner{err: c.err}}
		if _, err := ct.Run(ctx, "google-serp", nil); !errors.Is(err, c.want) {
			t.Errorf("%v → %v, want %v", c.err, err, c.want)
		}
	}
}
