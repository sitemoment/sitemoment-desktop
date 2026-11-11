CREATE TABLE agent_sessions (
  id INTEGER PRIMARY KEY,
  site_id INTEGER REFERENCES sites(id) ON DELETE CASCADE,
  title TEXT NOT NULL DEFAULT '',
  spend_mode TEXT NOT NULL DEFAULT 'confirm',
  turn INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'idle',
  pending TEXT NOT NULL DEFAULT '',
  credits REAL NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX agent_sessions_site ON agent_sessions(site_id, updated_at);

CREATE TABLE agent_messages (
  id INTEGER PRIMARY KEY,
  session_id INTEGER NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
  turn INTEGER NOT NULL,
  role TEXT NOT NULL,
  content TEXT NOT NULL DEFAULT '',
  tool_calls TEXT NOT NULL DEFAULT '',
  tool_call_id TEXT NOT NULL DEFAULT '',
  native TEXT NOT NULL DEFAULT '',
  native_of TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
CREATE INDEX agent_messages_session ON agent_messages(session_id, id);

CREATE TABLE agent_tool_calls (
  id INTEGER PRIMARY KEY,
  session_id INTEGER NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
  turn INTEGER NOT NULL,
  call_id TEXT NOT NULL,
  tool TEXT NOT NULL,
  label TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL DEFAULT '',
  args TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  cached INTEGER NOT NULL DEFAULT 0,
  credits REAL NOT NULL DEFAULT 0,
  card TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  started_at INTEGER NOT NULL,
  finished_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX agent_tool_calls_session ON agent_tool_calls(session_id, id);

CREATE TABLE web_cache (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  fetched_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
) WITHOUT ROWID;
