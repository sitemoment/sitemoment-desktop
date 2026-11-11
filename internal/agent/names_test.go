// SPDX-License-Identifier: AGPL-3.0-or-later
package agent

import "testing"

func TestReservedToolNames(t *testing.T) {
	names := ReservedToolNames()
	for _, n := range []string{"gsc_query", "keywords", "site_crawl", "keyword_suggest", "load_playbook", "keyword_difficulty", "paid_serp", "people_also_ask", "create_task", "list_tasks", "health_report", "site_pages"} {
		if !names[n] {
			t.Errorf("%s is not reserved", n)
		}
	}
	if names["google_serp"] || names["google_trends"] {
		t.Error("built-in collector tools must stay free for the collectors")
	}
}
