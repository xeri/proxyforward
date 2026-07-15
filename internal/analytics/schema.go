package analytics

// schemaV1 is the complete DDL, laid down in full on day one: later features
// only write to columns that already exist here, so mid-flight ALTERs are
// never needed. Schema changes append new entries to migrations instead of
// editing this constant.
//
// Gauge columns use -1 for "unknown", matching stats.Bucket semantics.
// Timestamps are unix milliseconds (UTC) throughout.
const schemaV1 = `
-- RRD tiers T2/T3/T4 of the bandwidth history (T0/T1 stay memory-only).
-- Column order mirrors stats.Bucket: byte sums, then OHLC of in-rate,
-- out-rate, connection gauge, link-RTT gauge, player gauge, loss gauge.
CREATE TABLE rrd (
  tier INTEGER NOT NULL,
  t    INTEGER NOT NULL,
  inb  INTEGER NOT NULL DEFAULT 0,
  outb INTEGER NOT NULL DEFAULT 0,
  io REAL NOT NULL DEFAULT 0,  ih REAL NOT NULL DEFAULT 0,  il REAL NOT NULL DEFAULT 0,  ic REAL NOT NULL DEFAULT 0,
  oo REAL NOT NULL DEFAULT 0,  oh REAL NOT NULL DEFAULT 0,  ol REAL NOT NULL DEFAULT 0,  oc REAL NOT NULL DEFAULT 0,
  co REAL NOT NULL DEFAULT -1, ch REAL NOT NULL DEFAULT -1, cl REAL NOT NULL DEFAULT -1, cc REAL NOT NULL DEFAULT -1,
  ro REAL NOT NULL DEFAULT -1, rh REAL NOT NULL DEFAULT -1, rl REAL NOT NULL DEFAULT -1, rc REAL NOT NULL DEFAULT -1,
  po REAL NOT NULL DEFAULT -1, ph REAL NOT NULL DEFAULT -1, pl REAL NOT NULL DEFAULT -1, pc REAL NOT NULL DEFAULT -1,
  lo REAL NOT NULL DEFAULT -1, lh REAL NOT NULL DEFAULT -1, ll REAL NOT NULL DEFAULT -1, lc REAL NOT NULL DEFAULT -1,
  PRIMARY KEY (tier, t)
) WITHOUT ROWID;

CREATE TABLE lifetime (
  id             INTEGER PRIMARY KEY CHECK (id = 1),
  bytes_in       INTEGER NOT NULL DEFAULT 0,
  bytes_out      INTEGER NOT NULL DEFAULT 0,
  link_bytes_in  INTEGER NOT NULL DEFAULT 0,
  link_bytes_out INTEGER NOT NULL DEFAULT 0,
  uptime_ms      INTEGER NOT NULL DEFAULT 0,
  link_sessions  INTEGER NOT NULL DEFAULT 0,
  first_run_ms   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE peers (
  ip         TEXT PRIMARY KEY,
  first_seen INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL,
  bytes_in   INTEGER NOT NULL DEFAULT 0,
  bytes_out  INTEGER NOT NULL DEFAULT 0,
  conns      INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;

-- Connection history, one row per proxied connection (metadata only).
-- conn_key is the gateway-issued connection id used to correlate RTT reports
-- across the control link; player/geo/rtt columns are filled as those
-- pipelines land.
CREATE TABLE sessions (
  id          INTEGER PRIMARY KEY,
  conn_key    TEXT,
  tunnel_id   TEXT NOT NULL,
  tunnel_name TEXT NOT NULL DEFAULT '',
  client_ip   TEXT NOT NULL,
  client_port INTEGER NOT NULL DEFAULT 0,
  started_ms  INTEGER NOT NULL,
  ended_ms    INTEGER,             -- NULL while live
  bytes_in    INTEGER NOT NULL DEFAULT 0,
  bytes_out   INTEGER NOT NULL DEFAULT 0,
  player_uuid TEXT,
  player_name TEXT,
  protocol    INTEGER,
  cc TEXT, asn INTEGER, as_org TEXT, isp TEXT,
  rtt_avg REAL, rtt_min REAL, rtt_max REAL, rtt_n INTEGER
);
CREATE INDEX sessions_started ON sessions(started_ms);
CREATE INDEX sessions_player  ON sessions(player_uuid, started_ms);
CREATE INDEX sessions_tunnel  ON sessions(tunnel_id, started_ms);

-- Per-connection traffic samples (~15 s cadence) for the session replay
-- timeline. inb/outb are deltas within the sample interval.
CREATE TABLE session_traffic (
  session_id INTEGER NOT NULL,
  t          INTEGER NOT NULL,
  inb        INTEGER NOT NULL DEFAULT 0,
  outb       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (session_id, t)
) WITHOUT ROWID;

-- Per-connection RTT aggregates, one row per minute.
CREATE TABLE session_rtt (
  session_id INTEGER NOT NULL,
  t          INTEGER NOT NULL,
  avg REAL NOT NULL, mn REAL NOT NULL, mx REAL NOT NULL,
  n  INTEGER NOT NULL,
  PRIMARY KEY (session_id, t)
) WITHOUT ROWID;

-- Player identity. uuid is dashed-lowercase; unresolved (offline-mode or
-- cracked) players use "offline:<name-lower>" with offline=1.
CREATE TABLE players (
  uuid               TEXT PRIMARY KEY,
  name               TEXT NOT NULL,
  offline            INTEGER NOT NULL DEFAULT 0,
  first_seen         INTEGER NOT NULL DEFAULT 0,
  last_seen          INTEGER NOT NULL DEFAULT 0,
  sessions           INTEGER NOT NULL DEFAULT 0,
  play_ms            INTEGER NOT NULL DEFAULT 0,
  bytes_in           INTEGER NOT NULL DEFAULT 0,
  bytes_out          INTEGER NOT NULL DEFAULT 0,
  last_cc            TEXT NOT NULL DEFAULT '',
  profile_checked_ms INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;

-- Names seen on this proxy (Mojang removed the public name-history API in
-- 2022; this is built from local observations only).
CREATE TABLE player_names (
  uuid       TEXT NOT NULL,
  name       TEXT NOT NULL,
  first_seen INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL,
  PRIMARY KEY (uuid, name)
) WITHOUT ROWID;

CREATE TABLE player_ips (
  uuid       TEXT NOT NULL,
  ip         TEXT NOT NULL,
  first_seen INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL,
  sessions   INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (uuid, ip)
) WITHOUT ROWID;

-- name -> uuid resolution cache; uuid '' records a confirmed miss (404) so
-- cracked names are not re-asked every join.
CREATE TABLE uuid_cache (
  name_lower  TEXT PRIMARY KEY,
  uuid        TEXT NOT NULL DEFAULT '',
  resolved_ms INTEGER NOT NULL
) WITHOUT ROWID;

CREATE TABLE geo_cache (
  ip          TEXT PRIMARY KEY,
  cc          TEXT NOT NULL DEFAULT '',
  country     TEXT NOT NULL DEFAULT '',
  asn         INTEGER NOT NULL DEFAULT 0,
  as_org      TEXT NOT NULL DEFAULT '',
  isp         TEXT NOT NULL DEFAULT '',
  resolved_ms INTEGER NOT NULL
) WITHOUT ROWID;

-- Hourly/daily rollups; hour_ms/day_ms are UTC bucket starts, rendered in
-- local time by the frontend. Gauge averages use -1 for unknown.
CREATE TABLE rollup_hourly (
  hour_ms        INTEGER PRIMARY KEY,
  bytes_in       INTEGER NOT NULL DEFAULT 0,
  bytes_out      INTEGER NOT NULL DEFAULT 0,
  peak_in_bps    REAL NOT NULL DEFAULT 0,
  peak_out_bps   REAL NOT NULL DEFAULT 0,
  peak_players   REAL NOT NULL DEFAULT -1,
  avg_players    REAL NOT NULL DEFAULT -1,
  sessions       INTEGER NOT NULL DEFAULT 0,
  unique_players INTEGER NOT NULL DEFAULT 0,
  rtt_avg        REAL NOT NULL DEFAULT -1,
  loss_avg       REAL NOT NULL DEFAULT -1
) WITHOUT ROWID;

CREATE TABLE rollup_daily (
  day_ms         INTEGER PRIMARY KEY,
  bytes_in       INTEGER NOT NULL DEFAULT 0,
  bytes_out      INTEGER NOT NULL DEFAULT 0,
  peak_in_bps    REAL NOT NULL DEFAULT 0,
  peak_out_bps   REAL NOT NULL DEFAULT 0,
  peak_players   REAL NOT NULL DEFAULT -1,
  avg_players    REAL NOT NULL DEFAULT -1,
  sessions       INTEGER NOT NULL DEFAULT 0,
  unique_players INTEGER NOT NULL DEFAULT 0,
  rtt_avg        REAL NOT NULL DEFAULT -1,
  loss_avg       REAL NOT NULL DEFAULT -1
) WITHOUT ROWID;

-- All-time records: keys 'players', 'in_bps', 'out_bps', 'conns'.
CREATE TABLE peaks (
  key   TEXT PRIMARY KEY,
  value REAL NOT NULL,
  at_ms INTEGER NOT NULL
) WITHOUT ROWID;

-- Uptime transitions: kind 'link' (tunnel_id '') or 'tunnel_local'.
CREATE TABLE events (
  id        INTEGER PRIMARY KEY,
  t         INTEGER NOT NULL,
  kind      TEXT NOT NULL,
  tunnel_id TEXT NOT NULL DEFAULT '',
  up        INTEGER NOT NULL
);
CREATE INDEX events_t ON events(kind, tunnel_id, t);
`

