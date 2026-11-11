// SPDX-License-Identifier: AGPL-3.0-or-later
package netproxy

import (
	"reflect"
	"testing"
)

// clashScutil is `scutil --proxy` with ClashX's "set as system proxy" on,
// including a per-interface __SCOPED__ dictionary whose keys must not
// override the top-level ones.
const clashScutil = `<dictionary> {
  ExceptionsList : <array> {
    0 : localhost
    1 : 127.0.0.1
    2 : ::1
    3 : *.local
    4 : <local>
    5 : 169.254/16
    6 : 10.0.0.0/8
  }
  ExcludeSimpleHostnames : 1
  FTPPassive : 1
  HTTPEnable : 1
  HTTPPort : 7890
  HTTPProxy : 127.0.0.1
  HTTPSEnable : 1
  HTTPSPort : 7890
  HTTPSProxy : 127.0.0.1
  ProxyAutoConfigEnable : 0
  SOCKSEnable : 1
  SOCKSPort : 7891
  SOCKSProxy : 127.0.0.1
  __SCOPED__ : <dictionary> {
    en0 : <dictionary> {
      HTTPEnable : 1
      HTTPPort : 9999
      HTTPProxy : 10.9.9.9
    }
  }
}
`

const pacScutil = `<dictionary> {
  ExceptionsList : <array> {
    0 : *.local
    1 : 169.254/16
  }
  FTPPassive : 1
  HTTPEnable : 0
  HTTPSEnable : 0
  ProxyAutoConfigEnable : 1
  ProxyAutoConfigURLString : http://127.0.0.1:33331/proxy.pac
  SOCKSEnable : 0
}
`

const offScutil = `<dictionary> {
  ExceptionsList : <array> {
    0 : *.local
    1 : 169.254/16
  }
  FTPPassive : 1
  HTTPEnable : 0
  HTTPSEnable : 0
  ProxyAutoConfigEnable : 0
  SOCKSEnable : 0
}
`

// socksOnlyScutil has a SOCKS proxy only, and an HTTP proxy host that is
// configured but switched off.
const socksOnlyScutil = `<dictionary> {
  HTTPEnable : 0
  HTTPPort : 8080
  HTTPProxy : 127.0.0.1
  SOCKSEnable : 1
  SOCKSProxy : 127.0.0.1
}
`

func TestParseScutil(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want System
	}{
		{"clash", clashScutil, System{
			HTTP: "127.0.0.1:7890", HTTPS: "127.0.0.1:7890", SOCKS: "127.0.0.1:7891",
			Exceptions:    []string{"localhost", "127.0.0.1", "::1", "*.local", "<local>", "169.254/16", "10.0.0.0/8"},
			ExcludeSimple: true,
		}},
		{"pac", pacScutil, System{Exceptions: []string{"*.local", "169.254/16"}, PAC: "http://127.0.0.1:33331/proxy.pac"}},
		{"off", offScutil, System{Exceptions: []string{"*.local", "169.254/16"}}},
		{"socks only, default port", socksOnlyScutil, System{SOCKS: "127.0.0.1:1080"}},
		{"empty", "", System{}},
		{"garbage", "scutil: no such thing\n", System{}},
	}
	for _, c := range cases {
		got := parseScutil(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}

func TestParseScutilIPv6Proxy(t *testing.T) {
	got := parseScutil("<dictionary> {\n  HTTPSEnable : 1\n  HTTPSProxy : ::1\n  HTTPSPort : 7890\n}\n")
	if got.HTTPS != "[::1]:7890" {
		t.Fatalf("HTTPS = %q", got.HTTPS)
	}
}
