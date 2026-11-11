// SPDX-License-Identifier: AGPL-3.0-or-later
package netproxy

import "strings"

// parseScutil reads the output of macOS `scutil --proxy`. Only the
// top-level dictionary counts: __SCOPED__ holds per-interface copies.
// Anything it does not understand is ignored (an empty System is "no
// proxy").
func parseScutil(out string) System {
	kv := map[string]string{}
	var exceptions []string
	depth := 0
	inExceptions := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "}" {
			depth--
			if depth <= 1 {
				inExceptions = false
			}
			continue
		}
		key, val, ok := strings.Cut(line, " : ")
		if strings.HasSuffix(line, "{") {
			if ok && depth == 1 && strings.TrimSpace(key) == "ExceptionsList" {
				inExceptions = true
			}
			depth++
			continue
		}
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch {
		case depth == 1:
			kv[key] = val
		case depth == 2 && inExceptions:
			exceptions = append(exceptions, val)
		}
	}
	on := func(k string) bool { return kv[k] == "1" }
	var s System
	if on("HTTPEnable") {
		s.HTTP = hostPort(kv["HTTPProxy"], kv["HTTPPort"], "80")
	}
	if on("HTTPSEnable") {
		s.HTTPS = hostPort(kv["HTTPSProxy"], kv["HTTPSPort"], "443")
	}
	if on("SOCKSEnable") {
		s.SOCKS = hostPort(kv["SOCKSProxy"], kv["SOCKSPort"], "1080")
	}
	if on("ProxyAutoConfigEnable") {
		s.PAC = kv["ProxyAutoConfigURLString"]
		if s.PAC == "" {
			s.PAC = "PAC"
		}
	} else if on("ProxyAutoDiscoveryEnable") {
		s.PAC = "WPAD"
	}
	s.Exceptions = exceptions
	s.ExcludeSimple = on("ExcludeSimpleHostnames")
	return s
}
