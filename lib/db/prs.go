package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/katbyte/go-kt/clog"
)

// PR states as GitHub's GraphQL API reports them.
const (
	PROpen   = "OPEN"
	PRClosed = "CLOSED"
	PRMerged = "MERGED"
)

// Mergeable states as GitHub's GraphQL API reports them.
const (
	MergeableConflicting = "CONFLICTING"
)

// Review decisions and review states.
const (
	DecisionApproved         = "APPROVED"
	DecisionChangesRequested = "CHANGES_REQUESTED"
	ReviewChangesRequested   = "CHANGES_REQUESTED"
)

type PR struct {
	Number            int
	Title             string
	Body              string
	State             string // OPEN | CLOSED | MERGED
	IsDraft           bool
	Author            string
	AuthorAssociation string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ClosedAt          time.Time
	MergedAt          time.Time
	Labels            []string
	Mergeable         string
	ReviewDecision    string
	Additions         int
	Deletions         int
	ChangedFiles      int
	Files             []string
	CommentCount      int
	ThumbsUp          int
	LastCommitAt      time.Time
	URL               string
	FetchedAt         time.Time

	MergedBy   string
	Milestone  string
	BaseRef    string
	HeadRef    string
	CheckState string // the head commit's combined CI state: SUCCESS | FAILURE | ERROR | PENDING | EXPECTED | ""
	// EventsCursor is non-empty while the timeline has pages past the first
	// still to fetch; "" once the stored events are complete.
	EventsCursor string
}

// HasLabel reports whether the PR carries the exact label name.
func (p *PR) HasLabel(name string) bool {
	return slices.Contains(p.Labels, name)
}

type Comment struct {
	ID                string
	PRNumber          int
	Author            string
	AuthorAssociation string
	CreatedAt         time.Time
	Body              string
	URL               string
}

// IsMaintainer reports whether the comment author is a repo maintainer
// (member of the org or outside collaborator with push access).
func (c *Comment) IsMaintainer() bool {
	return maintainerAssoc(c.AuthorAssociation)
}

type Review struct {
	ID                string
	PRNumber          int
	Author            string
	AuthorAssociation string
	State             string // APPROVED | CHANGES_REQUESTED | COMMENTED | DISMISSED
	SubmittedAt       time.Time
	Body              string
	URL               string
}

// IsMaintainer reports whether the review author is a repo maintainer.
func (r *Review) IsMaintainer() bool {
	return maintainerAssoc(r.AuthorAssociation)
}

func maintainerAssoc(assoc string) bool {
	return assoc == "MEMBER" || assoc == "OWNER" || assoc == "COLLABORATOR"
}

// LinkedIssue is one issue a PR's closing keywords reference ("fixes #N").
type LinkedIssue struct {
	PRNumber    int
	IssueNumber int
	Title       string
	State       string // OPEN | CLOSED
	StateReason string
	Labels      []string
}

// HasLabel reports whether the linked issue carries the exact label name.
func (l *LinkedIssue) HasLabel(name string) bool {
	return slices.Contains(l.Labels, name)
}

// PRBundle is everything fetched for one PR in one page.
type PRBundle struct {
	PR       PR
	Comments []Comment
	Reviews  []Review
	Closes   []LinkedIssue
	Events   []Event  // the first timeline page; PR.EventsCursor says whether more follow
	Commits  []Commit // the commits among those events
}

// SavePRs writes a page of PRs (with their comments, reviews, and linked
// issues) and the fetch cursor in a single transaction, so an interrupted
// fetch resumes cleanly.
func (d *DB) SavePRs(bundles []PRBundle, cursorKey, cursorVal string) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("beginning save tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for i := range bundles {
		if err := saveBundle(tx, &bundles[i]); err != nil {
			return err
		}
	}

	if cursorKey != "" {
		if _, err := tx.Exec("INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", cursorKey, cursorVal); err != nil {
			return fmt.Errorf("saving cursor: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing save tx: %w", err)
	}
	return nil
}

