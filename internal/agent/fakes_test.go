// SPDX-License-Identifier: AGPL-3.0-or-later
package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/cloud"
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

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// fakeCollectors offers the Google results collector.
type fakeCollectors struct {
	mu    sync.Mutex
	calls []map[string]any
	err   error
}

func (f *fakeCollectors) Tools() []CollectorTool {
	return []CollectorTool{{ID: "google-serp", Tool: "google_serp", Name: "谷歌搜索结果", Description: "谷歌第一页。",
		Params: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`)}}
}

func (f *fakeCollectors) Run(_ context.Context, id string, input map[string]any) (CollectorResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, input)
	f.mu.Unlock()
	if f.err != nil {
		return CollectorResult{}, f.err
	}
	return CollectorResult{FetchedAt: time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC).Unix(),
		Data: json.RawMessage(`{"location":"United States","organic":[{"rank":1,"domain":"kapwing.com","title":"Kapwing"}],"related":["ai thumbnail"],"paa":["Is it free?"]}`)}, nil
}

// fakeCloud is SiteMoment's data API: every uncached call quotes `quote`
// credits and charges `charge`.
type fakeCloud struct {
	mu       sync.Mutex
	signedIn bool
	quote    float64
	quoteErr error
	charge   float64
	data     []string
	err      error
}

func (f *fakeCloud) SignedIn() bool { return f.signedIn }

func (f *fakeCloud) Catalog(context.Context) ([]cloud.Tool, error) {
	return []cloud.Tool{{Name: "lookup_keywords", Label: "关键词库存数据", Enabled: true}, {Name: "google_serp", Label: "谷歌搜索结果", Enabled: true},
		{Name: "keyword_volume", Label: "搜索量核实", Enabled: false}}, nil
}

func (f *fakeCloud) Quote(_ context.Context, tool string, _ map[string]any) (cloud.Quote, error) {
	if f.quoteErr != nil {
		return cloud.Quote{}, f.quoteErr
	}
	return cloud.Quote{Tool: tool, MaxCredits: f.quote}, nil
}

func (f *fakeCloud) Data(_ context.Context, tool string, _ map[string]any) (cloud.DataResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data = append(f.data, tool)
	if f.err != nil {
		return cloud.DataResult{}, f.err
	}
	return cloud.DataResult{Data: json.RawMessage(`{"items":[{"keyword":"ai chat","volume":1900}]}`), Credits: f.charge, Source: "SiteMoment · 关键词库存数据", FetchedAt: 1}, nil
}

func (f *fakeCloud) dataCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.data)
}
