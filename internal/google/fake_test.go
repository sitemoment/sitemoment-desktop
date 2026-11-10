// SPDX-License-Identifier: AGPL-3.0-or-later
package google

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
)

var ctx = context.Background()

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func client(f func(*http.Request) (*http.Response, error)) *http.Client {
	return &http.Client{Transport: roundTrip(f)}
}

func reply(code int, body string) (*http.Response, error) {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)),
		Header: http.Header{"Content-Type": {"application/json"}}}, nil
}

func idToken(email string) string {
	return "h." + base64.RawURLEncoding.EncodeToString([]byte(`{"email":"`+email+`"}`)) + ".s"
}
