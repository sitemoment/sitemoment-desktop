// SPDX-License-Identifier: AGPL-3.0-or-later
package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Window is one hidden browser window (Wails in internal/webview, a fake
// in tests). Methods must be safe to call from any goroutine.
type Window interface {
	Load(url string)
	Inject(js string)
	Show()
	Hide()
}

// Page is one collector run on one page.
type Page struct {
	URL    string
	Hosts  []string
	Script string // the collector body (collector.js)
	Input  map[string]any
	// Timeout bounds the run until the page asks for a human (default 60s);
	// HumanTimeout is how long a human then has (default 2 minutes).
	Timeout, HumanTimeout time.Duration
	// OnHuman is called once when the page asks for a human, with the
	// window it is shown in.
	OnHuman func(window, reason string)
}

var (
	ErrTimeout      = errors.New("collect: the page did not answer in time")
	ErrHumanTimeout = errors.New("collect: nobody completed the check on the page in time")
	ErrClosed       = errors.New("collect: the collector window was closed")
)

// Browser runs collectors in a small pool of windows. It routes every
// message from a window to the run active in it, after checking the run's
// id, nonce and the page's host; the first result ends the run and later
// messages for it are dropped.
type Browser struct {
	New  func(name string) (Window, error)
	Size int // windows at most; default 2
	Logf func(format string, args ...any)

	once    sync.Once
	mu      sync.Mutex
	free    chan *slot
	slots   map[string]*slot
	created int
}

type slot struct {
	name string
	w    Window
	act  *active
}

type active struct {
	run       Run
	script    string
	msgs      chan Message
	closed    chan struct{}
	closeOnce sync.Once
	// ready becomes true only after Load has told the window to navigate
	// to this run's page. Navigated ignores events before that: a window
	// freshly created for the pool, or one just released back to it with
	// Load("about:blank"), can report a late "finished navigating" event
	// for its previous page (the boot page, or about:blank) after this
	// run's act has already been installed in the slot; injecting this
	// run's script there would hand its nonce to the wrong page.
	ready atomic.Bool
}

func (b *Browser) init() {
	b.once.Do(func() {
		if b.Size <= 0 {
			b.Size = 2
		}
		b.free = make(chan *slot, b.Size)
		b.slots = map[string]*slot{}
	})
}

func (b *Browser) logf(format string, args ...any) {
	if b.Logf != nil {
		b.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// acquire returns a free window, creating one while the pool is not full.
func (b *Browser) acquire(ctx context.Context) (*slot, error) {
	b.init()
	select {
	case s := <-b.free:
		return s, nil
	default:
	}
	b.mu.Lock()
	if b.created < b.Size {
		b.created++
		name := fmt.Sprintf("collector-%d", b.created)
		b.mu.Unlock()
		w, err := b.New(name)
		if err != nil {
			b.mu.Lock()
			b.created--
			b.mu.Unlock()
			return nil, err
		}
		s := &slot{name: name, w: w}
		b.mu.Lock()
		b.slots[name] = s
		b.mu.Unlock()
		return s, nil
	}
	b.mu.Unlock()
	select {
	case s := <-b.free:
		return s, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *Browser) release(s *slot) {
	b.mu.Lock()
	s.act = nil
	b.mu.Unlock()
	s.w.Hide()
	s.w.Load("about:blank")
	b.free <- s
}

// Load opens p.URL in a free window and waits for the collector's result.
// p.URL must be https: Browser never asks a window to navigate, and so
// never has a reason to inject a run's script, anywhere else.
func (b *Browser) Load(ctx context.Context, p Page) (json.RawMessage, error) {
	if !strings.HasPrefix(p.URL, "https://") {
		return nil, errors.New("collect: page URL is not https")
	}
	s, err := b.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer b.release(s)
	act := &active{run: NewRun(p.Hosts...), msgs: make(chan Message, 8), closed: make(chan struct{})}
	act.script = Script(act.run, p.Script, p.Input)
	b.mu.Lock()
	s.act = act
	b.mu.Unlock()
	s.w.Load(p.URL)
	act.ready.Store(true)

	timeout, human := p.Timeout, p.HumanTimeout
	if timeout <= 0 {
		timeout = time.Minute
	}
	if human <= 0 {
		human = 2 * time.Minute
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	waiting := false
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-act.closed:
			return nil, ErrClosed
		case <-timer.C:
			if waiting {
				return nil, ErrHumanTimeout
			}
			return nil, ErrTimeout
		case m := <-act.msgs:
			switch m.Kind {
			case "result":
				if len(m.Data) == 0 || string(m.Data) == "null" {
					return nil, errors.New("collect: the collector returned nothing")
				}
				return m.Data, nil
			case "error":
				var e struct {
					Message string `json:"message"`
				}
				json.Unmarshal(m.Data, &e)
				return nil, fmt.Errorf("collect: the collector failed: %s", e.Message)
			case "need_human":
				if waiting {
					continue
				}
				waiting = true
				var h struct {
					Reason string `json:"reason"`
				}
				json.Unmarshal(m.Data, &h)
				s.w.Show()
				if p.OnHuman != nil {
					p.OnHuman(s.name, h.Reason)
				}
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(human)
			case "log":
				b.logf("collector %s: %s", s.name, m.Data)
			}
		}
	}
}

func (b *Browser) activeIn(window string) (*slot, *active) {
	b.init()
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.slots[window]
	if s == nil {
		return nil, nil
	}
	return s, s.act
}

// Navigated injects the active run's script after window finished loading
// a page (also after a redirect or a solved check reloads it). Events for
// a window with no active run (never loaded one, or already released) are
// ignored, as are events that arrive before Load has told this run's
// window to navigate anywhere (see active.ready).
func (b *Browser) Navigated(window string) {
	if s, act := b.activeIn(window); act != nil && act.ready.Load() {
		s.w.Inject(act.script)
	}
}

// Handle takes a raw message from window's page (Wails RawMessageHandler);
// origin is the sending frame's URL.
func (b *Browser) Handle(window, raw, origin string) {
	m, err := Parse(raw)
	if err != nil {
		if !errors.Is(err, ErrNotOurs) {
			b.logf("collector %s: %v", window, err)
		}
		return
	}
	_, act := b.activeIn(window)
	if act == nil {
		return
	}
	if err := Verify(act.run, m, origin); err != nil {
		b.logf("collector %s: dropped a message: %v", window, err)
		return
	}
	select {
	case act.msgs <- m:
	default:
		b.logf("collector %s: too many messages, dropped one", window)
	}
}

// Closed ends the run in window: the user closed it.
func (b *Browser) Closed(window string) {
	if _, act := b.activeIn(window); act != nil {
		act.closeOnce.Do(func() { close(act.closed) })
	}
}

// Reveal shows window again while a run is active in it.
func (b *Browser) Reveal(window string) bool {
	s, act := b.activeIn(window)
	if act == nil {
		return false
	}
	s.w.Show()
	return true
}
