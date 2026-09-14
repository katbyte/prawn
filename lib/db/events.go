package db

import (
	"database/sql"
	"fmt"
	"time"
)

// Event is one item of a PR's timeline: a label change, a review request, a
// commit, a review, a comment, a close... Type is GitHub's __typename
// (LabeledEvent, PullRequestCommit, IssueComment, ...); the typed columns
// hold what the derived stats read, Raw the whole node for anything else.
type Event struct {
	ID        string
	PRNumber  int
	Type      string
	Actor     string // who did it (the author, for reviews, comments, and commits)
	CreatedAt time.Time
	Label     string // the label added or removed
	Milestone string // Milestoned/DemilestonedEvent
	Subject   string // the reviewer requested, the assignee, the commit oid, the referencing #number, the new title
	State     string // a review's state
	Raw       string // the node as fetched, json
}

// Commit is one commit on a PR's head, as the timeline lists it.
type Commit struct {
	Oid         string
	PRNumber    int
	Author      string // github login, "" when the commit email matches no account
	AuthorName  string
	AuthoredAt  time.Time
	CommittedAt time.Time
	Additions   int
	Deletions   int
	Headline    string
}

// Timeline item type names, as GitHub's __typename reports them.
const (
	EventLabelled          = "LabeledEvent"
	EventUnlabelled        = "UnlabeledEvent"
	EventClosed            = "ClosedEvent"
	EventReopened          = "ReopenedEvent"
	EventMerged            = "MergedEvent"
	EventReviewRequested   = "ReviewRequestedEvent"
	EventReadyForReview    = "ReadyForReviewEvent"
	EventConvertToDraft    = "ConvertToDraftEvent"
	EventMilestoned        = "MilestonedEvent"
	EventDemilestoned      = "DemilestonedEvent"
	EventHeadRefForcePush  = "HeadRefForcePushedEvent"
	EventCommit            = "PullRequestCommit"
	EventReview            = "PullRequestReview"
	EventComment           = "IssueComment"
	EventCrossReferenced   = "CrossReferencedEvent"
	EventReviewDismissed   = "ReviewDismissedEvent"
	EventRenamedTitle      = "RenamedTitleEvent"
	EventAssigned          = "AssignedEvent"
	EventReviewRequestGone = "ReviewRequestRemovedEvent"
)

func insertEvents(tx *sql.Tx, number int, events []Event, commits []Commit) error {
	for _, e := range events {
		if _, err := tx.Exec(
			"INSERT OR REPLACE INTO events (id, pr_number, type, actor, created_at, label, milestone, subject, state, raw) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			e.ID, number, e.Type, e.Actor, toDBTime(e.CreatedAt), e.Label, e.Milestone, e.Subject, e.State, e.Raw); err != nil {
			return fmt.Errorf("inserting event on #%d: %w", number, err)
		}
	}
	for _, c := range commits {
		if _, err := tx.Exec(
			"INSERT OR REPLACE INTO commits (oid, pr_number, author, author_name, authored_at, committed_at, additions, deletions, headline) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			c.Oid, number, c.Author, c.AuthorName, toDBTime(c.AuthoredAt), toDBTime(c.CommittedAt), c.Additions, c.Deletions, c.Headline); err != nil {
			return fmt.Errorf("inserting commit on #%d: %w", number, err)
		}
	}
	return nil
}

// SaveEvents appends a later timeline page to a PR and records where the
// next page starts ("" once complete), in one transaction so an interrupted
// follow-up resumes from the last page that landed.
func (d *DB) SaveEvents(number int, events []Event, commits []Commit, nextCursor string) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("beginning events tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := insertEvents(tx, number, events, commits); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE prs SET events_cursor = ? WHERE number = ?", nextCursor, number); err != nil {
		return fmt.Errorf("saving events cursor for #%d: %w", number, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing events tx: %w", err)
	}
	return nil
}