// schemaV2 drops the players table's dead denormalized aggregate columns
// (never written — playtime/bytes/session counts are computed from the
// sessions table at query time) and adds the indexes the geo snapshot,
// identity backfill, and country-name subqueries lean on.
const schemaV2 = `
ALTER TABLE players DROP COLUMN sessions;
ALTER TABLE players DROP COLUMN play_ms;
ALTER TABLE players DROP COLUMN bytes_in;
ALTER TABLE players DROP COLUMN bytes_out;
CREATE INDEX sessions_cc ON sessions(cc, started_ms);
CREATE INDEX sessions_backfill ON sessions(player_name COLLATE NOCASE)
  WHERE player_uuid IS NULL OR player_uuid = '';
CREATE INDEX geo_cache_cc ON geo_cache(cc);
`

// schemaV3 introduces per-agent attribution for the multi-agent gateway. A
// gateway now admits several agents; two of them may serve the same tunnel_id,
// so topology and bandwidth history must be keyed by agent. This is a
// destructive rebuild (the "full wipe" decision): every table is dropped and
// recreated fresh — local analytics is disposable telemetry, and a clean slate
// avoids orphan-identity half-states. Because the ladder is append-only, the
// wipe rides here as schemaV3 rather than editing schemaV1/schemaV2; an existing
// v2 database climbs to v3 by dropping its tables and recreating them empty.
//
// agent_id ” is the gateway-wide / single-agent series. Topology tables
// (sessions, events, rrd, rollup_hourly, rollup_daily) gain agent scope; the
// pure identity/geo caches keep their global shape (only their data resets).
const schemaV3 = `
DROP TABLE IF EXISTS rrd;
DROP TABLE IF EXISTS lifetime;
DROP TABLE IF EXISTS peers;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS session_traffic;
DROP TABLE IF EXISTS session_rtt;
DROP TABLE IF EXISTS players;
DROP TABLE IF EXISTS player_names;
DROP TABLE IF EXISTS player_ips;
DROP TABLE IF EXISTS uuid_cache;
DROP TABLE IF EXISTS geo_cache;
DROP TABLE IF EXISTS rollup_hourly;
DROP TABLE IF EXISTS rollup_daily;
DROP TABLE IF EXISTS peaks;
DROP TABLE IF EXISTS events;

-- RRD tiers, now per agent: agent_id '' is the gateway-wide series, one row per
-- (agent, tier, bucket). Column order still mirrors stats.Bucket.
CREATE TABLE rrd (
  agent_id TEXT NOT NULL DEFAULT '',
  tier INTEGER NOT NULL,
  t    INTEGER NOT NULL,
  inb  INTEGER NOT NULL DEFAULT 0,
  outb INTEGER NOT NULL DEFAULT 0,
  io REAL NOT NULL DEFAULT 0,  ih REAL NOT NULL DEFAULT 0,  il REAL NOT NULL DEFAULT 0,  ic REAL NOT NULL DEFAULT 0,
  oo REAL NOT NULL DEFAULT 0,  oh REAL NOT NULL DEFAULT 0,  ol REAL NOT NULL DEFAULT 0,  oc REAL NOT NULL DEFAULT 0,
  co REAL NOT NULL DEFAULT -1, ch REAL NOT NULL DEFAULT -1, cl REAL NOT NULL DEFAULT -1, cc REAL NOT NULL DEFAULT -1,
  ro REAL NOT NULL DEFAULT -1, rh REAL NOT NULL DEFAULT -1, rl REAL NOT NULL DEFAULT -1, rc REAL NOT NULL DEFAULT -1,
  po REAL NOT NULL DEFAULT -1, ph REAL NOT NULL DEFAULT -1, pl REAL NOT NULL DEFAULT -1, pc REAL NOT NULL DEFAULT -1,
  lo REAL NOT NULL DEFAULT -1, lh REAL NOT NULL DEFAULT -1, ll REAL NOT NULL DEFAULT -1, lc REAL NOT NULL DEFAULT -1,
  PRIMARY KEY (agent_id, tier, t)
) WITHOUT ROWID;

CREATE TABLE lifetime (
  id             INTEGER PRIMARY KEY CHECK (id = 1),
  bytes_in       INTEGER NOT NULL DEFAULT 0,
  bytes_out      INTEGER NOT NULL DEFAULT 0,
  link_bytes_in  INTEGER NOT NULL DEFAULT 0,
  link_bytes_out INTEGER NOT NULL DEFAULT 0,
  uptime_ms      INTEGER NOT NULL DEFAULT 0,
  link_sessions  INTEGER NOT NULL DEFAULT 0,
  first_run_ms   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE peers (
  ip         TEXT PRIMARY KEY,
  first_seen INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL,
  bytes_in   INTEGER NOT NULL DEFAULT 0,
  bytes_out  INTEGER NOT NULL DEFAULT 0,
  conns      INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;

-- Connection history, one row per proxied connection. agent_id owns the tunnel;
-- two agents may share a tunnel_id, so attribution needs both.
CREATE TABLE sessions (
  id          INTEGER PRIMARY KEY,
  agent_id    TEXT NOT NULL DEFAULT '',
  conn_key    TEXT,
  tunnel_id   TEXT NOT NULL,
  tunnel_name TEXT NOT NULL DEFAULT '',
  client_ip   TEXT NOT NULL,
  client_port INTEGER NOT NULL DEFAULT 0,
  started_ms  INTEGER NOT NULL,
  ended_ms    INTEGER,             -- NULL while live
  bytes_in    INTEGER NOT NULL DEFAULT 0,
  bytes_out   INTEGER NOT NULL DEFAULT 0,
  player_uuid TEXT,
  player_name TEXT,
  protocol    INTEGER,
  cc TEXT, asn INTEGER, as_org TEXT, isp TEXT,
  rtt_avg REAL, rtt_min REAL, rtt_max REAL, rtt_n INTEGER
);
CREATE INDEX sessions_started ON sessions(started_ms);
CREATE INDEX sessions_player  ON sessions(player_uuid, started_ms);
CREATE INDEX sessions_tunnel  ON sessions(agent_id, tunnel_id, started_ms);
CREATE INDEX sessions_agent   ON sessions(agent_id, started_ms);
CREATE INDEX sessions_cc      ON sessions(cc, started_ms);
CREATE INDEX sessions_backfill ON sessions(player_name COLLATE NOCASE)
  WHERE player_uuid IS NULL OR player_uuid = '';

CREATE TABLE session_traffic (
  session_id INTEGER NOT NULL,
  t          INTEGER NOT NULL,
  inb        INTEGER NOT NULL DEFAULT 0,
  outb       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (session_id, t)
) WITHOUT ROWID;

CREATE TABLE session_rtt (
  session_id INTEGER NOT NULL,
  t          INTEGER NOT NULL,
  avg REAL NOT NULL, mn REAL NOT NULL, mx REAL NOT NULL,
  n  INTEGER NOT NULL,
  PRIMARY KEY (session_id, t)
) WITHOUT ROWID;

CREATE TABLE players (
  uuid               TEXT PRIMARY KEY,
  name               TEXT NOT NULL,
  offline            INTEGER NOT NULL DEFAULT 0,
  first_seen         INTEGER NOT NULL DEFAULT 0,
  last_seen          INTEGER NOT NULL DEFAULT 0,
  last_cc            TEXT NOT NULL DEFAULT '',
  profile_checked_ms INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;

CREATE TABLE player_names (
  uuid       TEXT NOT NULL,
  name       TEXT NOT NULL,
  first_seen INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL,
  PRIMARY KEY (uuid, name)
) WITHOUT ROWID;

CREATE TABLE player_ips (
  uuid       TEXT NOT NULL,
  ip         TEXT NOT NULL,
  first_seen INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL,
  sessions   INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (uuid, ip)
) WITHOUT ROWID;

CREATE TABLE uuid_cache (
  name_lower  TEXT PRIMARY KEY,
  uuid        TEXT NOT NULL DEFAULT '',
  resolved_ms INTEGER NOT NULL
) WITHOUT ROWID;

CREATE TABLE geo_cache (
  ip          TEXT PRIMARY KEY,
  cc          TEXT NOT NULL DEFAULT '',
  country     TEXT NOT NULL DEFAULT '',
  asn         INTEGER NOT NULL DEFAULT 0,
  as_org      TEXT NOT NULL DEFAULT '',
  isp         TEXT NOT NULL DEFAULT '',
  resolved_ms INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX geo_cache_cc ON geo_cache(cc);

-- Hourly/daily rollups, now per agent (agent_id '' is gateway-wide).
CREATE TABLE rollup_hourly (
  hour_ms        INTEGER NOT NULL,
  agent_id       TEXT NOT NULL DEFAULT '',
  bytes_in       INTEGER NOT NULL DEFAULT 0,
  bytes_out      INTEGER NOT NULL DEFAULT 0,
  peak_in_bps    REAL NOT NULL DEFAULT 0,
  peak_out_bps   REAL NOT NULL DEFAULT 0,
  peak_players   REAL NOT NULL DEFAULT -1,
  avg_players    REAL NOT NULL DEFAULT -1,
  sessions       INTEGER NOT NULL DEFAULT 0,
  unique_players INTEGER NOT NULL DEFAULT 0,
  rtt_avg        REAL NOT NULL DEFAULT -1,
  loss_avg       REAL NOT NULL DEFAULT -1,
  PRIMARY KEY (hour_ms, agent_id)
) WITHOUT ROWID;

CREATE TABLE rollup_daily (
  day_ms         INTEGER NOT NULL,
  agent_id       TEXT NOT NULL DEFAULT '',
  bytes_in       INTEGER NOT NULL DEFAULT 0,
  bytes_out      INTEGER NOT NULL DEFAULT 0,
  peak_in_bps    REAL NOT NULL DEFAULT 0,
  peak_out_bps   REAL NOT NULL DEFAULT 0,
  peak_players   REAL NOT NULL DEFAULT -1,
  avg_players    REAL NOT NULL DEFAULT -1,
  sessions       INTEGER NOT NULL DEFAULT 0,
  unique_players INTEGER NOT NULL DEFAULT 0,
  rtt_avg        REAL NOT NULL DEFAULT -1,
  loss_avg       REAL NOT NULL DEFAULT -1,
  PRIMARY KEY (day_ms, agent_id)
) WITHOUT ROWID;

CREATE TABLE peaks (
  key   TEXT PRIMARY KEY,
  value REAL NOT NULL,
  at_ms INTEGER NOT NULL
) WITHOUT ROWID;

-- Uptime transitions, now per agent: kind 'link'/'engine' (tunnel_id '') or
-- 'tunnel_local'. agent_id '' is engine-lifecycle / single-agent.
CREATE TABLE events (
  id        INTEGER PRIMARY KEY,
  t         INTEGER NOT NULL,
  agent_id  TEXT NOT NULL DEFAULT '',
  kind      TEXT NOT NULL,
  tunnel_id TEXT NOT NULL DEFAULT '',
  up        INTEGER NOT NULL
);
CREATE INDEX events_t ON events(kind, agent_id, tunnel_id, t);
`

// migrations is the schema ladder; PRAGMA user_version tracks how far a
// database has climbed. Append only — never edit an applied entry.
var migrations = []string{schemaV1, schemaV2, schemaV3}
