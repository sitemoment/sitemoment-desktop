// SPDX-License-Identifier: AGPL-3.0-or-later
package collect

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// fakeLoader answers every page with a result naming its URL.
type fakeLoader struct {
	mu    sync.Mutex
	pages []Page
	err   error
	human bool
}

func (f *fakeLoader) Load(ctx context.Context, p Page) (json.RawMessage, error) {
	f.mu.Lock()
	f.pages = append(f.pages, p)
	err, human := f.err, f.human
	f.mu.Unlock()
	if human {
		p.OnHuman("collector-1", "captcha")
	}
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(map[string]string{"url": p.URL})
	return raw, nil
}

type runnerEnv struct {
	r      *Runner
	loader *fakeLoader
	now    time.Time
	waits  []time.Duration
	events []string
	mu     sync.Mutex
}

func newRunner(t *testing.T) *runnerEnv {
	st := openStore(t)
	cat := &Catalog{Builtin: builtinFS(), Store: st}
	if err := cat.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	e := &runnerEnv{loader: &fakeLoader{}, now: time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local)}
	e.r = &Runner{Store: st, Catalog: cat, Browser: e.loader,
		Now:  func() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now },
		Rand: func() float64 { return 0.5 },
		Wait: func(_ context.Context, d time.Duration) error {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.waits = append(e.waits, d)
			e.now = e.now.Add(d)
			return nil
		},
		Emit: func(name string, _ any) { e.mu.Lock(); e.events = append(e.events, name); e.mu.Unlock() },
	}
	return e
}

func (e *runnerEnv) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}

func TestRunnerCachesAndSpacesRuns(t *testing.T) {
	e := newRunner(t)
	res, err := e.r.Run(ctx, "builtin-one", map[string]any{"q": "AI chat"})
	if err != nil || res.Cached || !strings.Contains(string(res.Data), "q=AI+chat") || res.RunID == 0 {
		t.Fatalf("first run = %+v, %v", res, err)
	}
	again, err := e.r.Run(ctx, "builtin-one", map[string]any{"q": " AI chat", "gl": "us"})
	if err != nil || !again.Cached || again.RunID != res.RunID || len(e.loader.pages) != 1 {
		t.Fatalf("same input = %+v, %v (pages %d)", again, err, len(e.loader.pages))
	}
	e.advance(2 * time.Second)
	if _, err := e.r.Run(ctx, "builtin-one", map[string]any{"q": "other"}); err != nil {
		t.Fatal(err)
	}
	// minGapSeconds 10, jitterSeconds 0 in the test manifest: 8s more to wait.
	if len(e.waits) != 1 || e.waits[0] != 8*time.Second {
		t.Fatalf("waits = %v", e.waits)
	}
	e.advance(25 * time.Hour)
	if res, _ := e.r.Run(ctx, "builtin-one", map[string]any{"q": "AI chat"}); res.Cached {
		t.Fatal("expired entry served")
	}
	if p := e.loader.pages[0]; p.Timeout != time.Minute || p.HumanTimeout != 2*time.Minute || p.Hosts[0] != "www.example.com" || p.Input["n"] != 10.0 {
		t.Fatalf("page = %+v", p)
	}
	if !strings.Contains(strings.Join(e.events, ","), "collect:run") {
		t.Fatalf("events = %v", e.events)
	}
}

func TestRunnerDailyLimitErrorsAndHuman(t *testing.T) {
	e := newRunner(t)
	if _, err := e.r.Run(ctx, "nope", nil); !errors.Is(err, ErrUnknownCollector) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := e.r.Run(ctx, "builtin-one", map[string]any{}); err == nil {
		t.Fatal("missing q accepted")
	}
	e.loader.err, e.loader.human = ErrHumanTimeout, true
	if _, err := e.r.Run(ctx, "builtin-one", map[string]any{"q": "a"}); !errors.Is(err, ErrHumanTimeout) {
		t.Fatalf("human timeout: %v", err)
	}
	runs, _ := e.r.Runs(ctx, "", 10)
	if len(runs) != 1 || runs[0].Status != store.RunError || runs[0].Error != "页面要求人工验证，2 分钟内没有完成" {
		t.Fatalf("runs = %+v", runs)
	}
	e.loader.err, e.loader.human = nil, false
	for i := 1; i < 50; i++ { // the test manifest allows 50 a day
		e.advance(time.Minute)
		if _, err := e.r.Run(ctx, "builtin-one", map[string]any{"q": strings.Repeat("x", i)}); err != nil {
			t.Fatal(err)
		}
	}
	e.advance(time.Minute)
	if _, err := e.r.Run(ctx, "builtin-one", map[string]any{"q": "one more"}); !errors.Is(err, ErrDailyLimit) {
		t.Fatalf("over the limit: %v", err)
	}
	s, err := e.r.Stats(ctx)
	if err != nil || len(s.Today) != 1 || s.Today[0].Count != 50 || s.Today[0].Limit != 50 || s.Running != 0 {
		t.Fatalf("stats = %+v, %v", s, err)
	}
}

func TestRunnerPostHook(t *testing.T) {
	e := newRunner(t)
	e.r.Post = map[string]PostFunc{"builtin-one": func(_ context.Context, d json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"post":true}`), nil
	}}
	res, err := e.r.Run(ctx, "builtin-one", map[string]any{"q": "a"})
	if err != nil || string(res.Data) != `{"post":true}` {
		t.Fatalf("post = %s, %v", res.Data, err)
	}
	if res, _ := e.r.Run(ctx, "builtin-one", map[string]any{"q": "a"}); string(res.Data) != `{"post":true}` {
		t.Fatalf("cache kept %s", res.Data)
	}
}
