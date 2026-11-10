CREATE TABLE gsc_facts (
  site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  slice TEXT NOT NULL,
  day TEXT NOT NULL,
  query TEXT NOT NULL DEFAULT '',
  page TEXT NOT NULL DEFAULT '',
  clicks REAL NOT NULL,
  impressions REAL NOT NULL,
  position REAL NOT NULL,
  PRIMARY KEY (site_id, slice, day, query, page)
) WITHOUT ROWID;

CREATE TABLE sync_days (
  site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  source TEXT NOT NULL,
  slice TEXT NOT NULL,
  day TEXT NOT NULL,
  rows INTEGER NOT NULL,
  fetched_at INTEGER NOT NULL,
  PRIMARY KEY (site_id, source, slice, day)
) WITHOUT ROWID;
