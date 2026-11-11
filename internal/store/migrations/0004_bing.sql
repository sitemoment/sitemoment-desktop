CREATE TABLE bing_daily (
  site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  day TEXT NOT NULL,
  clicks REAL NOT NULL,
  impressions REAL NOT NULL,
  PRIMARY KEY (site_id, day)
) WITHOUT ROWID;

CREATE TABLE bing_queries (
  site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  week TEXT NOT NULL,
  query TEXT NOT NULL,
  clicks REAL NOT NULL,
  impressions REAL NOT NULL,
  position REAL NOT NULL,
  PRIMARY KEY (site_id, week, query)
) WITHOUT ROWID;

CREATE TABLE bing_pages (
  site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  week TEXT NOT NULL,
  page TEXT NOT NULL,
  clicks REAL NOT NULL,
  impressions REAL NOT NULL,
  position REAL NOT NULL,
  PRIMARY KEY (site_id, week, page)
) WITHOUT ROWID;
