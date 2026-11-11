// SPDX-License-Identifier: AGPL-3.0-or-later
package netproxy

import (
	"reflect"
	"testing"
)

func TestParseWindows(t *testing.T) {
	cases := []struct {
		name                     string
		enable                   uint64
		server, override, pacURL string
		want                     System
	}{
		{"single server for http and https", 1, "127.0.0.1:7890", "localhost;127.*;10.*;<local>", "",
			System{HTTP: "127.0.0.1:7890", HTTPS: "127.0.0.1:7890", Exceptions: []string{"localhost", "127.*", "10.*", "<local>"}}},
		{"per protocol", 1, "http=127.0.0.1:7890;https=127.0.0.1:7891;socks=127.0.0.1:7892;ftp=1.2.3.4:21", "", "",
			System{HTTP: "127.0.0.1:7890", HTTPS: "127.0.0.1:7891", SOCKS: "127.0.0.1:7892"}},
		{"scheme prefixes and spaces", 1, " http=http://127.0.0.1:7890 ; https=127.0.0.1:7890 ", " *.corp.example ; ", "",
			System{HTTP: "127.0.0.1:7890", HTTPS: "127.0.0.1:7890", Exceptions: []string{"*.corp.example"}}},
		{"socks only", 1, "socks=127.0.0.1:1080", "", "", System{SOCKS: "127.0.0.1:1080"}},
		{"disabled", 0, "127.0.0.1:7890", "<local>", "", System{Exceptions: []string{"<local>"}}},
		{"pac", 0, "", "", "http://127.0.0.1:33331/pac", System{PAC: "http://127.0.0.1:33331/pac"}},
		{"server without port", 1, "proxy.corp", "", "", System{HTTP: "proxy.corp:80", HTTPS: "proxy.corp:80"}},
	}
	for _, c := range cases {
		got := parseWindows(c.enable, c.server, c.override, c.pacURL)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}
