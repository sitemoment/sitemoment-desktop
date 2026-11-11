// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/google"
	"github.com/sitemoment/sitemoment-desktop/internal/secret"
)

// TestGoogleConnectAuthURLAndDenied: the auth URL goes to the main window before
// the wait, so the user can copy it into a browser that works.
func TestGoogleConnectAuthURLAndDenied(t *testing.T) {
	st, sec := openStore(t), secret.NewMemory()
	oauth := &google.OAuth{ClientID: "cid", TokenURL: "http://127.0.0.1:1/token"}
	var emitted []string
	g := &GoogleService{OAuth: oauth, Store: st, Secrets: sec,
		Emit: func(name string, data any) {
			if name == "google:auth-url" {
				emitted = append(emitted, data.(string))
			}
		},
		// The user says no on Google's consent page.
		Open: func(raw string) error {
			u, _ := url.Parse(raw)
			q := u.Query()
			go func() {
				res, err := http.Get(q.Get("redirect_uri") + "?" + url.Values{"error": {"access_denied"}, "state": {q.Get("state")}}.Encode())
				if err == nil {
					res.Body.Close()
				}
			}()
			return nil
		}}
	_, err := g.Connect(ctx)
	if err != ErrGoogleAccessDenied {
		t.Fatalf("Connect error = %v", err)
	}
	if len(emitted) != 1 || !strings.Contains(emitted[0], "client_id=cid") {
		t.Fatalf("emitted %v", emitted)
	}
	if strings.Contains(err.Error(), "access_denied") {
		t.Fatal("message would be rewritten by the frontend's errText")
	}
}

func TestGoogleConnectTimeout(t *testing.T) {
	st, sec := openStore(t), secret.NewMemory()
	g := &GoogleService{OAuth: &google.OAuth{ClientID: "cid"}, Store: st, Secrets: sec,
		Open: func(string) error { return nil }, Timeout: 50 * time.Millisecond}
	if _, err := g.Connect(ctx); err != ErrGoogleAuthTimeout {
		t.Fatalf("Connect error = %v", err)
	}
	if strings.Contains(ErrGoogleAuthTimeout.Error(), "timeout") {
		t.Fatal("message would be rewritten by the frontend's errText")
	}
}
