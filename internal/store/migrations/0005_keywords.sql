CREATE TABLE keywords (
  id INTEGER PRIMARY KEY,
  site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  keyword TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('target', 'found', 'scene')),
  volume REAL,
  volume_source TEXT NOT NULL DEFAULT '',
  volume_at INTEGER NOT NULL DEFAULT 0,
  kd REAL,
  kd_source TEXT NOT NULL DEFAULT '',
  kd_at INTEGER NOT NULL DEFAULT 0,
  cpc REAL,
  cpc_source TEXT NOT NULL DEFAULT '',
  cpc_at INTEGER NOT NULL DEFAULT 0,
  notes TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  UNIQUE (site_id, keyword)
);