func saveBundle(tx *sql.Tx, b *PRBundle) error {
	p := &b.PR
	labels, err := json.Marshal(p.Labels)
	if err != nil {
		return fmt.Errorf("marshalling labels for #%d: %w", p.Number, err)
	}
	files, err := json.Marshal(p.Files)
	if err != nil {
		return fmt.Errorf("marshalling files for #%d: %w", p.Number, err)
	}

	_, err = tx.Exec(`
		INSERT INTO prs (number, title, body, state, is_draft, author, author_association,
			created_at, updated_at, closed_at, merged_at, labels, mergeable, review_decision,
			additions, deletions, changed_files, files, comment_count, thumbs_up, last_commit_at, url, fetched_at,
			merged_by, milestone, base_ref, head_ref, events_cursor, check_state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(number) DO UPDATE SET
			merged_by = excluded.merged_by, milestone = excluded.milestone, base_ref = excluded.base_ref,
			head_ref = excluded.head_ref, events_cursor = excluded.events_cursor, check_state = excluded.check_state,
			title = excluded.title, body = excluded.body, state = excluded.state,
			is_draft = excluded.is_draft, author = excluded.author,
			author_association = excluded.author_association, created_at = excluded.created_at,
			updated_at = excluded.updated_at, closed_at = excluded.closed_at, merged_at = excluded.merged_at,
			labels = excluded.labels, mergeable = excluded.mergeable, review_decision = excluded.review_decision,
			additions = excluded.additions, deletions = excluded.deletions, changed_files = excluded.changed_files,
			files = excluded.files, comment_count = excluded.comment_count, thumbs_up = excluded.thumbs_up,
			last_commit_at = excluded.last_commit_at, url = excluded.url, fetched_at = excluded.fetched_at`,
		p.Number, p.Title, p.Body, p.State, boolToInt(p.IsDraft), p.Author, p.AuthorAssociation,
		toDBTime(p.CreatedAt), toDBTime(p.UpdatedAt), toDBTime(p.ClosedAt), toDBTime(p.MergedAt), string(labels),
		p.Mergeable, p.ReviewDecision, p.Additions, p.Deletions, p.ChangedFiles, string(files),
		p.CommentCount, p.ThumbsUp, toDBTime(p.LastCommitAt), p.URL, toDBTime(p.FetchedAt),
		p.MergedBy, p.Milestone, p.BaseRef, p.HeadRef, p.EventsCursor, p.CheckState)
	if err != nil {
		return fmt.Errorf("upserting PR #%d: %w", p.Number, err)
	}

	// the timeline is replaced from its first page; later pages append via
	// SaveEvents, and a PR whose events_cursor is set is not complete yet
	if _, err := tx.Exec("DELETE FROM events WHERE pr_number = ?", p.Number); err != nil {
		return fmt.Errorf("clearing events for #%d: %w", p.Number, err)
	}
	if _, err := tx.Exec("DELETE FROM commits WHERE pr_number = ?", p.Number); err != nil {
		return fmt.Errorf("clearing commits for #%d: %w", p.Number, err)
	}
	if err := insertEvents(tx, p.Number, b.Events, b.Commits); err != nil {
		return err
	}

	// comments/reviews/closes are replaced wholesale so deletions and edits upstream are reflected
	if _, err := tx.Exec("DELETE FROM comments WHERE pr_number = ?", p.Number); err != nil {
		return fmt.Errorf("clearing comments for #%d: %w", p.Number, err)
	}
	for _, c := range b.Comments {
		if _, err := tx.Exec(
			"INSERT OR REPLACE INTO comments (id, pr_number, author, author_association, created_at, body, url) VALUES (?, ?, ?, ?, ?, ?, ?)",
			c.ID, p.Number, c.Author, c.AuthorAssociation, toDBTime(c.CreatedAt), c.Body, c.URL); err != nil {
			return fmt.Errorf("inserting comment on #%d: %w", p.Number, err)
		}
	}

	if _, err := tx.Exec("DELETE FROM reviews WHERE pr_number = ?", p.Number); err != nil {
		return fmt.Errorf("clearing reviews for #%d: %w", p.Number, err)
	}
	for _, r := range b.Reviews {
		if _, err := tx.Exec(
			"INSERT OR REPLACE INTO reviews (id, pr_number, author, author_association, state, submitted_at, body, url) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			r.ID, p.Number, r.Author, r.AuthorAssociation, r.State, toDBTime(r.SubmittedAt), r.Body, r.URL); err != nil {
			return fmt.Errorf("inserting review on #%d: %w", p.Number, err)
		}
	}

	if _, err := tx.Exec("DELETE FROM closes WHERE pr_number = ?", p.Number); err != nil {
		return fmt.Errorf("clearing closes for #%d: %w", p.Number, err)
	}
	for _, l := range b.Closes {
		ilabels, merr := json.Marshal(l.Labels)
		if merr != nil {
			return fmt.Errorf("marshalling issue labels for #%d: %w", p.Number, merr)
		}
		if _, err := tx.Exec(
			"INSERT OR REPLACE INTO closes (pr_number, issue_number, title, state, state_reason, labels) VALUES (?, ?, ?, ?, ?, ?)",
			p.Number, l.IssueNumber, l.Title, l.State, l.StateReason, string(ilabels)); err != nil {
			return fmt.Errorf("inserting linked issue on #%d: %w", p.Number, err)
		}
	}

	return nil
}

