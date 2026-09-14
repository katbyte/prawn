// The fetch: every open PR — title, body, comments, reviews, changed files,
// and the issues its closing keywords reference — via the GraphQL API into
// the local database. The first run walks everything (resumable); later runs
// sync incrementally via the search API and reconcile the open set against
// the repository connection (search's index lags; the connection is ground
// truth).

package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/katbyte/go-kt/cout"
	"github.com/katbyte/prawn/lib/db"
	"github.com/katbyte/prawn/lib/gh"
	"github.com/katbyte/prawn/lib/text"
)

// meta keys the fetch records its progress under.
const (
	metaWalkCursor = "pr_walk_cursor"
	metaWalkDone   = "pr_walk_done"
	metaLastSync   = "pr_last_sync"
)

// syncOverlap is re-fetched behind the last sync point so edits that landed
// while the previous sync ran are never missed.
const syncOverlap = 5 * time.Minute

// autoFetchTTL is how fresh the local db must be for a check to skip the
// pre-run sync.
const autoFetchTTL = time.Hour

// Fetch fills the local database: a resumable full walk the first time (or
// with full), an incremental search-based sync after, and an open-set
// reconcile either way.
func (f *FlagData) Fetch(full bool) error {
	d, err := f.OpenDB()
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return f.fetch(d, full)
}

// AutoFetch keeps a check honest: sync before scanning unless the local db is
// fresh or --no-auto-fetch says to run against it as-is.
func (f *FlagData) AutoFetch() error {
	d, err := f.OpenDB()
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()

	last, err := d.GetMeta(metaLastSync)
	if err != nil {
		return err
	}
	if last != "" {
		if t, terr := time.Parse(time.RFC3339, last); terr == nil && time.Since(t) < autoFetchTTL {
			return nil
		}
	}
	return f.fetch(d, false)
}

func (f *FlagData) fetch(d *db.DB, full bool) error {
	owner, name, err := f.RepoOwnerName()
	if err != nil {
		return err
	}
	client := f.NewGraphQL()

	done, err := d.GetMeta(metaWalkDone)
	if err != nil {
		return err
	}
	if full {
		// a forced full walk starts over: cursor and done flag reset together
		if err := d.DeleteMeta(metaWalkCursor); err != nil {
			return err
		}
		done = ""
	}

	start := db.Now()
	if done != "1" {
		if err := f.walkPRs(d, client, owner, name); err != nil {
			return err
		}
	} else {
		if err := f.syncPRs(d, client, owner, name); err != nil {
			return err
		}
	}
	if err := f.reconcile(d, client, owner, name); err != nil {
		return err
	}
	if err := f.syncDiffs(d); err != nil {
		return err
	}
	// the sync point is when this run STARTED — anything that changed since
	// gets picked up (again) next time
	if err := d.SetMeta(metaLastSync, start.Format(time.RFC3339)); err != nil {
		return err
	}

	total, open, err := d.CountPRs()
	if err != nil {
		return err
	}
	cout.Printf("<green>fetch complete:</> %d PRs known, <yellow>%d</> open\n", total, open)
	return nil
}

// walkPRs is the resumable full walk over open PRs, oldest first.
func (f *FlagData) walkPRs(d *db.DB, client *gh.Client, owner, name string) error {
	cursor, err := d.GetMeta(metaWalkCursor)
	if err != nil {
		return err
	}
	if cursor != "" {
		cout.Printf("resuming the open-PR walk of %s from the saved cursor...\n", f.RepoTag())
	} else {
		cout.Printf("walking every open PR of %s (comments, reviews, files, linked issues included)...\n", f.RepoTag())
	}

	fetched := 0
	for {
		page, err := client.OpenPRs(owner, name, cursor)
		if err != nil {
			return err
		}
		if err := d.SavePRs(bundles(page.PRs), metaWalkCursor, page.PageInfo.EndCursor); err != nil {
			return err
		}
		fetched += len(page.PRs)
		cout.Printf("  <gray>%d/%d fetched · rate limit: %d remaining</>\n", fetched, page.TotalCount, page.RateLimit.Remaining)
		page.RateLimit.WaitIfLow()
		if !page.PageInfo.HasNextPage {
			break
		}
		cursor = page.PageInfo.EndCursor
	}

	return d.SetMeta(metaWalkDone, "1")
}

