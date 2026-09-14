// The fetch: every open PR — title, body, comments, reviews, changed files,
// and the issues its closing keywords reference — via the GraphQL API into
// the local database. The first run walks everything (resumable); later runs
// sync incrementally via the search API and reconcile the open set against
// the repository connection (search's index lags; the connection is ground
// truth).

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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

	// the closed-PR backfill: the date the completed backfill reaches back
	// to, and — while one runs — the window boundary it has reached and the
	// date it is filling up to
	metaBackfillSince = "pr_backfill_since"
	metaBackfillUntil = "pr_backfill_until"
	metaBackfillEnd   = "pr_backfill_end"
)

// backfillWindowCap is the most PRs one search window may hold: the search
// API stops at 1000 results, so a window over the cap is split until under.
const backfillWindowCap = 900

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
	since, err := f.SinceTime()
	if err != nil {
		return err
	}
	if !since.IsZero() {
		if err := f.backfill(d, client, owner, name, since); err != nil {
			return err
		}
	}
	if err := f.syncTimelines(d, client, owner, name); err != nil {
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

// backfill walks every PR closed or merged since the date — the rest of the
// population that was open at some point in the period — in month windows
// (split when a window nears the search cap), recording progress per window
// so an interrupted run resumes at the last window that completed. A later
// run with an earlier --since fills only the gap.
func (f *FlagData) backfill(d *db.DB, client *gh.Client, owner, name string, since time.Time) error {
	covered, err := d.GetMeta(metaBackfillSince)
	if err != nil {
		return err
	}
	// windows are [from, to) on whole days, so the default end is tomorrow:
	// PRs closed earlier today are in the last window, not in a gap between
	// this backfill and the next sync (whose point is this run's start)
	now := db.Now()
	end := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	if t, terr := time.Parse("2006-01-02", covered); terr == nil {
		if !since.Before(t) {
			cout.Verbosef("  <gray>backfill: closed PRs since %s already fetched</>\n", covered)
			return nil
		}
		end = t // an earlier since: fill only [since, covered)
	}
	// an interrupted run left its target end and progress behind
	if v, verr := d.GetMeta(metaBackfillEnd); verr == nil && v != "" {
		if t, terr := time.Parse("2006-01-02", v); terr == nil {
			end = t
		}
	}
	start := since
	if v, verr := d.GetMeta(metaBackfillUntil); verr == nil && v != "" {
		if t, terr := time.Parse("2006-01-02", v); terr == nil && t.After(start) {
			start = t
			cout.Printf("resuming the closed-PR backfill of %s from <yellow>%s</>...\n", f.RepoTag(), v)
		}
	}
	if err := d.SetMeta(metaBackfillEnd, end.Format("2006-01-02")); err != nil {
		return err
	}
	if start.Equal(since) {
		cout.Printf("backfilling every PR of %s closed or merged since <yellow>%s</> (timelines included)...\n",
			f.RepoTag(), since.Format("2006-01-02"))
	}

	fetched := 0
	for ws := start; ws.Before(end); {
		we := time.Date(ws.Year(), ws.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		if we.After(end) {
			we = end
		}
		n, err := f.backfillWindow(d, client, owner, name, ws, we)
		if err != nil {
			return err
		}
		fetched += n
		if err := d.SetMeta(metaBackfillUntil, we.Format("2006-01-02")); err != nil {
			return err
		}
		ws = we
	}

	if err := d.SetMeta(metaBackfillSince, since.Format("2006-01-02")); err != nil {
		return err
	}
	for _, k := range []string{metaBackfillUntil, metaBackfillEnd} {
		if err := d.DeleteMeta(k); err != nil {
			return err
		}
	}
	cout.Printf("  <gray>backfill complete: %d closed PRs fetched</>\n", fetched)
	return nil
}

// backfillWindow fetches the PRs closed in [from, to), splitting the window
// in half while it holds more than the search cap.
func (f *FlagData) backfillWindow(d *db.DB, client *gh.Client, owner, name string, from, to time.Time) (int, error) {
	// the search's closed:A..B is inclusive of B's whole day, so end a day early
	last := to.AddDate(0, 0, -1)
	if last.Before(from) {
		last = from
	}
	count, err := client.ClosedPRCount(owner, name, from, last)
	if err != nil {
		return 0, err
	}
	if count > backfillWindowCap && to.Sub(from) > 24*time.Hour {
		mid := from.Add(to.Sub(from) / 2).Truncate(24 * time.Hour)
		a, err := f.backfillWindow(d, client, owner, name, from, mid)
		if err != nil {
			return a, err
		}
		b, err := f.backfillWindow(d, client, owner, name, mid, to)
		return a + b, err
	}
	if count == 0 {
		return 0, nil
	}

	cursor, fetched, pageSize := "", 0, gh.ClosedPRsPageSize
	for {
		page, err := client.ClosedPRs(owner, name, from, last, cursor, pageSize)
		if err != nil {
			// the client already retried with growing waits; a page that still
			// fails is too heavy for the gateway — take the rest of the window
			// in small bites before giving up
			if pageSize > gh.ClosedPRsSmallPage {
				cout.Printf("  <yellow>page failed (%v) — retrying this window in pages of %d</>\n", err, gh.ClosedPRsSmallPage)
				pageSize = gh.ClosedPRsSmallPage
				continue
			}
			return fetched, err
		}
		if err := d.SavePRs(bundles(page.PRs), "", ""); err != nil {
			return fetched, err
		}
		fetched += len(page.PRs)
		cout.Printf("  <gray>%s..%s: %d/%d fetched · rate limit: %d remaining</>\n",
			from.Format("2006-01-02"), last.Format("2006-01-02"), fetched, page.PRCount, page.RateLimit.Remaining)
		page.RateLimit.WaitIfLow()
		if !page.PageInfo.HasNextPage {
			return fetched, nil
		}
		cursor = page.PageInfo.EndCursor
	}
}

// syncTimelines fetches the remaining timeline pages of every PR whose first
// page (fetched with the PR) had more — long threads, mostly — one request
// per page, saved as it goes.
func (f *FlagData) syncTimelines(d *db.DB, client *gh.Client, owner, name string) error {
	pending, err := d.IncompleteTimelines()
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		cout.Verbosef("  <gray>timelines: every PR's timeline is complete</>\n")
		return nil
	}
	cout.Printf("fetching the remaining timeline pages of <yellow>%d</> PRs with long histories...\n", len(pending))
	numbers := make([]int, 0, len(pending))
	for n := range pending {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	pages := 0
	for i, n := range numbers {
		cursor := pending[n]
		for cursor != "" {
			page, rl, err := client.PRTimeline(owner, name, n, cursor)
			if err != nil {
				return err
			}
			events, commits := timelineRows(n, page.Nodes)
			next := ""
			if page.PageInfo.HasNextPage {
				next = page.PageInfo.EndCursor
			}
			if err := d.SaveEvents(n, events, commits, next); err != nil {
				return err
			}
			pages++
			rl.WaitIfLow()
			cursor = next
		}
		if (i+1)%25 == 0 || i+1 == len(numbers) {
			cout.Printf("  <gray>%d/%d timelines completed (%d pages)</>\n", i+1, len(numbers), pages)
		}
	}
	return nil
}

// timelineRows converts raw timeline nodes into event rows and, for the
// commit items among them, commit rows.
func timelineRows(number int, nodes []json.RawMessage) ([]db.Event, []db.Commit) {
	events := make([]db.Event, 0, len(nodes))
	var commits []db.Commit
	for _, raw := range nodes {
		n, err := gh.ParseTimelineNode(raw)
		if err != nil || n.ID == "" {
			continue // a node the token can't read, nulled by DoTolerant
		}
		e := db.Event{
			ID: n.ID, PRNumber: number, Type: n.Typename, Actor: n.Who(), CreatedAt: n.When(),
			Label: n.Label.Name, Milestone: n.MilestoneTitle, Subject: n.Subject(), State: n.State, Raw: string(raw),
		}
		events = append(events, e)
		if n.Typename == db.EventCommit && n.Commit.Oid != "" {
			commits = append(commits, db.Commit{
				Oid: n.Commit.Oid, PRNumber: number, Author: n.Commit.Author.User.Login, AuthorName: n.Commit.Author.Name,
				AuthoredAt: n.Commit.AuthoredDate, CommittedAt: n.Commit.CommittedDate,
				Additions: n.Commit.Additions, Deletions: n.Commit.Deletions, Headline: n.Commit.MessageHeadline,
			})
		}
	}
	return events, commits
}

// reconcile pages github's real open set and closes local rows that fell out
// of it — catches closes the search-index lag hides — and refreshes every
// open PR's mergeability and CI state, which change without the PR's
// updatedAt moving and so are invisible to the incremental sync.
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
	statuses := make(map[int]db.PRStatus, len(open))
	for number, state := range states {
		if state != db.PROpen {
			continue
		}
		st, stillOpen := open[number]
		if !stillOpen {
			gone = append(gone, number)
			continue
		}
		statuses[number] = db.PRStatus{Mergeable: st.Mergeable, CheckState: st.CheckState}
	}
	if len(gone) > 0 {
		cout.Printf("  <gray>reconcile: %d locally-open PRs are no longer open on github — marked closed</>\n", len(gone))
		if err := d.MarkPRsClosed(gone); err != nil {
			return err
		}
	}
	changed, err := d.UpdateOpenStatuses(statuses)
	if err != nil {
		return err
	}
	cout.Verbosef("  <gray>reconcile: mergeability and CI refreshed on %d open PRs, %d changed</>\n", len(statuses), changed)
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
		var checkState string
		if len(n.Commits.Nodes) > 0 {
			lastCommit = n.Commits.Nodes[0].Commit.CommittedDate
			checkState = n.Commits.Nodes[0].Commit.StatusCheckRollup.State
		}

		b := db.PRBundle{PR: db.PR{
			Number: n.Number, Title: n.Title, Body: n.Body, State: n.State, IsDraft: n.IsDraft,
			Author: n.Author.Login, AuthorAssociation: n.AuthorAssociation,
			CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt, ClosedAt: n.ClosedAt, MergedAt: n.MergedAt,
			Labels: labels, Mergeable: n.Mergeable, ReviewDecision: n.ReviewDecision,
			Additions: n.Additions, Deletions: n.Deletions, ChangedFiles: n.ChangedFiles, Files: paths,
			CommentCount: n.Comments.TotalCount, ThumbsUp: n.Thumbs.TotalCount,
			LastCommitAt: lastCommit, URL: n.URL, FetchedAt: db.Now(),
			MergedBy: n.MergedBy.Login, Milestone: n.Milestone.Title, BaseRef: n.BaseRefName, HeadRef: n.HeadRefName,
			CheckState: checkState,
		}}
		b.Events, b.Commits = timelineRows(n.Number, n.TimelineItems.Nodes)
		if n.TimelineItems.PageInfo.HasNextPage {
			b.PR.EventsCursor = n.TimelineItems.PageInfo.EndCursor
		}

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
		for _, t := range []string{"prs", "comments", "reviews", "closes", "events", "commits", "diffs", "ai_verdicts", "actions"} {
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
		if _, err := d.Exec("DELETE FROM prs; DELETE FROM comments; DELETE FROM reviews; DELETE FROM closes; DELETE FROM events; DELETE FROM commits; DELETE FROM diffs"); err != nil {
			return fmt.Errorf("clearing prs: %w", err)
		}
		for _, k := range []string{metaWalkCursor, metaWalkDone, metaLastSync, metaBackfillSince, metaBackfillUntil, metaBackfillEnd} {
			if err := d.DeleteMeta(k); err != nil {
				return err
			}
		}
		cout.Printf("<green>cleared</> the fetched PRs — run <cyan>prawn fetch</> to rebuild (actions are never touched)\n")
		return nil
	case "all":
		if _, err := d.Exec("DELETE FROM prs; DELETE FROM comments; DELETE FROM reviews; DELETE FROM closes; DELETE FROM events; DELETE FROM commits; DELETE FROM diffs; DELETE FROM ai_verdicts; DELETE FROM meta"); err != nil {
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
