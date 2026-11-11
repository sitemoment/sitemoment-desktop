// SPDX-License-Identifier: AGPL-3.0-or-later
package google

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestCallJSONNamesTheProduct(t *testing.T) {
	denied := errors.New("denied")
	c := caller{http: client(func(*http.Request) (*http.Response, error) { return reply(500, `{"error":{"message":"boom"}}`) }),
		sleep: noSleep, product: "Analytics", forbidden: func(string) error { return denied }}
	err := callJSON(ctx, c, http.MethodGet, "https://example.com/x", "tok", nil, &struct{}{})
	if !errors.Is(err, ErrServerError) || !strings.Contains(err.Error(), "Analytics HTTP 500: boom") {
		t.Fatalf("5xx err = %v", err)
	}
	var seen string
	c.http = client(func(*http.Request) (*http.Response, error) { return reply(403, `{"error":{"message":"no access"}}`) })
	c.forbidden = func(msg string) error { seen = msg; return denied }
	if err := callJSON(ctx, c, http.MethodGet, "https://example.com/x", "tok", nil, &struct{}{}); !errors.Is(err, denied) || seen != "no access" {
		t.Fatalf("403 err = %v, message %q", err, seen)
	}
}