// syncPRs pulls every PR (any state) updated since the last sync. The search
// API caps results at 1000 — beyond that a full walk is cheaper anyway.
func (f *FlagData) syncPRs(d *db.DB, client *gh.Client, owner, name string) error {
	last, err := d.GetMeta(metaLastSync)
	if err != nil {
		return err
	}
	since := time.Time{}
	if t, terr := time.Parse(time.RFC3339, last); terr == nil {
		since = t.Add(-syncOverlap)
	}
	cout.Printf("syncing PRs of %s updated since <yellow>%s</>...\n", f.RepoTag(), since.Format(time.RFC3339))

	cursor, fetched := "", 0
	for {
		page, err := client.UpdatedPRs(owner, name, since, cursor)
		if err != nil {
			return err
		}
		if page.PRCount > 900 {
			cout.Printf("<yellow>%d PRs updated since the last sync — the search cap looms, re-walking instead</>\n", page.PRCount)
			if err := d.DeleteMeta(metaWalkCursor); err != nil {
				return err
			}
			return f.walkPRs(d, client, owner, name)
		}
		if err := d.SavePRs(bundles(page.PRs), "", ""); err != nil {
			return err
		}
		fetched += len(page.PRs)
		cout.Printf("  <gray>%d/%d synced · rate limit: %d remaining</>\n", fetched, page.PRCount, page.RateLimit.Remaining)
		page.RateLimit.WaitIfLow()
		if !page.PageInfo.HasNextPage {
			return nil
		}
		cursor = page.PageInfo.EndCursor
	}
}

// reconcile pages just the numbers of github's real open set and closes local
// rows that fell out of it — catches closes the search-index lag hides.
func (f *FlagData) reconcile(d *db.DB, client *gh.Client, owner, name string) error {
	open, err := client.OpenPRNumbers(owner, name, func(fetched, total int) {
		cout.Verbosef("  <gray>reconcile: %d/%d open PR numbers</>\n", fetched, total)
	})
	if err != nil {
		return err
	}

	states, err := d.PRStates()
	if err != nil {
		return err
	}
	var gone []int
	for number, state := range states {
		if state == db.PROpen && !open[number] {
			gone = append(gone, number)
		}
	}
	if len(gone) > 0 {
		cout.Printf("  <gray>reconcile: %d locally-open PRs are no longer open on github — marked closed</>\n", len(gone))
		return d.MarkPRsClosed(gone)
	}
	return nil
}

// diffMaxBytes caps a stored diff — a 30k-line API bump is noise past the
// first few hundred KB, and the checks match on identifiers, not bulk.
const diffMaxBytes = 512 << 10

// diffThrottle spaces the per-PR REST calls the diff sync makes.
const diffThrottle = 150 * time.Millisecond

// syncDiffs fetches the unified diff of every open PR whose diff is missing or
// predates the PR's last update — one REST call each, saved as it goes so an
// interrupted run resumes where it stopped. Diffs GitHub declines to render
// are recorded empty so they are not retried every run.
func (f *FlagData) syncDiffs(d *db.DB) error {
	stale, err := d.StaleDiffs()
	if err != nil {
		return err
	}
	if len(stale) == 0 {
		cout.Verbosef("  <gray>diffs: every open PR's diff is current</>\n")
		return nil
	}
	repo, err := f.NewRepo()
	if err != nil {
		return err
	}
	cout.Printf("fetching the diffs of <yellow>%d</> open PRs (new, or updated since their last diff)...\n", len(stale))
	tooLarge := 0
	for i, n := range stale {
		p, gerr := d.GetPR(n)
		if gerr != nil {
			return gerr
		}
		diff, derr := repo.GetPullDiff(n)
		switch {
		case errors.Is(derr, gh.ErrDiffTooLarge):
			tooLarge++
		case derr != nil:
			return fmt.Errorf("fetching diff for #%d: %w", n, derr)
		}
		if len(diff) > diffMaxBytes {
			cut := diffMaxBytes
			if nl := strings.LastIndex(diff[:cut], "\n"); nl > 0 {
				cut = nl
			}
			diff = diff[:cut]
		}
		if err := d.SaveDiff(n, p.UpdatedAt, diff); err != nil {
			return err
		}
		if (i+1)%25 == 0 || i+1 == len(stale) {
			cout.Printf("  <gray>%d/%d diffs fetched</>\n", i+1, len(stale))
		}
		time.Sleep(diffThrottle)
	}
	if tooLarge > 0 {
		cout.Printf("  <gray>%d diffs too large for github to render — those PRs are matched on files and text only</>\n", tooLarge)
	}
	return nil
}

