// SPDX-License-Identifier: AGPL-3.0-or-later
package google

import (
	"context"
	"net/http"
	"strings"
)

// Inspection is the index status part of a URL Inspection answer.
type Inspection struct {
	// Verdict is PASS (indexed), NEUTRAL (excluded), FAIL (error) or VERDICT_UNSPECIFIED.
	Verdict string `json:"verdict"`
	// CoverageState is Search Console's wording, e.g. "Crawled - currently not indexed".
	CoverageState   string `json:"coverageState"`
	RobotsTxtState  string `json:"robotsTxtState"`
	IndexingState   string `json:"indexingState"`
	PageFetchState  string `json:"pageFetchState"`
	LastCrawlTime   string `json:"lastCrawlTime"`
	GoogleCanonical string `json:"googleCanonical"`
	UserCanonical   string `json:"userCanonical"`
	// Link opens the inspection in Search Console.
	Link string `json:"link"`
}

// Indexed reports whether Google says the URL is on Google.
func (i Inspection) Indexed() bool { return i.Verdict == "PASS" }

// inspectURL is the URL Inspection API (v1), which lives beside webmasters/v3.
func (g *GSC) inspectURL() string {
	return strings.TrimSuffix(g.base(), "/webmasters/v3") + "/v1/urlInspection/index:inspect"
}

// Inspect asks Search Console how Google indexes pageURL, which must belong
// to property. Each call counts against the property's quota of 2,000
// inspections a day (and 600 a minute).
func (g *GSC) Inspect(ctx context.Context, token, property, pageURL string) (Inspection, error) {
	var out struct {
		InspectionResult struct {
			InspectionResultLink string     `json:"inspectionResultLink"`
			IndexStatusResult    Inspection `json:"indexStatusResult"`
		} `json:"inspectionResult"`
	}
	body := map[string]any{"inspectionUrl": pageURL, "siteUrl": property, "languageCode": "en-US"}
	if err := g.call(ctx, http.MethodPost, g.inspectURL(), token, body, &out); err != nil {
		return Inspection{}, err
	}
	in := out.InspectionResult.IndexStatusResult
	in.Link = out.InspectionResult.InspectionResultLink
	return in, nil
}
