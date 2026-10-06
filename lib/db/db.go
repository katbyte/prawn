// Package db provides the SQLite store holding fetched pull requests, AI
// verdicts, and the actions ledger. One file, inspectable with the sqlite3
// CLI, safe to delete to start over.
package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/katbyte/go-kt/clog"
	_ "modernc.org/sqlite" // pure-go sqlite driver
)

type DB struct {
	*sql.DB
}

// Open opens (creating if needed) the database at path and migrates the schema.
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database %s: %w", path, err)
	}

	// sqlite handles one writer at a time; a single connection avoids SQLITE_BUSY
	sdb.SetMaxOpenConns(1)

	d := &DB{sdb}
	if err := d.migrate(); err != nil {
		_ = sdb.Close()
		return nil, fmt.Errorf("migrating database %s: %w", path, err)
	}

	return d, nil
}

// migrations are applied in order; user_version records the last applied index+1.
var migrations = []string{`
CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) WITHOUT ROWID;

CREATE TABLE prs (
  number             INTEGER PRIMARY KEY,
  title              TEXT NOT NULL DEFAULT '',
  body               TEXT NOT NULL DEFAULT '',
  state              TEXT NOT NULL DEFAULT '',
  is_draft           INTEGER NOT NULL DEFAULT 0,
  author             TEXT NOT NULL DEFAULT '',
  author_association TEXT NOT NULL DEFAULT '',
  created_at         TEXT NOT NULL DEFAULT '',
  updated_at         TEXT NOT NULL DEFAULT '',
  closed_at          TEXT NOT NULL DEFAULT '',
  merged_at          TEXT NOT NULL DEFAULT '',
  labels             TEXT NOT NULL DEFAULT '[]',
  mergeable          TEXT NOT NULL DEFAULT '',
  review_decision    TEXT NOT NULL DEFAULT '',
  additions          INTEGER NOT NULL DEFAULT 0,
  deletions          INTEGER NOT NULL DEFAULT 0,
  changed_files      INTEGER NOT NULL DEFAULT 0,
  files              TEXT NOT NULL DEFAULT '[]',
  comment_count      INTEGER NOT NULL DEFAULT 0,
  thumbs_up          INTEGER NOT NULL DEFAULT 0,
  last_commit_at     TEXT NOT NULL DEFAULT '',
  url                TEXT NOT NULL DEFAULT '',
  fetched_at         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_prs_state ON prs(state);

CREATE TABLE comments (
  id                 TEXT PRIMARY KEY,
  pr_number          INTEGER NOT NULL,
  author             TEXT NOT NULL DEFAULT '',
  author_association TEXT NOT NULL DEFAULT '',
  created_at         TEXT NOT NULL DEFAULT '',
  body               TEXT NOT NULL DEFAULT '',
  url                TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_comments_pr ON comments(pr_number, created_at);

CREATE TABLE reviews (
  id                 TEXT PRIMARY KEY,
  pr_number          INTEGER NOT NULL,
  author             TEXT NOT NULL DEFAULT '',
  author_association TEXT NOT NULL DEFAULT '',
  state              TEXT NOT NULL DEFAULT '',
  submitted_at       TEXT NOT NULL DEFAULT '',
  body               TEXT NOT NULL DEFAULT '',
  url                TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_reviews_pr ON reviews(pr_number, submitted_at);

-- the issues each PR's closing keywords reference ("fixes #N"), with enough
-- of the issue for the fixed and label checks to work without a second fetch
CREATE TABLE closes (
  pr_number    INTEGER NOT NULL,
  issue_number INTEGER NOT NULL,
  title        TEXT NOT NULL DEFAULT '',
  state        TEXT NOT NULL DEFAULT '',
  state_reason TEXT NOT NULL DEFAULT '',
  labels       TEXT NOT NULL DEFAULT '[]',
  PRIMARY KEY (pr_number, issue_number)
) WITHOUT ROWID;

CREATE TABLE ai_verdicts (
  pr_number   INTEGER NOT NULL,
  pass        TEXT NOT NULL,
  prompt_hash TEXT NOT NULL,
  model       TEXT NOT NULL DEFAULT '',
  verdict     TEXT NOT NULL DEFAULT '{}',
  confidence  REAL NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (pr_number, pass)
) WITHOUT ROWID;

CREATE TABLE actions (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  pr_number     INTEGER NOT NULL,
  action        TEXT NOT NULL,
  reason        TEXT NOT NULL,
  template      TEXT NOT NULL DEFAULT '',
  evidence      TEXT NOT NULL DEFAULT '{}',
  confidence    REAL NOT NULL DEFAULT 0,
  source        TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL DEFAULT '',
  decided_by    TEXT NOT NULL DEFAULT '',
  pr_updated_at TEXT NOT NULL DEFAULT '',
  applied_at    TEXT NOT NULL DEFAULT '',
  error         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_actions_pr ON actions(pr_number, status);
`, `
-- each open PR's unified diff, keyed to the PR update it was fetched for, so
-- the checks can see what a PR actually changes rather than guess from prose
CREATE TABLE diffs (
  pr_number  INTEGER PRIMARY KEY,
  updated_at TEXT NOT NULL DEFAULT '',
  diff       TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;
`, `
-- the explore backfill: every PR open in the period, with its full timeline
-- (label changes, review requests, commits, reviews, comments, closes...) so
-- the derived stats — ball in court, waiting cycles, response times — are
-- recomputable from the events rather than guessed from the current state
ALTER TABLE prs ADD COLUMN merged_by     TEXT NOT NULL DEFAULT '';
ALTER TABLE prs ADD COLUMN milestone     TEXT NOT NULL DEFAULT '';
ALTER TABLE prs ADD COLUMN base_ref      TEXT NOT NULL DEFAULT '';
ALTER TABLE prs ADD COLUMN head_ref      TEXT NOT NULL DEFAULT '';
-- non-empty: the timeline has more pages past this cursor, fetched by the
-- follow-up sync; '' means the stored events are complete
ALTER TABLE prs ADD COLUMN events_cursor TEXT NOT NULL DEFAULT '';

CREATE TABLE events (
  id         TEXT PRIMARY KEY,
  pr_number  INTEGER NOT NULL,
  type       TEXT NOT NULL DEFAULT '',
  actor      TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT '',
  label      TEXT NOT NULL DEFAULT '',
  milestone  TEXT NOT NULL DEFAULT '',
  subject    TEXT NOT NULL DEFAULT '',
  state      TEXT NOT NULL DEFAULT '',
  raw        TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_events_pr ON events(pr_number, created_at);

CREATE TABLE commits (
  oid          TEXT PRIMARY KEY,
  pr_number    INTEGER NOT NULL,
  author       TEXT NOT NULL DEFAULT '',
  author_name  TEXT NOT NULL DEFAULT '',
  authored_at  TEXT NOT NULL DEFAULT '',
  committed_at TEXT NOT NULL DEFAULT '',
  additions    INTEGER NOT NULL DEFAULT 0,
  deletions    INTEGER NOT NULL DEFAULT 0,
  headline     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_commits_pr ON commits(pr_number, committed_at);
`, `
-- the combined CI state of the head commit (SUCCESS, FAILURE, ERROR, PENDING,
-- EXPECTED, '' when there are no checks), as fetched; stale once a PR closes
ALTER TABLE prs ADD COLUMN check_state TEXT NOT NULL DEFAULT '';
`, `
-- the detail behind check_state, refreshed for every open PR on every fetch:
-- when the head commit's checks last finished, which ones fail, since when the
-- PR has been failing (kept across pushes while it stays red), and how far the
-- branch and its result have fallen behind the base. -1 is "not measured".
CREATE TABLE pr_ci (
  pr_number     INTEGER PRIMARY KEY,
  ran_at        TEXT NOT NULL DEFAULT '',
  failing       TEXT NOT NULL DEFAULT '[]',
  failing_since TEXT NOT NULL DEFAULT '',
  behind        INTEGER NOT NULL DEFAULT -1,
  ahead         INTEGER NOT NULL DEFAULT -1,
  drift         INTEGER NOT NULL DEFAULT -1,
  fetched_at    TEXT NOT NULL DEFAULT ''
);
`, `
-- how many of the head commit's workflows github is holding for a maintainer's
-- approval (a fork's first runs): while any are, the check state says nothing
ALTER TABLE pr_ci ADD COLUMN awaiting INTEGER NOT NULL DEFAULT 0;
`, `
-- the acceptance test builds teamcity ran on pull request branches, one row a
-- build: several a PR, one per service it was tested for and one per re-run.
-- failed_tests is filled in afterwards, for the builds that matter.
CREATE TABLE tc_builds (
  id           INTEGER PRIMARY KEY,
  pr_number    INTEGER NOT NULL,
  build_type   TEXT NOT NULL DEFAULT '',
  branch       TEXT NOT NULL DEFAULT '',
  state        TEXT NOT NULL DEFAULT '',
  status       TEXT NOT NULL DEFAULT '',
  status_text  TEXT NOT NULL DEFAULT '',
  passed       INTEGER NOT NULL DEFAULT 0,
  failed       INTEGER NOT NULL DEFAULT 0,
  ignored      INTEGER NOT NULL DEFAULT 0,
  started_at   TEXT NOT NULL DEFAULT '',
  finished_at  TEXT NOT NULL DEFAULT '',
  revision     TEXT NOT NULL DEFAULT '',
  url          TEXT NOT NULL DEFAULT '',
  failed_tests TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_tc_builds_pr ON tc_builds(pr_number, build_type, id);
`, `
-- the PR's head commit when last measured, and when its branch last caught up with its base (the merge base's
-- date): it changes only with the head, so it is asked again only then
ALTER TABLE pr_ci ADD COLUMN head_oid TEXT NOT NULL DEFAULT '';
ALTER TABLE pr_ci ADD COLUMN behind_since TEXT NOT NULL DEFAULT '';
`}

