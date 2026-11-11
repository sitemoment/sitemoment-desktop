// SPDX-License-Identifier: AGPL-3.0-or-later
package netproxy

import (
	"net"
	"strings"
)

// parseWindows turns the WinINet values under HKCU\Software\Microsoft\
// Windows\CurrentVersion\Internet Settings into a System. ProxyServer is
// either one "host:port" for HTTP and HTTPS, or "http=…;https=…;socks=…".
// ProxyOverride is a ;-separated exception list ("<local>" = dotless names).
func parseWindows(enable uint64, server, override, autoConfigURL string) System {
	var s System
	if enable != 0 {
		server = strings.TrimSpace(server)
		if strings.Contains(server, "=") {
			for _, part := range strings.Split(server, ";") {
				k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
				if !ok {
					continue
				}
				switch strings.ToLower(strings.TrimSpace(k)) {
				case "http":
					s.HTTP = winAddr(v, "80")
				case "https":
					s.HTTPS = winAddr(v, "80")
				case "socks":
					s.SOCKS = winAddr(v, "1080")
				}
			}
		} else if server != "" {
			s.HTTP = winAddr(server, "80")
			s.HTTPS = s.HTTP
		}
	}
	for _, e := range strings.Split(override, ";") {
		if e = strings.TrimSpace(e); e != "" {
			s.Exceptions = append(s.Exceptions, e)
		}
	}
	s.PAC = strings.TrimSpace(autoConfigURL)
	return s
}

// winAddr strips an optional scheme from a ProxyServer entry and adds the
// default port.
func winAddr(v, def string) string {
	v = strings.TrimSpace(v)
	if _, rest, ok := strings.Cut(v, "://"); ok {
		v = rest
	}
	v = strings.TrimSuffix(v, "/")
	if v == "" {
		return ""
	}
	if h, p, err := net.SplitHostPort(v); err == nil {
		return hostPort(h, p, def)
	}
	return hostPort(v, "", def)
}
