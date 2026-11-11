// SPDX-License-Identifier: AGPL-3.0-or-later
package cloud

import "testing"

func val(p *float64) float64 {
	if p == nil {
		return -1
	}
	return *p
}

func TestParseKeywordMetrics(t *testing.T) {
	got := ParseKeywordMetrics([]byte(`{"items":[
		{"keyword":"AI  Chat","found":true,"volume":1900,"cpc":"1.25","kd":34,"monthly":[{"month":"2026-10","volume":2000}]},
		{"keyword":"nothing here","found":false,"volume":0},
		{"keyword":"logo maker","search_volume":"12,100","keyword_difficulty":{"score":61}}]}`))
	if len(got) != 2 {
		t.Fatalf("got %d keywords: %+v", len(got), got)
	}
	a := got["ai chat"]
	if val(a.Volume) != 1900 || val(a.KD) != 34 || val(a.CPC) != 1.25 {
		t.Fatalf("ai chat = vol %v kd %v cpc %v", val(a.Volume), val(a.KD), val(a.CPC))
	}
	l := got["logo maker"]
	if val(l.Volume) != 12100 || val(l.KD) != 61 || l.CPC != nil {
		t.Fatalf("logo maker = vol %v kd %v cpc %v", val(l.Volume), val(l.KD), val(l.CPC))
	}
	nested := ParseKeywordMetrics([]byte(`{"data":{"rows":[{"kw":"b","sv":10}]}}`))
	if val(nested["b"].Volume) != 10 {
		t.Fatalf("nested = %+v", nested)
	}
	if len(ParseKeywordMetrics([]byte(`not json`))) != 0 || len(ParseKeywordMetrics(nil)) != 0 {
		t.Fatal("bad input should give no keywords")
	}
}
