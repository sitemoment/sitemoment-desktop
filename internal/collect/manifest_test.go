// SPDX-License-Identifier: AGPL-3.0-or-later
package collect

import (
	"strings"
	"testing"
	"testing/fstest"
)

const testSchema = `{"description":"查一个词","input":{"type":"object","properties":{` +
	`"q":{"type":"string","description":"搜索词"},"gl":{"type":"string","default":"us","enum":["us","gb"]},` +
	`"n":{"type":"integer","default":10},"fresh":{"type":"boolean"}},"required":["q"]},"output":{"type":"object"}}`

func pkg(manifest string) fstest.MapFS {
	return fstest.MapFS{
		"x/manifest.json": {Data: []byte(manifest)},
		"x/schema.json":   {Data: []byte(testSchema)},
		"x/collector.js":  {Data: []byte("sm.done({});")},
	}
}

const goodManifest = `{"id":"demo-serp","name":"演示","version":"1.0.0","hosts":["www.example.com"],` +
	`"url":"https://www.example.com/search?q={q}&gl={gl}&n={n}","minGapSeconds":10,"dailyLimit":50}`

func TestLoadAndNormalize(t *testing.T) {
	c, err := Load(pkg(goodManifest), "x", false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Tool != "demo_serp" || c.CacheHours != 24 || c.TimeoutSeconds != 60 || c.MinGapSeconds != 10 || c.DailyLimit != 50 {
		t.Fatalf("defaults: %+v", c.Manifest)
	}
	in, err := c.Normalize(map[string]any{"q": "  ai chat ", "extra": 1, "n": 3.7})
	if err != nil {
		t.Fatal(err)
	}
	if in["q"] != "ai chat" || in["gl"] != "us" || in["n"] != 3.0 || len(in) != 3 {
		t.Fatalf("normalized = %v", in)
	}
	u, err := c.PageURL(in)
	if err != nil || u != "https://www.example.com/search?q=ai+chat&gl=us&n=3" {
		t.Fatalf("url = %q, %v", u, err)
	}
	for _, bad := range []map[string]any{{}, {"q": ""}, {"q": 3.0}, {"q": "a", "gl": "fr"}, {"q": "a", "fresh": "yes"}, {"q": strings.Repeat("x", 201)}} {
		if _, err := c.Normalize(bad); err == nil {
			t.Errorf("Normalize(%v) accepted", bad)
		}
	}
}

func TestLoadRejects(t *testing.T) {
	for name, m := range map[string]string{
		"bad id":         strings.Replace(goodManifest, `"demo-serp"`, `"Demo"`, 1),
		"http url":       strings.Replace(goodManifest, `https://www.example.com/search`, `http://www.example.com/search`, 1),
		"other host":     strings.Replace(goodManifest, `https://www.example.com/search`, `https://evil.example.org/search`, 1),
		"host param":     strings.Replace(goodManifest, `https://www.example.com/search`, `https://{q}.example.com/search`, 1),
		"port":           strings.Replace(goodManifest, `https://www.example.com/search`, `https://www.example.com:8443/search`, 1),
		"wildcard host":  strings.Replace(goodManifest, `["www.example.com"]`, `["*.example.com"]`, 1),
		"unknown param":  strings.Replace(goodManifest, `{n}`, `{page}`, 1),
		"bad tool":       strings.Replace(goodManifest, `"name"`, `"tool":"Bad-Tool","name"`, 1),
		"no name":        strings.Replace(goodManifest, `"演示"`, `""`, 1),
		"not json":       `{`,
		"no hosts":       strings.Replace(goodManifest, `["www.example.com"]`, `[]`, 1),
		"uppercase host": strings.Replace(goodManifest, `["www.example.com"]`, `["WWW.example.com"]`, 1),
		"ip literal":     strings.Replace(goodManifest, `["www.example.com"]`, `["203.0.113.5"]`, 1),
		"local TLD":      strings.Replace(goodManifest, `["www.example.com"]`, `["printer.local"]`, 1),
		"internal TLD":   strings.Replace(goodManifest, `["www.example.com"]`, `["db.internal"]`, 1),
	} {
		if _, err := Load(pkg(m), "x", false); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	noJS := pkg(goodManifest)
	delete(noJS, "x/collector.js")
	if _, err := Load(noJS, "x", false); err == nil {
		t.Error("missing collector.js accepted")
	}
}