const prCols = `number, title, body, state, is_draft, author, author_association,
	created_at, updated_at, closed_at, merged_at, labels, mergeable, review_decision,
	additions, deletions, changed_files, files, comment_count, thumbs_up, last_commit_at, url, fetched_at,
	merged_by, milestone, base_ref, head_ref, events_cursor, check_state`

func scanPR(row interface{ Scan(...any) error }) (*PR, error) {
	var p PR
	var isDraft int
	var created, updated, closed, merged, lastCommit, fetched, labels, files string
	if err := row.Scan(&p.Number, &p.Title, &p.Body, &p.State, &isDraft, &p.Author, &p.AuthorAssociation,
		&created, &updated, &closed, &merged, &labels, &p.Mergeable, &p.ReviewDecision,
		&p.Additions, &p.Deletions, &p.ChangedFiles, &files, &p.CommentCount, &p.ThumbsUp, &lastCommit, &p.URL, &fetched,
		&p.MergedBy, &p.Milestone, &p.BaseRef, &p.HeadRef, &p.EventsCursor, &p.CheckState); err != nil {
		return nil, err
	}
	p.IsDraft = isDraft != 0
	p.CreatedAt, p.UpdatedAt, p.ClosedAt, p.MergedAt = fromDBTime(created), fromDBTime(updated), fromDBTime(closed), fromDBTime(merged)
	p.LastCommitAt, p.FetchedAt = fromDBTime(lastCommit), fromDBTime(fetched)
	if err := json.Unmarshal([]byte(labels), &p.Labels); err != nil {
		clog.Log.Debugf("unparseable labels for #%d: %v", p.Number, err)
	}
	if err := json.Unmarshal([]byte(files), &p.Files); err != nil {
		clog.Log.Debugf("unparseable files for #%d: %v", p.Number, err)
	}
	return &p, nil
}

// GetPR returns the PR or nil when it isn't in the db.
func (d *DB) GetPR(number int) (*PR, error) {
	row := d.QueryRow("SELECT "+prCols+" FROM prs WHERE number = ?", number)
	p, err := scanPR(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading PR #%d: %w", number, err)
	}
	return p, nil
}

