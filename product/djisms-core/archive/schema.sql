PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS schema_version(version INTEGER NOT NULL PRIMARY KEY);
INSERT OR IGNORE INTO schema_version VALUES(1);
CREATE TABLE IF NOT EXISTS events(
  seq INTEGER PRIMARY KEY, event_hash TEXT NOT NULL UNIQUE,
  kind TEXT NOT NULL, record_id TEXT, occurred_at TEXT NOT NULL, payload TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS receipts(
  id TEXT PRIMARY KEY, source_id TEXT NOT NULL UNIQUE,
  device_key TEXT NOT NULL, connection_id TEXT NOT NULL,
  storage TEXT, storage_index INTEGER CHECK(storage_index IS NULL OR storage_index BETWEEN 0 AND 65535),
  raw_path TEXT NOT NULL UNIQUE, raw_file_sha256 TEXT NOT NULL,
  pdu_ascii_sha256 TEXT NOT NULL, pdu_bytes_sha256 TEXT,
  metadata_json TEXT NOT NULL, archived_at TEXT NOT NULL,
  archive_state TEXT NOT NULL CHECK(archive_state IN ('preserved','verified','reconciled')),
  decode_state TEXT NOT NULL DEFAULT 'unknown', sender TEXT, body TEXT,
  decoded_json TEXT, delete_state TEXT NOT NULL DEFAULT 'not_requested'
);
CREATE INDEX IF NOT EXISTS receipt_slot ON receipts(device_key,storage,storage_index);
CREATE TABLE IF NOT EXISTS messages(
  id TEXT PRIMARY KEY, state TEXT NOT NULL, sender TEXT, body TEXT,
  created_at TEXT NOT NULL, notification_state TEXT NOT NULL DEFAULT 'suppressed'
);
CREATE TABLE IF NOT EXISTS message_parts(
  message_id TEXT NOT NULL REFERENCES messages(id),
  receipt_id TEXT NOT NULL REFERENCES receipts(id), ordinal INTEGER NOT NULL,
  PRIMARY KEY(message_id,ordinal), UNIQUE(message_id,receipt_id)
);
CREATE TABLE IF NOT EXISTS notifications(
  message_id TEXT PRIMARY KEY REFERENCES messages(id),
  state TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0,
  last_error TEXT, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS purge_attempts(
  id TEXT PRIMARY KEY, receipt_id TEXT NOT NULL REFERENCES receipts(id),
  storage TEXT NOT NULL, storage_index INTEGER NOT NULL,
  expected_pdu_sha256 TEXT NOT NULL, connection_id TEXT NOT NULL,
  mode TEXT NOT NULL CHECK(mode IN ('simulation','approved_live')),
  state TEXT NOT NULL, response TEXT, started_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS message_visibility(
 message_id TEXT PRIMARY KEY REFERENCES messages(id),
 reason TEXT NOT NULL, hidden_at TEXT NOT NULL
);
