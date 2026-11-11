CREATE TABLE collector_runs (
  id INTEGER PRIMARY KEY,
  collector TEXT NOT NULL,
  input_hash TEXT NOT NULL,
  input TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('running', 'need_human', 'ok', 'error', 'cancelled')),
  result TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  finished_at INTEGER NOT NULL DEFAULT 0,
  expires_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX collector_runs_cache ON collector_runs (collector, input_hash, expires_at);
CREATE INDEX collector_runs_day ON collector_runs (collector, created_at);
CREATE INDEX collector_runs_status ON collector_runs (status, id);

CREATE TABLE collector_grants (
  collector TEXT PRIMARY KEY,
  version TEXT NOT NULL,
  hosts TEXT NOT NULL,
  granted_at INTEGER NOT NULL
) WITHOUT ROWID;
