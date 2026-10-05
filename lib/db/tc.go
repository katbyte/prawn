package db

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/katbyte/go-kt/clog"
)

// TCBuild is one acceptance test build TeamCity ran on a PR's branch.
type TCBuild struct {
	ID          int
	PRNumber    int
	BuildType   string // the build configuration: one per service
	Branch      string
	State       string // queued | running | finished
	Status      string // SUCCESS | FAILURE | UNKNOWN
	StatusText  string
	Passed      int
	Failed      int
	Ignored     int
	StartedAt   time.Time
	FinishedAt  time.Time
	Revision    string
	URL         string
	FailedTests []string // nil until looked up: only the builds that matter are
}

// SaveTCBuilds upserts builds as TeamCity reports them now — a running build
// seen again has finished, or moved on. The failed test names already looked
// up for a build are kept.
func (d *DB) SaveTCBuilds(builds []TCBuild) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("beginning teamcity tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for i := range builds {
		b := &builds[i]
		if _, err := tx.Exec(`
			INSERT INTO tc_builds (id, pr_number, build_type, branch, state, status, status_text, passed, failed, ignored, started_at, finished_at, revision, url)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				state = excluded.state, status = excluded.status, status_text = excluded.status_text,
				passed = excluded.passed, failed = excluded.failed, ignored = excluded.ignored,
				started_at = excluded.started_at, finished_at = excluded.finished_at, revision = excluded.revision, url = excluded.url`,
			b.ID, b.PRNumber, b.BuildType, b.Branch, b.State, b.Status, b.StatusText, b.Passed, b.Failed, b.Ignored,
			toDBTime(b.StartedAt), toDBTime(b.FinishedAt), b.Revision, b.URL); err != nil {
			return fmt.Errorf("saving teamcity build %d: %w", b.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing teamcity tx: %w", err)
	}
	return nil
}

// SetTCFailedTests records the names of the tests that failed in a build.
func (d *DB) SetTCFailedTests(buildID int, names []string) error {
	if names == nil {
		names = []string{}
	}
	list, err := json.Marshal(names)
	if err != nil {
		return fmt.Errorf("encoding the failed tests of build %d: %w", buildID, err)
	}
	if _, err := d.Exec("UPDATE tc_builds SET failed_tests = ? WHERE id = ?", string(list), buildID); err != nil {
		return fmt.Errorf("saving the failed tests of build %d: %w", buildID, err)
	}
	return nil
}

// TCBuildsWantingTests lists the finished builds whose failed tests are worth
// naming and have not been: the latest build of each service on each open PR,
// where that build failed tests.
func (d *DB) TCBuildsWantingTests() ([]int, error) {
	rows, err := d.Query(`
		SELECT b.id FROM tc_builds b
		JOIN prs p ON p.number = b.pr_number AND p.state = ?
		WHERE b.state = 'finished' AND b.failed > 0 AND b.failed_tests = ''
		  AND b.id = (SELECT MAX(id) FROM tc_builds WHERE pr_number = b.pr_number AND build_type = b.build_type)
		ORDER BY b.id DESC`, PROpen)
	if err != nil {
		return nil, fmt.Errorf("listing builds wanting failed tests: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning build id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// AllTCBuilds returns every stored build by PR number, oldest first.
func (d *DB) AllTCBuilds() (map[int][]TCBuild, error) {
	rows, err := d.Query(`SELECT id, pr_number, build_type, branch, state, status, status_text, passed, failed, ignored,
		started_at, finished_at, revision, url, failed_tests FROM tc_builds ORDER BY pr_number, id`)
	if err != nil {
		return nil, fmt.Errorf("reading teamcity builds: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int][]TCBuild{}
	for rows.Next() {
		var b TCBuild
		var started, finished, failedTests string
		if err := rows.Scan(&b.ID, &b.PRNumber, &b.BuildType, &b.Branch, &b.State, &b.Status, &b.StatusText, &b.Passed, &b.Failed, &b.Ignored,
			&started, &finished, &b.Revision, &b.URL, &failedTests); err != nil {
			return nil, fmt.Errorf("scanning teamcity build: %w", err)
		}
		b.StartedAt, b.FinishedAt = fromDBTime(started), fromDBTime(finished)
		if failedTests != "" {
			if err := json.Unmarshal([]byte(failedTests), &b.FailedTests); err != nil {
				clog.Log.Debugf("unparsable failed tests for build %d: %v", b.ID, err)
			}
		}
		out[b.PRNumber] = append(out[b.PRNumber], b)
	}
	return out, rows.Err()
}
