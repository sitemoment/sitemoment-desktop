// SPDX-License-Identifier: AGPL-3.0-or-later
package oauthx

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

const pageStyle = `font:15px -apple-system,sans-serif;background:#0b0e14;color:#e6e9ef;display:grid;place-items:center;height:100vh;margin:0`

// page renders the loopback landing page's message for the given OAuth
// "error" query param ("" means success).
func page(errParam string) string {
	msg := "已完成，可以关闭这个页面，回到 SiteMoment。"
	switch {
	case errParam == "access_denied":
		msg = "已取消，可以关闭这个页面。"
	case errParam != "":
		msg = "登录没有完成，可以关闭这个页面，回到 SiteMoment 重试。"
	}
	return `<!doctype html><meta charset="utf-8"><title>SiteMoment</title>
<body style="` + pageStyle + `">
<p>` + msg + `</p></body>`
}

type result struct{ code, state, err string }

// AuthError is the "error" the authorization server redirected back with
// (RFC 6749 4.1.2.1), e.g. "access_denied".
type AuthError struct{ Code string }

func (e *AuthError) Error() string { return "oauthx: authorization failed: " + e.Code }

// Receiver listens on 127.0.0.1 for one OAuth redirect.
type Receiver struct {
	ln  net.Listener
	srv *http.Server
	ch  chan result

	mu   sync.Mutex
	want *string // set by Wait, under mu, before it blocks; nil until then
}

// Listen starts a receiver on a random local port.
func Listen() (*Receiver, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	r := &Receiver{ln: ln, ch: make(chan result, 1)}
	r.srv = &http.Server{Handler: http.HandlerFunc(r.serve), ReadHeaderTimeout: 10 * time.Second}
	go r.srv.Serve(ln)
	return r, nil
}

func (r *Receiver) serve(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/callback" {
		http.NotFound(w, req)
		return
	}
	q := req.URL.Query()
	state, errParam := q.Get("state"), q.Get("error")

	r.mu.Lock()
	want := r.want
	r.mu.Unlock()
	// Once Wait has registered the state it expects, ignore a callback for
	// a different state unless it carries an error: a stray/forged success
	// for another state must not consume the one result slot, but a
	// genuine cancellation/error should still surface instead of hanging
	// Wait until ctx times out. Callbacks that arrive before Wait is
	// called (want == nil) are buffered as before.
	if want != nil && state != *want && errParam == "" {
		http.NotFound(w, req)
		return
	}

	res := result{code: q.Get("code"), state: state, err: errParam}
	// Write and flush the response before signaling Wait, so the browser
	// already has the page before the caller is free to Close the receiver.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, page(errParam))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	select {
	case r.ch <- res:
	default: // only the first (matching) redirect counts
	}
}

// RedirectURI is the address to register as the OAuth redirect.
func (r *Receiver) RedirectURI() string {
	return fmt.Sprintf("http://127.0.0.1:%d/callback", r.ln.Addr().(*net.TCPAddr).Port)
}

// Wait returns the authorization code once the browser is redirected back
// with the expected state.
func (r *Receiver) Wait(ctx context.Context, state string) (string, error) {
	r.mu.Lock()
	r.want = &state
	r.mu.Unlock()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-r.ch:
		if res.err != "" {
			return "", &AuthError{Code: res.err}
		}
		if subtle.ConstantTimeCompare([]byte(res.state), []byte(state)) != 1 {
			return "", errors.New("oauthx: state mismatch")
		}
		if res.code == "" {
			return "", errors.New("oauthx: no code in redirect")
		}
		return res.code, nil
	}
}

// Close stops the listener.
func (r *Receiver) Close() error { return r.srv.Close() }
