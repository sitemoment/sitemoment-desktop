// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import "testing"

func TestSettings(t *testing.T) {
	s := open(t)
	if v, err := s.Setting(ctx, "llm.provider"); err != nil || v != "" {
		t.Fatalf("missing = %q, %v", v, err)
	}
	for _, v := range []string{"openai", "anthropic"} {
		if err := s.SetSetting(ctx, "llm.provider", v); err != nil {
			t.Fatal(err)
		}
		if got, err := s.Setting(ctx, "llm.provider"); err != nil || got != v {
			t.Fatalf("got %q, %v; want %q", got, err, v)
		}
	}
	if err := s.SetSetting(ctx, "llm.provider", ""); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Setting(ctx, "llm.provider"); v != "" {
		t.Fatalf("after delete = %q", v)
	}
}
