// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/sitemoment/sitemoment-desktop/collectors"
	"github.com/sitemoment/sitemoment-desktop/internal/collect"
)

// humanLoader asks for a human, then answers.
type humanLoader struct{ revealed []string }

func (h *humanLoader) Load(ctx context.Context, p collect.Page) (json.RawMessage, error) {
	p.OnHuman("collector-1", "captcha")
	return json.RawMessage(`{"organic":[]}`), nil
}

func (h *humanLoader) Reveal(window string) bool {
	h.revealed = append(h.revealed, window)
	return true
}

func TestCollectorService(t *testing.T) {
	st := openStore(t)
	cat := &collect.Catalog{Builtin: collectors.FS, Dir: t.TempDir(), Store: st}
	if err := cat.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	loader := &humanLoader{}
	var needHuman int64
	runner := &collect.Runner{Store: st, Catalog: cat, Browser: loader}
	svc := &CollectorService{Catalog: cat, Runner: runner, Browser: loader}
	runner.Emit = func(name string, data any) {
		if r, ok := data.(collect.RunView); ok && r.Status == "need_human" {
			needHuman = r.ID
			list, err := svc.NeedsHuman(ctx)
			if err != nil || len(list) != 1 || list[0].ID != r.ID {
				t.Errorf("needs human = %+v, %v", list, err)
			}
			if err := svc.Reveal(ctx, r.ID); err != nil {
				t.Errorf("reveal: %v", err)
			}
		}
	}
	list, err := svc.List(ctx)
	if err != nil || len(list) != 2 || list[0].ID != "google-serp" || list[0].Status != collect.StatusEnabled {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if _, err := runner.Run(ctx, "google-serp", map[string]any{"q": "thumbnail maker"}); err != nil {
		t.Fatal(err)
	}
	if needHuman == 0 || len(loader.revealed) != 1 || loader.revealed[0] != "collector-1" {
		t.Fatalf("need human %d, revealed %v", needHuman, loader.revealed)
	}
	if err := svc.Reveal(ctx, needHuman); !errors.Is(err, ErrRunOver) {
		t.Fatalf("reveal after the run: %v", err)
	}
	runs, err := svc.Runs(ctx, 10)
	if err != nil || len(runs) != 1 || runs[0].Status != "ok" || runs[0].Input != `{"gl":"us","hl":"en","q":"thumbnail maker"}` {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	s, err := svc.Stats(ctx)
	if err != nil || s.Today[0].Count != 1 || s.Today[0].Limit != 200 {
		t.Fatalf("stats = %+v, %v", s, err)
	}
	if _, err := svc.Approve(ctx, "google-serp"); err == nil {
		t.Fatal("approved a built-in")
	}
	if dir, _ := svc.Dir(ctx); dir == "" {
		t.Fatal("no collector dir")
	}
}
