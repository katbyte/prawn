package db

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/katbyte/go-kt/clog"
)

// CI is the detail behind a PR's check state: what the open-set reconcile
// measures for every open PR on every fetch.
type CI struct {
	PRNumber     int
	RanAt        time.Time // when the head commit's checks last finished; zero when it has none
	Awaiting     int       // workflows held until a maintainer approves them
	Failing      []string  // the checks failing on the head commit
	FailingSince time.Time // when the PR was first seen failing, kept across pushes while it stays red
	Behind       int       // commits on the base branch the PR's branch lacks, -1 unmeasured
	Ahead        int       // commits on the PR's branch the base lacks, -1 unmeasured
	Drift        int       // commits the base has had since the checks last ran, -1 unmeasured
	FetchedAt    time.Time
}

// SaveCI records the measured CI detail of open PRs. failing says which of
// them are red: a PR already recorded as failing keeps the time it was first
// seen so, so a push that fails again does not restart the clock; one newly
// red starts it at its checks' time (or now, without one); one no longer red
// clears it.
func (d *DB) SaveCI(cis []CI, failing map[int]bool) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("beginning ci tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := Now()
	for i := range cis {
		c := &cis[i]
		names := c.Failing
		if names == nil {
			names = []string{}
		}
		list, err := json.Marshal(names)
		if err != nil {
			return fmt.Errorf("encoding failing checks of #%d: %w", c.PRNumber, err)
		}
		since := ""
		if failing[c.PRNumber] {
			var known string
			_ = tx.QueryRow("SELECT failing_since FROM pr_ci WHERE pr_number = ?", c.PRNumber).Scan(&known) // no row: never seen failing
			switch {
			case known != "":
				since = known
			case !c.RanAt.IsZero():
				since = toDBTime(c.RanAt)
			default:
				since = toDBTime(now)
			}
		}
		if _, err := tx.Exec(`
			INSERT INTO pr_ci (pr_number, ran_at, failing, failing_since, behind, ahead, drift, fetched_at, awaiting)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(pr_number) DO UPDATE SET
				ran_at = excluded.ran_at, failing = excluded.failing, failing_since = excluded.failing_since,
				behind = excluded.behind, ahead = excluded.ahead, drift = excluded.drift, fetched_at = excluded.fetched_at,
				awaiting = excluded.awaiting`,
			c.PRNumber, toDBTime(c.RanAt), string(list), since, c.Behind, c.Ahead, c.Drift, toDBTime(now), c.Awaiting); err != nil {
			return fmt.Errorf("saving ci of #%d: %w", c.PRNumber, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing ci tx: %w", err)
	}
	return nil
}

// AllCI returns the stored CI detail by PR number.
func (d *DB) AllCI() (map[int]CI, error) {
	rows, err := d.Query("SELECT pr_number, ran_at, failing, failing_since, behind, ahead, drift, fetched_at, awaiting FROM pr_ci")
	if err != nil {
		return nil, fmt.Errorf("reading ci: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int]CI{}
	for rows.Next() {
		var c CI
		var ranAt, failing, since, fetchedAt string
		if err := rows.Scan(&c.PRNumber, &ranAt, &failing, &since, &c.Behind, &c.Ahead, &c.Drift, &fetchedAt, &c.Awaiting); err != nil {
			return nil, fmt.Errorf("scanning ci: %w", err)
		}
		if err := json.Unmarshal([]byte(failing), &c.Failing); err != nil {
			clog.Log.Debugf("unparsable failing checks for #%d: %v", c.PRNumber, err)
		}
		c.RanAt, c.FailingSince, c.FetchedAt = fromDBTime(ranAt), fromDBTime(since), fromDBTime(fetchedAt)
		out[c.PRNumber] = c
	}
	return out, rows.Err()
}
