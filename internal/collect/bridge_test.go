// SPDX-License-Identifier: AGPL-3.0-or-later
package collect

import (
	"errors"
	"testing"
)

func TestParseAndVerify(t *testing.T) {
	run := Run{ID: "r1", Nonce: "n1", Hosts: []string{"www.google.com"}}
	raw := `smc:{"run":"r1","nonce":"n1","kind":"result","data":{"organic":[]}}`

	m, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m.Kind != "result" || string(m.Data) != `{"organic":[]}` {
		t.Fatalf("parsed %+v", m)
	}
	if err := Verify(run, m, "https://www.google.com/search?q=x"); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestParseRejects(t *testing.T) {
	for _, raw := range []string{
		"wails:runtime:ready",
		"hello",
		"smc:{not json",
		`smc:{"run":"r1","nonce":"n1","kind":"shell"}`,
	} {
		if _, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q) accepted", raw)
		}
	}
	if _, err := Parse("hello"); !errors.Is(err, ErrNotOurs) {
		t.Errorf("plain text: %v, want ErrNotOurs", err)
	}
}

func TestVerifyRejects(t *testing.T) {
	run := Run{ID: "r1", Nonce: "n1", Hosts: []string{"www.google.com"}}
	good := Message{Run: "r1", Nonce: "n1", Kind: "result"}
	cases := []struct {
		name   string
		m      Message
		origin string
		want   error
	}{
		{"wrong nonce", Message{Run: "r1", Nonce: "nX", Kind: "result"}, "https://www.google.com/", ErrBadRun},
		{"wrong run", Message{Run: "r2", Nonce: "n1", Kind: "result"}, "https://www.google.com/", ErrBadRun},
		{"other host", good, "https://evil.example/", ErrBadOrigin},
		{"subdomain", good, "https://x.www.google.com/", ErrBadOrigin},
		{"http", good, "http://www.google.com/", ErrBadOrigin},
		{"app page", good, "wails://wails/boot.html", ErrBadOrigin},
		{"empty", good, "", ErrBadOrigin},
	}
	for _, c := range cases {
		if err := Verify(run, c.m, c.origin); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}

func TestNewRun(t *testing.T) {
	a, b := NewRun("www.google.com"), NewRun("www.google.com")
	if a.ID == b.ID || a.Nonce == b.Nonce || len(a.Nonce) < 20 {
		t.Fatalf("runs not random: %+v %+v", a, b)
	}
}
