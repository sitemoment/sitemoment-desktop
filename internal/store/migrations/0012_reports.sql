CREATE TABLE reports (
  id INTEGER PRIMARY KEY,
  site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  origin TEXT NOT NULL,
  period_from TEXT NOT NULL,
  period_to TEXT NOT NULL,
  title TEXT NOT NULL,
  markdown TEXT NOT NULL,
  html TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX reports_site ON reports(site_id, id);