func (d *DB) migrate() error {
	var current int
	if err := d.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("reading user_version: %w", err)
	}

	for i := current; i < len(migrations); i++ {
		clog.Log.Debugf("applying db migration %d", i+1)
		tx, err := d.Begin()
		if err != nil {
			return fmt.Errorf("beginning migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("applying migration %d: %w", i+1, err)
		}
		// PRAGMA cannot be parameterised; the value is a loop index, not user input
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("setting user_version %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing migration %d: %w", i+1, err)
		}
	}

	return nil
}

// GetMeta returns the value for key, or "" when unset.
func (d *DB) GetMeta(key string) (string, error) {
	var v string
	err := d.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading meta %s: %w", key, err)
	}
	return v, nil
}

// SetMeta upserts a meta key.
func (d *DB) SetMeta(key, value string) error {
	if _, err := d.Exec("INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value); err != nil {
		return fmt.Errorf("writing meta %s: %w", key, err)
	}
	return nil
}

// DeleteMeta removes a meta key.
func (d *DB) DeleteMeta(key string) error {
	if _, err := d.Exec("DELETE FROM meta WHERE key = ?", key); err != nil {
		return fmt.Errorf("deleting meta %s: %w", key, err)
	}
	return nil
}

// Count returns the row count of one table. The name must come from a fixed
// in-code list, never user input.
func (d *DB) Count(table string) (int, error) {
	var n int
	if err := d.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting %s: %w", table, err)
	}
	return n, nil
}

