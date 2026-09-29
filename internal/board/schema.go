package board

// migrations holds one DDL script per schema version; migrate applies the
// ones the database has not seen and records progress in meta.
//
// v1 notes:
//   - cards.status holds a subtask's status; a container stores only
//     planned, or cancelled when it was cancelled itself, and its shown
//     status is derived from its subtasks.
//   - The CHECK constraints hold doing ⇔ held_by after every write.
//   - expires_at NULL means confirmed; accept_cmd NULL inherits the
//     Project default and an empty string means none.
//   - Links, comments and requests carry no foreign keys: purge removes them
//     with their cards in one transaction.
//   - cards_fts is a plain FTS5 table maintained by triggers, because cards
//     has a TEXT primary key; its description column is body because desc is
//     reserved inside fts5().
var migrations = []string{
	`
CREATE TABLE cards (
  id            TEXT PRIMARY KEY,
  seq           INTEGER NOT NULL UNIQUE,
  project_id    TEXT NOT NULL,
  kind          TEXT NOT NULL CHECK (kind IN ('epic', 'story', 'subtask')),
  parent_id     TEXT NOT NULL DEFAULT '',
  rank          INTEGER NOT NULL DEFAULT 0,
  title         TEXT NOT NULL,
  "desc"        TEXT NOT NULL DEFAULT '',
  win_condition TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL CHECK (status IN ('planned', 'todo', 'doing', 'done', 'cancelled')),
  prio          INTEGER NOT NULL DEFAULT 3 CHECK (prio BETWEEN 1 AND 3),
  due           TEXT NOT NULL DEFAULT '',
  effort        TEXT NOT NULL DEFAULT 'S',
  labels        TEXT NOT NULL DEFAULT '[]',
  checklist     TEXT NOT NULL DEFAULT '[]',
  blocked       INTEGER NOT NULL DEFAULT 0,
  expires_at    TEXT,
  held_by       TEXT NOT NULL DEFAULT '',
  pinned_sha    TEXT NOT NULL DEFAULT '',
  accept_cmd    TEXT,
  paths         TEXT NOT NULL DEFAULT '[]',
  cascade_id    TEXT NOT NULL DEFAULT '',
  created_by    TEXT NOT NULL,
  revision      INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL,
  moved_at      TEXT NOT NULL,
  CHECK ((status = 'doing') = (held_by <> '')),
  CHECK (kind = 'subtask' OR status IN ('planned', 'cancelled'))
);
CREATE INDEX cards_project_parent ON cards (project_id, parent_id, rank);
CREATE INDEX cards_held ON cards (held_by) WHERE held_by <> '';
CREATE INDEX cards_created_by ON cards (created_by);

CREATE TABLE sequences (
  name TEXT PRIMARY KEY,
  next INTEGER NOT NULL
);

CREATE TABLE labels (
  project_id TEXT NOT NULL,
  label      TEXT NOT NULL,
  last_used  INTEGER NOT NULL,
  PRIMARY KEY (project_id, label)
);

CREATE TABLE comments (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  card_id    TEXT NOT NULL,
  author     TEXT NOT NULL,
  agent_id   TEXT NOT NULL DEFAULT '',
  body       TEXT NOT NULL,
  automatic  INTEGER NOT NULL DEFAULT 0,
  close      INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE INDEX comments_card ON comments (card_id, id);

CREATE TABLE links (
  blocker_id TEXT NOT NULL,
  blocked_id TEXT NOT NULL,
  PRIMARY KEY (blocker_id, blocked_id)
);
CREATE INDEX links_blocked ON links (blocked_id);

CREATE TABLE requests (
  id               TEXT PRIMARY KEY,
  card_id          TEXT NOT NULL,
  task_id          TEXT NOT NULL,
  agent_id         TEXT NOT NULL DEFAULT '',
  kind             TEXT NOT NULL CHECK (kind IN ('done', 'cancel', 'blocked', 'split', 'change')),
  comment          TEXT NOT NULL DEFAULT '',
  payload          TEXT NOT NULL DEFAULT '{}',
  evidence         TEXT NOT NULL DEFAULT '',
  flags            TEXT NOT NULL DEFAULT '[]',
  base_revision    INTEGER NOT NULL DEFAULT 0,
  status           TEXT NOT NULL CHECK (status IN ('pending', 'accepted', 'rejected', 'withdrawn')),
  created_at       TEXT NOT NULL,
  decided_at       TEXT NOT NULL DEFAULT '',
  decision_comment TEXT NOT NULL DEFAULT ''
);
CREATE INDEX requests_card ON requests (card_id, status);

CREATE TABLE holds (
  id              TEXT PRIMARY KEY,
  card_id         TEXT NOT NULL,
  task_id         TEXT NOT NULL,
  attempt         INTEGER NOT NULL,
  started_at      TEXT NOT NULL,
  baseline_head   TEXT NOT NULL DEFAULT '',
  baseline_status TEXT NOT NULL DEFAULT '[]',
  ended_at        TEXT NOT NULL DEFAULT '',
  end_reason      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX holds_card ON holds (card_id, attempt);
CREATE UNIQUE INDEX holds_open ON holds (card_id) WHERE ended_at = '';

CREATE TABLE scopes (
  task_id    TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  card_id    TEXT NOT NULL,
  kind       TEXT NOT NULL CHECK (kind IN ('planning', 'working')),
  created_at TEXT NOT NULL
);

CREATE TABLE project_settings (
  project_id TEXT PRIMARY KEY,
  accept_cmd TEXT NOT NULL DEFAULT ''
);

CREATE TABLE revisions (
  project_id TEXT PRIMARY KEY,
  revision   INTEGER NOT NULL
);

CREATE VIRTUAL TABLE cards_fts USING fts5(
  title, body, labels, id UNINDEXED, project UNINDEXED,
  tokenize = 'unicode61 remove_diacritics 2'
);
CREATE TRIGGER cards_fts_ai AFTER INSERT ON cards BEGIN
  INSERT INTO cards_fts (id, project, title, body, labels)
  VALUES (new.id, new.project_id, new.title, new."desc", new.labels);
END;
CREATE TRIGGER cards_fts_ad AFTER DELETE ON cards BEGIN
  DELETE FROM cards_fts WHERE id = old.id;
END;
CREATE TRIGGER cards_fts_au AFTER UPDATE OF title, "desc", labels, project_id ON cards BEGIN
  DELETE FROM cards_fts WHERE id = old.id;
  INSERT INTO cards_fts (id, project, title, body, labels)
  VALUES (new.id, new.project_id, new.title, new."desc", new.labels);
END;
`,
	// v2: the blob name of each path dirty when a hold started, so evidence
	// can leave out the ones the Task did not change. Older holds read as
	// having none.
	`ALTER TABLE holds ADD COLUMN baseline_blobs TEXT NOT NULL DEFAULT '{}';`,
	// v3: one row per imported source task, keyed on its UUID, so a second
	// import updates its card instead of adding another. state is what the
	// last import took from the task and last_comment the highest source
	// comment number copied. A purged card leaves its row behind, so the
	// import never brings it back.
	`
CREATE TABLE import_refs (
  source_id    TEXT PRIMARY KEY,
  card_id      TEXT NOT NULL,
  state        TEXT NOT NULL,
  last_comment INTEGER NOT NULL DEFAULT 0
);
`,
}