// bundles converts a page of GraphQL PR nodes into db bundles.
func bundles(nodes []gh.PRNode) []db.PRBundle {
	out := make([]db.PRBundle, 0, len(nodes))
	for i := range nodes {
		n := &nodes[i]
		if n.Number == 0 {
			continue // a search node that failed to decode (DoTolerant nulled it)
		}

		labels := make([]string, 0, len(n.Labels.Nodes))
		for _, l := range n.Labels.Nodes {
			labels = append(labels, l.Name)
		}
		paths := make([]string, 0, len(n.Files.Nodes))
		for _, fl := range n.Files.Nodes {
			paths = append(paths, fl.Path)
		}
		var lastCommit time.Time
		if len(n.Commits.Nodes) > 0 {
			lastCommit = n.Commits.Nodes[0].Commit.CommittedDate
		}

		b := db.PRBundle{PR: db.PR{
			Number: n.Number, Title: n.Title, Body: n.Body, State: n.State, IsDraft: n.IsDraft,
			Author: n.Author.Login, AuthorAssociation: n.AuthorAssociation,
			CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt, ClosedAt: n.ClosedAt, MergedAt: n.MergedAt,
			Labels: labels, Mergeable: n.Mergeable, ReviewDecision: n.ReviewDecision,
			Additions: n.Additions, Deletions: n.Deletions, ChangedFiles: n.ChangedFiles, Files: paths,
			CommentCount: n.Comments.TotalCount, ThumbsUp: n.Thumbs.TotalCount,
			LastCommitAt: lastCommit, URL: n.URL, FetchedAt: db.Now(),
		}}

		for _, c := range n.Comments.Nodes {
			b.Comments = append(b.Comments, db.Comment{
				ID: c.ID, PRNumber: n.Number, Author: c.Author.Login, AuthorAssociation: c.AuthorAssociation,
				CreatedAt: c.CreatedAt, Body: c.Body, URL: c.URL,
			})
		}
		for _, r := range n.Reviews.Nodes {
			b.Reviews = append(b.Reviews, db.Review{
				ID: r.ID, PRNumber: n.Number, Author: r.Author.Login, AuthorAssociation: r.AuthorAssociation,
				State: r.State, SubmittedAt: r.SubmittedAt, Body: r.Body, URL: r.URL,
			})
		}
		for _, l := range n.ClosingIssuesReferences.Nodes {
			ilabels := make([]string, 0, len(l.Labels.Nodes))
			for _, il := range l.Labels.Nodes {
				ilabels = append(ilabels, il.Name)
			}
			b.Closes = append(b.Closes, db.LinkedIssue{
				PRNumber: n.Number, IssueNumber: l.Number, Title: l.Title,
				State: l.State, StateReason: l.StateReason, Labels: ilabels,
			})
		}
		out = append(out, b)
	}
	return out
}

