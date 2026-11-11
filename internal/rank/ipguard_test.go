// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"net/netip"
	"testing"
)

func TestIsBlockedAddr(t *testing.T) {
	blocked := []string{
		"0.0.0.0", "0.1.2.3", "10.0.0.1", "10.255.255.255", "100.64.0.1", "100.127.255.254",
		"127.0.0.1", "127.8.9.10", "169.254.0.1", "169.254.169.254", "172.16.0.1", "172.31.255.255",
		"192.0.0.8", "192.0.2.1", "192.168.1.1", "198.18.0.1", "198.51.100.7", "203.0.113.9",
		"224.0.0.1", "239.255.255.250", "240.0.0.1", "255.255.255.255",
		"::", "::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254", "::127.0.0.1",
		"64:ff9b::7f00:1", "100::1", "2001:db8::1", "2001::1", "2002:7f00:1::1",
		"fc00::1", "fd12:3456::1", "fe80::1", "fec0::1", "ff02::1", "fe80::1%en0",
	}
	for _, s := range blocked {
		if !isBlockedAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	allowed := []string{
		"1.1.1.1", "8.8.8.8", "93.184.216.34", "172.15.255.255", "172.32.0.1", "100.63.255.255",
		"100.128.0.1", "169.253.1.1", "192.169.0.1", "::ffff:8.8.8.8", "2606:4700:4700::1111", "2a00:1450:4001::200e",
	}
	for _, s := range allowed {
		if isBlockedAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
	if !isBlockedAddr(netip.Addr{}) {
		t.Error("the zero Addr should be blocked")
	}
}
