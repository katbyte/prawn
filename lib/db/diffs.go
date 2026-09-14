package db

import (
	"fmt"
	"time"
)

// Diffs: each open PR's unified diff, fetched once per PR update. The checks
// that ask what a PR actually changes read these rather than guess from the
// title and body.

// SaveDiff stores a PR's diff against the PR update it was fetched for.
func (d *DB) SaveDiff(number int, updatedAt time.Time, diff string) error {
	if _, err := d.Exec(`
		INSERT INTO diffs (pr_number, updated_at, diff) VALUES (?, ?, ?)
		ON CONFLICT(pr_number) DO UPDATE SET updated_at = excluded.updated_at, diff = excluded.diff`,
		number, toDBTime(updatedAt), diff); err != nil {
		return fmt.Errorf("saving diff for #%d: %w", number, err)
	}
	return nil
}

// AllDiffs returns every stored diff by PR number (empty strings for diffs
// GitHub would not render).
func (d *DB) AllDiffs() (map[int]string, error) {
	rows, err := d.Query("SELECT pr_number, diff FROM diffs")
	if err != nil {
		return nil, fmt.Errorf("reading diffs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int]string{}
	for rows.Next() {
		var n int
		var diff string
		if err := rows.Scan(&n, &diff); err != nil {
			return nil, fmt.Errorf("scanning diff: %w", err)
		}
		out[n] = diff
	}
	return out, rows.Err()
}

// StaleDiffs lists the open PRs whose diff is missing or predates the PR's
// last update.
func (d *DB) StaleDiffs() ([]int, error) {
	rows, err := d.Query(`
		SELECT p.number FROM prs p LEFT JOIN diffs x ON x.pr_number = p.number
		WHERE p.state = ? AND (x.pr_number IS NULL OR x.updated_at != p.updated_at)
		ORDER BY p.number`, PROpen)
	if err != nil {
		return nil, fmt.Errorf("finding stale diffs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("scanning stale diff: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