// Cache lists the local db's clearable caches, or clears one domain.
func (f *FlagData) Cache(domain string) error {
	d, err := f.OpenDB()
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()

	switch domain {
	case "":
		for _, t := range []string{"prs", "comments", "reviews", "closes", "diffs", "ai_verdicts", "actions"} {
			n, cerr := d.Count(t)
			if cerr != nil {
				return cerr
			}
			cout.Printf("  %-12s <yellow>%d</> rows\n", t, n)
		}
		cout.Printf("<gray>clear with:</> <cyan>prawn cache clear ai|prs|all</>\n")
		return nil
	case "ai":
		if _, err := d.Exec("DELETE FROM ai_verdicts"); err != nil {
			return fmt.Errorf("clearing ai_verdicts: %w", err)
		}
		cout.Printf("<green>cleared</> the AI verdict cache — the next judged run rescores\n")
		return nil
	case "prs":
		if _, err := d.Exec("DELETE FROM prs; DELETE FROM comments; DELETE FROM reviews; DELETE FROM closes; DELETE FROM diffs"); err != nil {
			return fmt.Errorf("clearing prs: %w", err)
		}
		for _, k := range []string{metaWalkCursor, metaWalkDone, metaLastSync} {
			if err := d.DeleteMeta(k); err != nil {
				return err
			}
		}
		cout.Printf("<green>cleared</> the fetched PRs — run <cyan>prawn fetch</> to rebuild (actions are never touched)\n")
		return nil
	case "all":
		if _, err := d.Exec("DELETE FROM prs; DELETE FROM comments; DELETE FROM reviews; DELETE FROM closes; DELETE FROM diffs; DELETE FROM ai_verdicts; DELETE FROM meta"); err != nil {
			return fmt.Errorf("clearing caches: %w", err)
		}
		cout.Printf("<green>cleared</> every cache — run <cyan>prawn fetch</> to rebuild (actions are never touched)\n")
		return nil
	default:
		return fmt.Errorf("unknown cache domain %q (ai, prs, or all)", domain)
	}
}

// Stats shows what the local db holds.
func (f *FlagData) Stats() error {
	d, err := f.OpenDB()
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()

	total, open, err := d.CountPRs()
	if err != nil {
		return err
	}
	cout.Printf("%s\n", f.RepoTag())
	cout.Printf("  %-16s <yellow>%d</> <gray>(%d known)</>\n", "open PRs", open, total)

	prs, err := d.OpenPRs()
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, p := range prs {
		switch {
		case p.IsDraft:
			counts["draft"]++
		case p.ReviewDecision == db.DecisionApproved:
			counts["approved"]++
		case p.ReviewDecision == db.DecisionChangesRequested:
			counts["changes-requested"]++
		default:
			counts["awaiting-review"]++
		}
		if p.Mergeable == db.MergeableConflicting {
			counts["conflicted"]++
		}
	}
	text.PrintCounts(counts)

	last, err := d.GetMeta(metaLastSync)
	if err != nil {
		return err
	}
	cout.Printf("  %-16s <gray>%s</>\n", "last sync", OrDash(last))
	return nil
}

// Reopen reopens a PR (mistake recovery) and records it on the action row.
func (f *FlagData) Reopen(number int) error {
	comment := f.Cmd.ReopenComment
	d, err := f.OpenDB()
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()

	repo, err := f.NewRepo()
	if err != nil {
		return err
	}

	if f.DryRun {
		cout.Printf("<yellow>dry-run: would reopen</> <cyan>#%d</> <darkGray>%s</>\n", number, f.PRURL(number))
		return nil
	}

	if comment != "" {
		if err := repo.CreateComment(number, comment); err != nil {
			return err
		}
	}
	if err := repo.ReopenPull(number); err != nil {
		return err
	}

	if a, err := d.LastAction(number); err != nil {
		return err
	} else if a != nil {
		if err := d.SetActionStatus(a.ID, db.StatusReopened); err != nil {
			return err
		}
	}

	cout.Printf("<fg=28>reopened</> <cyan>#%d</> <darkGray>%s</>\n", number, f.PRURL(number))
	return nil
}

// ParseNumber parses a PR number argument.
func ParseNumber(arg string) (int, error) {
	number, err := strconv.Atoi(arg)
	if err != nil {
		return 0, fmt.Errorf("PR number %q is not a number: %w", arg, err)
	}
	return number, nil
}
