// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"net/url"
	"testing"
)

func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"example.com", "https://example.com/", true},
		{"  Example.COM/Path?q=1#frag ", "https://example.com/Path?q=1", true},
		{"http://example.com:80/a", "http://example.com/a", true},
		{"https://example.com:443", "https://example.com/", true},
		{"HTTPS://example.com.", "https://example.com/", true},
		{"//example.com/x", "https://example.com/x", true},
		{"http://[2606:4700::1]/", "http://[2606:4700::1]/", true},
		{"https://example.com:80/", "https://example.com:80/", true},
		{"", "", false},
		{"ftp://example.com/", "", false},
		{"file:///etc/passwd", "", false},
		{"javascript:alert(1)", "", false},
		{"gopher://example.com", "", false},
		{"https://user:pass@example.com/", "", false},
		{"https://user@example.com/", "", false},
		{"https://example.com:8080/", "", false},
		{"https://example.com:22/", "", false},
		{"https://example.com:0/", "", false},
		{"https://example.com:99999/", "", false},
		{"https://example.com:abc/", "", false},
		{"https://example.com:/", "", false},
		{"https:///nohost", "", false},
		{"https://exa mple.com/", "", false},
	}
	for _, c := range cases {
		got, err := NormalizeURL(c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("NormalizeURL(%q) = %q, %v; want %q ok=%v", c.in, got, err, c.want, c.ok)
		}
	}
}

func TestNormalizeURLAnyPort(t *testing.T) {
	got, err := normalizeURL("http://127.0.0.1:8080/x", true)
	if err != nil || got != "http://127.0.0.1:8080/x" {
		t.Fatalf("anyPort: %q %v", got, err)
	}
}

func mustParse(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