// OpenPRs returns all open PRs ordered oldest first.
func (d *DB) OpenPRs() ([]*PR, error) {
	rows, err := d.Query("SELECT " + prCols + " FROM prs WHERE state = 'OPEN' ORDER BY number ASC")
	if err != nil {
		return nil, fmt.Errorf("querying open PRs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var prs []*PR
	for rows.Next() {
		p, err := scanPR(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning PR: %w", err)
		}
		prs = append(prs, p)
	}
	return prs, rows.Err()
}

// PRsSince returns every PR that was open at any point on or after since:
// the open set plus everything closed or merged since then, oldest first.
// This is the explore page's population.
func (d *DB) PRsSince(since time.Time) ([]*PR, error) {
	rows, err := d.Query("SELECT "+prCols+" FROM prs WHERE state = 'OPEN' OR closed_at >= ? OR merged_at >= ? ORDER BY number ASC",
		toDBTime(since), toDBTime(since))
	if err != nil {
		return nil, fmt.Errorf("querying PRs since %s: %w", since.Format("2006-01-02"), err)
	}
	defer func() { _ = rows.Close() }()

	var prs []*PR
	for rows.Next() {
		p, err := scanPR(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning PR: %w", err)
		}
		prs = append(prs, p)
	}
	return prs, rows.Err()
}

// CommentsFor returns a PR's conversation comments ordered oldest first.
func (d *DB) CommentsFor(number int) ([]Comment, error) {
	rows, err := d.Query(
		"SELECT id, pr_number, author, author_association, created_at, body, url FROM comments WHERE pr_number = ? ORDER BY created_at ASC", number)
	if err != nil {
		return nil, fmt.Errorf("querying comments for #%d: %w", number, err)
	}
	defer func() { _ = rows.Close() }()

	var comments []Comment
	for rows.Next() {
		var c Comment
		var created string
		if err := rows.Scan(&c.ID, &c.PRNumber, &c.Author, &c.AuthorAssociation, &created, &c.Body, &c.URL); err != nil {
			return nil, fmt.Errorf("scanning comment: %w", err)
		}
		c.CreatedAt = fromDBTime(created)
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

// ReviewsFor returns a PR's reviews ordered oldest first.
func (d *DB) ReviewsFor(number int) ([]Review, error) {
	rows, err := d.Query(
		"SELECT id, pr_number, author, author_association, state, submitted_at, body, url FROM reviews WHERE pr_number = ? ORDER BY submitted_at ASC", number)
	if err != nil {
		return nil, fmt.Errorf("querying reviews for #%d: %w", number, err)
	}
	defer func() { _ = rows.Close() }()

	var reviews []Review
	for rows.Next() {
		var r Review
		var submitted string
		if err := rows.Scan(&r.ID, &r.PRNumber, &r.Author, &r.AuthorAssociation, &r.State, &submitted, &r.Body, &r.URL); err != nil {
			return nil, fmt.Errorf("scanning review: %w", err)
		}
		r.SubmittedAt = fromDBTime(submitted)
		reviews = append(reviews, r)
	}
	return reviews, rows.Err()
}

// ClosesFor returns the issues a PR's closing keywords reference.
func (d *DB) ClosesFor(number int) ([]LinkedIssue, error) {
	rows, err := d.Query(
		"SELECT pr_number, issue_number, title, state, state_reason, labels FROM closes WHERE pr_number = ? ORDER BY issue_number ASC", number)
	if err != nil {
		return nil, fmt.Errorf("querying linked issues for #%d: %w", number, err)
	}
	defer func() { _ = rows.Close() }()

	var links []LinkedIssue
	for rows.Next() {
		var l LinkedIssue
		var labels string
		if err := rows.Scan(&l.PRNumber, &l.IssueNumber, &l.Title, &l.State, &l.StateReason, &labels); err != nil {
			return nil, fmt.Errorf("scanning linked issue: %w", err)
		}
		if err := json.Unmarshal([]byte(labels), &l.Labels); err != nil {
			clog.Log.Debugf("unparseable issue labels for #%d: %v", l.PRNumber, err)
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

// AllCloses returns every PR→issue closing link, keyed by PR number — the
// duplicate check reads the whole map at once.
func (d *DB) AllCloses() (map[int][]LinkedIssue, error) {
	rows, err := d.Query("SELECT pr_number, issue_number, title, state, state_reason, labels FROM closes ORDER BY pr_number, issue_number")
	if err != nil {
		return nil, fmt.Errorf("querying linked issues: %w", err)
	}
	defer func() { _ = rows.Close() }()

	links := map[int][]LinkedIssue{}
	for rows.Next() {
		var l LinkedIssue
		var labels string
		if err := rows.Scan(&l.PRNumber, &l.IssueNumber, &l.Title, &l.State, &l.StateReason, &labels); err != nil {
			return nil, fmt.Errorf("scanning linked issue: %w", err)
		}
		if err := json.Unmarshal([]byte(labels), &l.Labels); err != nil {
			clog.Log.Debugf("unparseable issue labels for #%d: %v", l.PRNumber, err)
		}
		links[l.PRNumber] = append(links[l.PRNumber], l)
	}
	return links, rows.Err()
}

// PRStates returns every known PR number and its state — the local half of the
// open-set reconcile against github.
func (d *DB) PRStates() (map[int]string, error) {
	rows, err := d.Query("SELECT number, state FROM prs")
	if err != nil {
		return nil, fmt.Errorf("querying PR states: %w", err)
	}
	defer func() { _ = rows.Close() }()

	states := map[int]string{}
	for rows.Next() {
		var number int
		var state string
		if err := rows.Scan(&number, &state); err != nil {
			return nil, fmt.Errorf("scanning PR state: %w", err)
		}
		states[number] = state
	}
	return states, rows.Err()
}

// MarkPRsClosed flips local state to CLOSED for PRs the reconcile found gone
// from github's open set (merged ones the sync already saw stay MERGED).
func (d *DB) MarkPRsClosed(numbers []int) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("beginning reconcile tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, n := range numbers {
		if _, err := tx.Exec("UPDATE prs SET state = ? WHERE number = ? AND state = ?", PRClosed, n, PROpen); err != nil {
			return fmt.Errorf("marking #%d closed: %w", n, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing reconcile tx: %w", err)
	}
	return nil
}

// PRStatus is a PR's mergeability and CI state, as the open-set reconcile
// refreshes them.
type PRStatus struct {
	Mergeable  string
	CheckState string
}

// UpdateOpenStatuses writes fresh mergeability and CI states onto locally
// open PRs, and returns how many rows changed. Both move without the PR's
// updated_at moving, so the incremental sync never sees them; the reconcile
// walk does, on every fetch.
func (d *DB) UpdateOpenStatuses(statuses map[int]PRStatus) (int, error) {
	tx, err := d.Begin()
	if err != nil {
		return 0, fmt.Errorf("beginning status tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	changed := 0
	for n, s := range statuses {
		res, err := tx.Exec("UPDATE prs SET mergeable = ?, check_state = ? WHERE number = ? AND state = ? AND (mergeable != ? OR check_state != ?)",
			s.Mergeable, s.CheckState, n, PROpen, s.Mergeable, s.CheckState)
		if err != nil {
			return 0, fmt.Errorf("updating status of #%d: %w", n, err)
		}
		if rows, _ := res.RowsAffected(); rows > 0 {
			changed++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing status tx: %w", err)
	}
	return changed, nil
}

// CountPRs returns total and open PR counts.
func (d *DB) CountPRs() (total, open int, err error) {
	if err = d.QueryRow("SELECT COUNT(*), COALESCE(SUM(state = 'OPEN'), 0) FROM prs").Scan(&total, &open); err != nil {
		return 0, 0, fmt.Errorf("counting PRs: %w", err)
	}
	return total, open, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
