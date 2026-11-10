// SPDX-License-Identifier: AGPL-3.0-or-later

// Package collect runs collectors: scripts injected into web pages that send
// structured results back to Go over the WebView's native message channel.
// Any page can post to that channel, so every message must carry the run id
// and nonce Go handed to the script, and come from a host the collector
// declared.
package collect

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

const prefix = "smc:"

// Message is what a collector script posts: "smc:" + JSON.
type Message struct {
	Run   string          `json:"run"`
	Nonce string          `json:"nonce"`
	Kind  string          `json:"kind"` // result | need_human | error | log
	Data  json.RawMessage `json:"data,omitempty"`
}

// Run is one execution of a collector on one page.
type Run struct {
	ID    string
	Nonce string
	Hosts []string // exact hosts the collector may read, e.g. "www.google.com"
}

var (
	ErrNotOurs   = errors.New("collect: not a collector message")
	ErrBadRun    = errors.New("collect: unknown run or nonce")
	ErrBadOrigin = errors.New("collect: message from a host the collector may not read")
)

var kinds = map[string]bool{"result": true, "need_human": true, "error": true, "log": true}

// NewRun returns a run with a random id and nonce.
func NewRun(hosts ...string) Run {
	return Run{ID: random(9), Nonce: random(24), Hosts: hosts}
}

func random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Parse decodes a raw WebView message. Messages without the "smc:" prefix
// return ErrNotOurs so callers can ignore them quietly.
func Parse(raw string) (Message, error) {
	body, ok := strings.CutPrefix(raw, prefix)
	if !ok {
		return Message{}, ErrNotOurs
	}
	var m Message
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return Message{}, errors.New("collect: message is not JSON")
	}
	if !kinds[m.Kind] {
		return Message{}, errors.New("collect: unknown message kind")
	}
	return m, nil
}

// Verify checks that m belongs to run and was posted by a page on one of the
// run's hosts. origin is the URL the WebView reports for the sending frame
// (a full URL on macOS and Windows).
func Verify(run Run, m Message, origin string) error {
	if m.Run != run.ID || subtle.ConstantTimeCompare([]byte(m.Nonce), []byte(run.Nonce)) != 1 {
		return ErrBadRun
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" {
		return ErrBadOrigin
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range run.Hosts {
		if host == h {
			return nil
		}
	}
	return ErrBadOrigin
}
