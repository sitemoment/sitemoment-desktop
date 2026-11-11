// SPDX-License-Identifier: AGPL-3.0-or-later
package report

import (
	"context"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// Build gathers, renders and stores a report of site; origin is weekly or manual.
func Build(ctx context.Context, st *store.Store, site store.Site, now time.Time, origin string) (store.Report, error) {
	d, err := Gather(ctx, st, site, now)
	if err != nil {
		return store.Report{}, err
	}
	md := Markdown(d)
	r := store.Report{SiteID: site.ID, Origin: origin, From: d.From, To: d.To, Title: Title(d), Markdown: md, HTML: HTML(d, md), CreatedAt: now.Unix()}
	r.ID, err = st.AddReport(ctx, r)
	return r, err
}