// timestamps are stored as RFC3339 strings (UTC), "" for unset.

func toDBTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func fromDBTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		clog.Log.Debugf("unparsable timestamp %q in db", s)
		return time.Time{}
	}
	return t
}

// Now returns the current time in the storage format's precision.
func Now() time.Time {
	return time.Now().UTC().Truncate(time.Second)
}

// Snapshot writes a whole, consistent copy of the database to dest — a path
// that does not exist yet, or an empty file — while others go on reading and
// writing this one. The copy is one file, with nothing left in a journal.
func (d *DB) Snapshot(dest string) error {
	if _, err := d.Exec("VACUUM INTO ?", dest); err != nil {
		return fmt.Errorf("copying the database to %s: %w", dest, err)
	}
	return nil
}

// Inspect looks at a file someone handed over, without changing it, and says
// whether it can stand in as the database: sqlite, sound, written by this
// prawn or an older one (Open brings an older one up to date). It returns how
// many PRs it holds.
func Inspect(path string) (int, error) {
	sdb, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", path))
	if err != nil {
		return 0, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = sdb.Close() }()

	var version int
	if err := sdb.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("not a sqlite database: %w", err)
	}
	switch {
	case version == 0:
		return 0, errors.New("not a prawn database: it has no schema version")
	case version > len(migrations):
		return 0, fmt.Errorf("written by a newer prawn (schema %d, this one knows %d): update prawn first", version, len(migrations))
	}
	var sound string
	if err := sdb.QueryRow("PRAGMA quick_check").Scan(&sound); err != nil {
		return 0, fmt.Errorf("checking the file: %w", err)
	}
	if sound != "ok" {
		return 0, fmt.Errorf("the file is damaged: %s", sound)
	}
	var prs int
	if err := sdb.QueryRow("SELECT count(*) FROM prs").Scan(&prs); err != nil {
		return 0, fmt.Errorf("not a prawn database: %w", err)
	}
	return prs, nil
}
