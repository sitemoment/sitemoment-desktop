// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"errors"
	"testing"
)

func TestReports(t *testing.T) {
	st := open(t)
	site, err := st.AddSite(ctx, "example.com", 1)
	ok(t, err)
	var last int64
	for i := 0; i < keepReports+2; i++ {
		last, err = st.AddReport(ctx, Report{SiteID: site.ID, Origin: "weekly", From: "2026-11-02", To: "2026-11-08", Title: "周报", Markdown: "# x", HTML: "<p>x</p>", CreatedAt: int64(100 + i)})
		ok(t, err)
	}
	_, err = st.AddReport(ctx, Report{SiteID: site.ID, Origin: "manual", From: "a", To: "b", Title: "手动", Markdown: "m", HTML: "h", CreatedAt: 500})
	ok(t, err)
	list, err := st.Reports(ctx, site.ID)
	ok(t, err)
	if len(list) != keepReports || list[0].Origin != "manual" || list[0].Markdown != "" {
		t.Fatalf("Reports = %d, first %+v", len(list), list[0])
	}
	r, err := st.Report(ctx, last)
	ok(t, err)
	if r.HTML != "<p>x</p>" || r.Markdown != "# x" || r.To != "2026-11-08" {
		t.Fatalf("Report = %+v", r)
	}
	if at, _ := st.LastReportAt(ctx, site.ID, "weekly"); at != int64(100+keepReports+1) {
		t.Fatalf("LastReportAt = %d", at)
	}
	ok(t, st.DeleteReport(ctx, last))
	if _, err := st.Report(ctx, last); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted report: %v", err)
	}
}
