// SPDX-License-Identifier: AGPL-3.0-or-later
package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"testing"
	"time"
)

// fakeWindow records what the Browser asks of a window.
type fakeWindow struct {
	name  string
	mu    sync.Mutex
	loads chan string
	js    []string
	shown int
}

func (f *fakeWindow) Load(url string) { f.loads <- url }
func (f *fakeWindow) Inject(js string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.js = append(f.js, js)
}
func (f *fakeWindow) Show() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shown++
}
func (f *fakeWindow) Hide() {}

func (f *fakeWindow) lastJS() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.js) == 0 {
		return ""
	}
	return f.js[len(f.js)-1]
}

var runVars = regexp.MustCompile(`var run = ("[^"]*"), nonce = ("[^"]*")`)

// msg builds a raw collector message for the run injected last into w.
func msg(t *testing.T, w *fakeWindow, kind, data string) string {
	t.Helper()
	m := runVars.FindStringSubmatch(w.lastJS())
	if m == nil {
		t.Fatal("no script injected")
	}
	var run, nonce string
	json.Unmarshal([]byte(m[1]), &run)
	json.Unmarshal([]byte(m[2]), &nonce)
	return fmt.Sprintf(`smc:{"run":%q,"nonce":%q,"kind":%q,"data":%s}`, run, nonce, kind, data)
}

type harness struct {
	b       *Browser
	mu      sync.Mutex
	windows map[string]*fakeWindow
	news    int
}

func newHarness(size int) *harness {
	h := &harness{windows: map[string]*fakeWindow{}}
	h.b = &Browser{Size: size, Logf: func(string, ...any) {}, New: func(name string) (Window, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.news++
		w := &fakeWindow{name: name, loads: make(chan string, 16)}
		h.windows[name] = w
		return w, nil
	}}
	return h
}

func (h *harness) window(name string) *fakeWindow {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.windows[name]
}

type loadResult struct {
	data json.RawMessage
	err  error
}

func (h *harness) start(p Page) chan loadResult {
	ch := make(chan loadResult, 1)
	go func() {
		d, err := h.b.Load(context.Background(), p)
		ch <- loadResult{d, err}
	}()
	return ch
}

