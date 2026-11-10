// SPDX-License-Identifier: AGPL-3.0-or-later
package oauthx

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

const donePage = `<!doctype html><meta charset="utf-8"><title>SiteMoment</title>
<body style="font:15px -apple-system,sans-serif;background:#0b0e14;color:#e6e9ef;display:grid;place-items:center;height:100vh;margin:0">
<p>已完成，可以关闭这个页面，回到 SiteMoment。</p></body>`

type result struct{ code, state, err string }

// Receiver listens on 127.0.0.1 for one OAuth redirect.
type Receiver struct {
	ln  net.Listener
	srv *http.Server
	ch  chan result
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
	res := result{code: q.Get("code"), state: q.Get("state"), err: q.Get("error")}
	// Write and flush the response before signaling Wait, so the browser
	// already has the page before the caller is free to Close the receiver.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, donePage)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	select {
	case r.ch <- res:
	default: // only the first redirect counts
	}
}

// RedirectURI is the address to register as the OAuth redirect.
func (r *Receiver) RedirectURI() string {
	return fmt.Sprintf("http://127.0.0.1:%d/callback", r.ln.Addr().(*net.TCPAddr).Port)
}

// Wait returns the authorization code once the browser is redirected back
// with the expected state.
func (r *Receiver) Wait(ctx context.Context, state string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-r.ch:
		if res.err != "" {
			return "", fmt.Errorf("oauthx: authorization failed: %s", res.err)
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
