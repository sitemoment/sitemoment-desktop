CREATE TABLE sites (
  id INTEGER PRIMARY KEY,
  host TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL DEFAULT '',
  google_account TEXT NOT NULL DEFAULT '',
  gsc_property TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);

CREATE TABLE connections (
  id INTEGER PRIMARY KEY,
  kind TEXT NOT NULL,
  account TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  UNIQUE (kind, account)
);
