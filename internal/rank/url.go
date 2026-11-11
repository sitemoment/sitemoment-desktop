// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// NormalizeURL turns user input into an absolute http(s) URL suitable for an
// audit: it adds https:// when no scheme is given, lower-cases the scheme and
// host, drops the fragment and default ports, and uses "/" for an empty path.
// It rejects other schemes, user info, empty hosts and any port other than
// 80 or 443.
func NormalizeURL(raw string) (string, error) {
	return normalizeURL(raw, false)
}

// errBadURL is the reason-free error wrapped in FetchError{Reason: "bad_url"}.
var errBadURL = errors.New("not a valid http(s) URL")

// normalizeURL is NormalizeURL; anyPort lifts the 80/443 rule (tests only).
func normalizeURL(raw string, anyPort bool) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 2048 {
		return "", errBadURL
	}
	if !strings.Contains(s, "://") {
		if strings.HasPrefix(s, "//") {
			s = "https:" + s
		} else {
			s = "https://" + s
		}
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", errBadURL
	}
	if err := checkURL(u, anyPort); err != nil {
		return "", err
	}
	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	u.Host = host
	u.Fragment = ""
	u.RawFragment = ""
	if u.Path == "" {
		u.Path = "/"
		u.RawPath = ""
	}
	return u.String(), nil
}

// checkURL applies the URL rules to a parsed URL; it is used for the first
// request and again for every redirect hop.
func checkURL(u *url.URL, anyPort bool) error {
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return errBadURL
	}
	if u.User != nil || u.Opaque != "" {
		return errBadURL
	}
	host := u.Hostname()
	if host == "" || strings.ContainsAny(host, " \t\r\n%\\") {
		return errBadURL
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return errBadURL
		}
		if !anyPort && n != 80 && n != 443 {
			return errBadURL
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return errBadURL
	}
	return nil
}
