// SPDX-License-Identifier: AGPL-3.0-or-later
package oauthx

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPKCE(t *testing.T) {
	v, c := NewPKCE()
	if len(v) != 43 || len(c) != 43 {
		t.Fatalf("lengths %d %d", len(v), len(c))
	}
	// RFC 7636 appendix B
	if got := Challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("Challenge = %s", got)
	}
	if a, b := RandomState(), RandomState(); a == b || len(a) < 20 {
		t.Fatalf("states %q %q", a, b)
	}
}

func get(t *testing.T, u string) {
	t.Helper()
	res, err := http.Get(u)
	if err != nil {
		t.Errorf("get %s: %v", u, err)
		return
	}
	res.Body.Close()
}

func TestReceiverCode(t *testing.T) {
	r, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if !strings.HasPrefix(r.RedirectURI(), "http://127.0.0.1:") || !strings.HasSuffix(r.RedirectURI(), "/callback") {
		t.Fatalf("RedirectURI = %s", r.RedirectURI())
	}
	go get(t, r.RedirectURI()+"?code=abc&state=s1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	code, err := r.Wait(ctx, "s1")
	if err != nil || code != "abc" {
		t.Fatalf("Wait = %q, %v", code, err)
	}
}

func TestReceiverRejects(t *testing.T) {
	for _, q := range []string{"?code=abc&state=other", "?error=access_denied&state=s1", "?state=s1"} {
		r, err := Listen()
		if err != nil {
			t.Fatal(err)
		}
		go get(t, r.RedirectURI()+q)
		// "?code=abc&state=other" (wrong state, no error) is now silently
		// ignored once Wait has registered "s1" as the expected state, so
		// that case only ever fails via the ctx deadline; keep it short.
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		if _, err := r.Wait(ctx, "s1"); err == nil {
			t.Errorf("%s: accepted", q)
		}
		cancel()
		r.Close()
	}
}

// TestReceiverIgnoresStrayState checks that once Wait has registered the
// state it expects, a callback for some other state (no error) is ignored
// (404s, not consumed) rather than taking the one result slot, so the
// genuine callback that follows still gets through.
func TestReceiverIgnoresStrayState(t *testing.T) {
	r, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	type waitResult struct {
		code string
		err  error
	}
	resCh := make(chan waitResult, 1)
	go func() {
		wctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		code, err := r.Wait(wctx, "right")
		resCh <- waitResult{code, err}
	}()
	time.Sleep(20 * time.Millisecond) // let Wait register "right" as the expected state

	get(t, r.RedirectURI()+"?code=wrong-code&state=bad")
	time.Sleep(20 * time.Millisecond)
	get(t, r.RedirectURI()+"?code=right-code&state=right")

	select {
	case res := <-resCh:
		if res.err != nil || res.code != "right-code" {
			t.Fatalf("Wait = %q, %v, want %q, nil", res.code, res.err, "right-code")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return")
	}
}

func TestReceiverTimeout(t *testing.T) {
	r, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := r.Wait(ctx, "s1"); err == nil {
		t.Fatal("no timeout")
	}
}