// IncompleteTimelines lists PRs whose timeline has pages still to fetch, with
// the cursor each continues from.
func (d *DB) IncompleteTimelines() (map[int]string, error) {
	rows, err := d.Query("SELECT number, events_cursor FROM prs WHERE events_cursor != '' ORDER BY number")
	if err != nil {
		return nil, fmt.Errorf("finding incomplete timelines: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int]string{}
	for rows.Next() {
		var n int
		var c string
		if err := rows.Scan(&n, &c); err != nil {
			return nil, fmt.Errorf("scanning timeline cursor: %w", err)
		}
		out[n] = c
	}
	return out, rows.Err()
}

const eventCols = "id, pr_number, type, actor, created_at, label, milestone, subject, state, raw"

func scanEvents(rows *sql.Rows) ([]Event, error) {
	var events []Event
	for rows.Next() {
		var e Event
		var created string
		if err := rows.Scan(&e.ID, &e.PRNumber, &e.Type, &e.Actor, &created, &e.Label, &e.Milestone, &e.Subject, &e.State, &e.Raw); err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}
		e.CreatedAt = fromDBTime(created)
		events = append(events, e)
	}
	return events, rows.Err()
}

// EventsFor returns a PR's timeline oldest first.
func (d *DB) EventsFor(number int) ([]Event, error) {
	rows, err := d.Query("SELECT "+eventCols+" FROM events WHERE pr_number = ? ORDER BY created_at ASC, id ASC", number)
	if err != nil {
		return nil, fmt.Errorf("querying events for #%d: %w", number, err)
	}
	defer func() { _ = rows.Close() }()
	return scanEvents(rows)
}

// AllEvents returns every stored timeline item keyed by PR, each list oldest
// first — the explore page derives its stats from the whole set at once.
func (d *DB) AllEvents() (map[int][]Event, error) {
	rows, err := d.Query("SELECT " + eventCols + " FROM events ORDER BY pr_number, created_at ASC, id ASC")
	if err != nil {
		return nil, fmt.Errorf("querying events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	events, err := scanEvents(rows)
	if err != nil {
		return nil, err
	}
	out := map[int][]Event{}
	for _, e := range events {
		out[e.PRNumber] = append(out[e.PRNumber], e)
	}
	return out, nil
}

// AllCommits returns every stored commit keyed by PR, oldest first.
func (d *DB) AllCommits() (map[int][]Commit, error) {
	rows, err := d.Query("SELECT oid, pr_number, author, author_name, authored_at, committed_at, additions, deletions, headline FROM commits ORDER BY pr_number, committed_at ASC")
	if err != nil {
		return nil, fmt.Errorf("querying commits: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int][]Commit{}
	for rows.Next() {
		var c Commit
		var authored, committed string
		if err := rows.Scan(&c.Oid, &c.PRNumber, &c.Author, &c.AuthorName, &authored, &committed, &c.Additions, &c.Deletions, &c.Headline); err != nil {
			return nil, fmt.Errorf("scanning commit: %w", err)
		}
		c.AuthoredAt, c.CommittedAt = fromDBTime(authored), fromDBTime(committed)
		out[c.PRNumber] = append(out[c.PRNumber], c)
	}
	return out, rows.Err()
}

// AllReviews returns every stored review keyed by PR, oldest first.
func (d *DB) AllReviews() (map[int][]Review, error) {
	rows, err := d.Query("SELECT id, pr_number, author, author_association, state, submitted_at, body, url FROM reviews ORDER BY pr_number, submitted_at ASC")
	if err != nil {
		return nil, fmt.Errorf("querying reviews: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int][]Review{}
	for rows.Next() {
		var r Review
		var submitted string
		if err := rows.Scan(&r.ID, &r.PRNumber, &r.Author, &r.AuthorAssociation, &r.State, &submitted, &r.Body, &r.URL); err != nil {
			return nil, fmt.Errorf("scanning review: %w", err)
		}
		r.SubmittedAt = fromDBTime(submitted)
		out[r.PRNumber] = append(out[r.PRNumber], r)
	}
	return out, rows.Err()
}

// AllVerdicts returns every cached AI verdict keyed by PR then pass.
func (d *DB) AllVerdicts() (map[int]map[string]Verdict, error) {
	rows, err := d.Query("SELECT pr_number, pass, prompt_hash, model, verdict, confidence FROM ai_verdicts")
	if err != nil {
		return nil, fmt.Errorf("querying verdicts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int]map[string]Verdict{}
	for rows.Next() {
		var v Verdict
		if err := rows.Scan(&v.PRNumber, &v.Pass, &v.PromptHash, &v.Model, &v.Verdict, &v.Confidence); err != nil {
			return nil, fmt.Errorf("scanning verdict: %w", err)
		}
		if out[v.PRNumber] == nil {
			out[v.PRNumber] = map[string]Verdict{}
		}
		out[v.PRNumber][v.Pass] = v
	}
	return out, rows.Err()
}

// MaintainerLogins returns every login the fetched reviews and comments mark
// as MEMBER, OWNER, or COLLABORATOR — the fallback when no maintainers group
// is configured.
func (d *DB) MaintainerLogins() ([]string, error) {
	rows, err := d.Query(`
		SELECT DISTINCT lower(author) FROM reviews WHERE author_association IN ('MEMBER', 'OWNER', 'COLLABORATOR')
		UNION SELECT DISTINCT lower(author) FROM comments WHERE author_association IN ('MEMBER', 'OWNER', 'COLLABORATOR')
		ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("querying maintainer logins: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, fmt.Errorf("scanning login: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
