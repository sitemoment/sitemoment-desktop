// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import "net/netip"

// blockedPrefixes are the ranges a server-side fetch must never reach, in
// addition to what the netip predicates in isBlockedAddr already cover.
var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8",       // "this network"
		"10.0.0.0/8",      // private
		"100.64.0.0/10",   // CGNAT
		"127.0.0.0/8",     // loopback
		"169.254.0.0/16",  // link-local, incl. cloud metadata 169.254.169.254
		"172.16.0.0/12",   // private
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // TEST-NET-1
		"192.88.99.0/24",  // 6to4 relay anycast
		"192.168.0.0/16",  // private
		// 198.18.0.0/15 (benchmarking) is deliberately allowed: proxy tools in
		// TUN mode with fake-ip DNS (Clash, Surge) answer every lookup from it.
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"224.0.0.0/4",     // multicast
		"240.0.0.0/4",     // reserved, incl. broadcast 255.255.255.255
		"::/96",           // unspecified, loopback, IPv4-compatible
		"64:ff9b::/96",    // NAT64 (embeds an IPv4 address)
		"64:ff9b:1::/48",  // local-use NAT64
		"100::/64",        // discard
		"2001::/23",       // IETF protocol assignments, incl. Teredo
		"2001:db8::/32",   // documentation
		"2002::/16",       // 6to4 (embeds an IPv4 address)
		"fc00::/7",        // unique local
		"fe80::/10",       // link-local
		"fec0::/10",       // site-local (deprecated)
		"ff00::/8",        // multicast
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// isBlockedAddr reports whether a is an address the fetcher refuses to
// connect to: anything that is not plain global unicast (private, loopback,
// link-local, CGNAT, multicast, broadcast, reserved, documentation and the
// IPv6 forms that embed IPv4). IPv4-mapped IPv6 (::ffff:a.b.c.d) is judged
// as the IPv4 address it maps.
func isBlockedAddr(a netip.Addr) bool {
	if !a.IsValid() {
		return true
	}
	a = a.Unmap()
	if a.Zone() != "" {
		return true
	}
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsMulticast() || a.IsUnspecified() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