// waitLoad waits for window name to be asked to load a URL and returns it.
func (h *harness) waitLoad(t *testing.T, name string) (*fakeWindow, string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w := h.window(name); w != nil {
			select {
			case u := <-w.loads:
				return w, u
			case <-time.After(2 * time.Second):
				t.Fatalf("%s loaded nothing", name)
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("window %s never created", name)
	return nil, ""
}

const origin = "https://www.google.com/search?q=x"

var page = Page{URL: "https://www.google.com/search?q=x", Hosts: []string{"www.google.com"}, Script: "sm.done({});", Timeout: 2 * time.Second}

func TestBrowserResult(t *testing.T) {
	h := newHarness(2)
	res := h.start(page)
	w, u := h.waitLoad(t, "collector-1")
	if u != page.URL {
		t.Fatalf("loaded %q", u)
	}
	h.b.Navigated("collector-1")
	h.b.Handle("main", msg(t, w, "result", `{"x":1}`), origin)                         // another window
	h.b.Handle("collector-1", "wails:runtime:ready", origin)                           // not ours
	h.b.Handle("collector-1", msg(t, w, "result", `{"x":2}`), "https://evil.example/") // wrong host
	h.b.Handle("collector-1", msg(t, w, "result", `{"x":3}`), origin)
	r := <-res
	if r.err != nil || string(r.data) != `{"x":3}` {
		t.Fatalf("Load = %s, %v", r.data, r.err)
	}
	if _, u := h.waitLoad(t, "collector-1"); u != "about:blank" {
		t.Fatalf("released window loaded %q", u)
	}
	// A late message of the finished run reaches nobody.
	late := msg(t, w, "result", `{"x":4}`)
	res = h.start(page)
	h.waitLoad(t, "collector-1")
	h.b.Handle("collector-1", late, origin)
	h.b.Navigated("collector-1")
	h.b.Handle("collector-1", msg(t, w, "result", `{"x":5}`), origin)
	if r := <-res; string(r.data) != `{"x":5}` {
		t.Fatalf("second run = %s, %v", r.data, r.err)
	}
	if h.news != 1 {
		t.Fatalf("created %d windows for sequential runs", h.news)
	}
}

func TestBrowserNeedHuman(t *testing.T) {
	h := newHarness(2)
	var human []string
	p := page
	p.HumanTimeout = 2 * time.Second
	p.OnHuman = func(window, reason string) { human = append(human, window+":"+reason) }
	res := h.start(p)
	w, _ := h.waitLoad(t, "collector-1")
	h.b.Navigated("collector-1")
	h.b.Handle("collector-1", msg(t, w, "need_human", `{"reason":"captcha"}`), origin)
	h.b.Handle("collector-1", msg(t, w, "need_human", `{"reason":"captcha"}`), origin)
	time.Sleep(50 * time.Millisecond)
	if !h.b.Reveal("collector-1") {
		t.Fatal("Reveal found no run")
	}
	// The human solves it; the page reloads and the script runs again.
	h.b.Navigated("collector-1")
	h.b.Handle("collector-1", msg(t, w, "result", `{"ok":true}`), origin)
	r := <-res
	if r.err != nil || len(human) != 1 || human[0] != "collector-1:captcha" || w.shown != 2 {
		t.Fatalf("result %s %v, human %v, shown %d", r.data, r.err, human, w.shown)
	}
	if h.b.Reveal("collector-1") {
		t.Fatal("Reveal after the run ended")
	}
}

func TestBrowserTimeoutsErrorsAndClose(t *testing.T) {
	h := newHarness(1)
	p := page
	p.Timeout = 50 * time.Millisecond
	if _, err := h.b.Load(context.Background(), p); !errors.Is(err, ErrTimeout) {
		t.Fatalf("timeout: %v", err)
	}
	h.waitLoad(t, "collector-1") // the page
	h.waitLoad(t, "collector-1") // about:blank
	p.HumanTimeout = 50 * time.Millisecond
	p.Timeout = 2 * time.Second
	res := h.start(p)
	w, _ := h.waitLoad(t, "collector-1")
	h.b.Navigated("collector-1")
	h.b.Handle("collector-1", msg(t, w, "need_human", `{"reason":"captcha"}`), origin)
	if r := <-res; !errors.Is(r.err, ErrHumanTimeout) {
		t.Fatalf("human timeout: %v", r.err)
	}
	h.waitLoad(t, "collector-1") // about:blank
	res = h.start(p)
	h.waitLoad(t, "collector-1")
	h.b.Navigated("collector-1")
	h.b.Handle("collector-1", msg(t, w, "error", `{"message":"boom"}`), origin)
	if r := <-res; r.err == nil || r.err.Error() != "collect: the collector failed: boom" {
		t.Fatalf("error: %v", r.err)
	}
	h.waitLoad(t, "collector-1")
	res = h.start(p)
	h.waitLoad(t, "collector-1")
	h.b.Closed("collector-1")
	if r := <-res; !errors.Is(r.err, ErrClosed) {
		t.Fatalf("closed: %v", r.err)
	}
}

func TestBrowserPoolWaits(t *testing.T) {
	h := newHarness(1)
	first := h.start(page)
	w, _ := h.waitLoad(t, "collector-1")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := h.b.Load(ctx, page); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second load with a busy pool: %v", err)
	}
	h.b.Navigated("collector-1")
	h.b.Handle("collector-1", msg(t, w, "result", `{}`), origin)
	if r := <-first; r.err != nil {
		t.Fatal(r.err)
	}
	if h.news != 1 {
		t.Fatalf("pool of 1 created %d windows", h.news)
	}
}
