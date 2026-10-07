// The fetch: every open PR — title, body, comments, reviews, changed files,
// and the issues its closing keywords reference — via the GraphQL API into
// the local database. The first run walks everything (resumable); later runs
// sync incrementally via the search API and reconcile the open set against
// the repository connection (search's index lags; the connection is ground
// truth).

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/katbyte/go-kt/clog"
	"github.com/katbyte/go-kt/cout"
	"github.com/katbyte/prawn/lib/db"
	"github.com/katbyte/prawn/lib/gh"
	"github.com/katbyte/prawn/lib/tc"
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

// syncBatch is how many changed PRs a sync fetches and saves before saying how far it has got.
const syncBatch = 5

// autoFetchTTL is how fresh the local db must be for a check to skip the
// pre-run sync.
const autoFetchTTL = time.Hour

// Fetch fills the local database from both places it reads: GitHub — a
// resumable full walk the first time (or with full), an incremental
// search-based sync after, and an open-set reconcile either way — and then,
// when it is configured, TeamCity's test builds.
func (f *FlagData) Fetch(full bool) error {
	d, err := f.OpenDB()
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return f.fetchAll(d, full)
}

// FetchGitHub is Fetch for GitHub alone.
func (f *FlagData) FetchGitHub(full bool) error {
	d, err := f.OpenDB()
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return f.fetchInto(d, full)
}

// FetchTeamCity is Fetch for TeamCity alone: every test build it keeps with
// full, those since the last sync without. Asked for by name, a failure is
// the command's failure.
func (f *FlagData) FetchTeamCity(full bool) error {
	if !f.TC.Configured() {
		return errors.New("teamcity is not configured: set TC_SERVER, TC_TOKEN and TC_PROJECT (or --tc-server, --tc-token, --tc-project)")
	}
	d, err := f.OpenDB()
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return f.syncTests(d, full)
}

// fetchAll is GitHub, then TeamCity when it is configured. The tests are
// worth having and not worth failing a fetch over: prawn works without them.
func (f *FlagData) fetchAll(d *db.DB, full bool) error {
	if err := f.fetchInto(d, full); err != nil {
		return err
	}
	if f.TC.Configured() {
		if err := f.syncTests(d, false); err != nil {
			clog.Log.Warnf("teamcity test results not refreshed: %v", err)
		}
	}
	if f.Cmd.SrcDir != "" {
		if err := f.SyncSrcDir(); err != nil {
			clog.Log.Warnf("provider checkout not refreshed: %v", err)
		}
	}
	return nil
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
	return f.fetchAll(d, false)
}

// fetchInto is the GitHub half of a fetch.
func (f *FlagData) fetchInto(d *db.DB, full bool) error {
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
		cout.Printf("  <yellow>%d</><gray>/</><yellow>%d</><gray> fetched · rate limit: </><yellow>%d</><gray> remaining</>\n", fetched, page.TotalCount, page.RateLimit.Remaining)
		page.RateLimit.WaitIfLow()
		if !page.PageInfo.HasNextPage {
			break
		}
		cursor = page.PageInfo.EndCursor
	}

	return d.SetMeta(metaWalkDone, "1")
}

// syncPRs pulls every PR (any state) updated since the last sync: it counts them first, then fetches them.
func (f *FlagData) syncPRs(d *db.DB, client *gh.Client, owner, name string) error {
	last, err := d.GetMeta(metaLastSync)
	if err != nil {
		return err
	}
	t, terr := time.Parse(time.RFC3339, last)
	if terr != nil {
		// no sync point to stop at: the open walk is the way to start over
		if err := d.DeleteMeta(metaWalkCursor); err != nil {
			return err
		}
		return f.walkPRs(d, client, owner, name)
	}
	since := t.Add(-syncOverlap)
	cout.Printf("asking github which PRs of %s have changed since <yellow>%s</>...\n", f.RepoTag(), since.Format(time.RFC3339))

	numbers, err := client.UpdatedPRNumbers(owner, name, since)
	if err != nil {
		return err
	}
	if len(numbers) == 0 {
		cout.Printf("  <gray>nothing has changed</>\n")
		return nil
	}
	cout.Printf("  <yellow>%d</><gray> PRs have changed — fetching them, oldest change first</>\n", len(numbers))

	// oldest change first and saved as it goes, so a sync that is cut short has lost nothing it will not see again
	fetched := 0
	for batch := range slices.Chunk(numbers, syncBatch) {
		nodes, failed := client.PRsByNumber(owner, name, batch)
		if err := d.SavePRs(bundles(nodes), "", ""); err != nil {
			return err
		}
		for _, n := range slices.Sorted(maps.Keys(failed)) {
			clog.Log.Warnf("PR #%d changed on github and could not be fetched: %s", n, failed[n])
		}
		fetched += len(batch)
		cout.Printf("  <yellow>%d</><gray>/</><yellow>%d</><gray> fetched</>\n", fetched, len(numbers))
	}
	return nil
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
			cout.Printf("  <gray>closed PRs since %s are already here</>\n", covered)
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
	cout.Printf("  <gray>backfill complete: </><yellow>%d</><gray> closed PRs fetched</>\n", fetched)
	if fetched == 0 {
		// the backfill is the one thing left that searches, and search answers a fine-grained token nothing
		clog.Log.Warnf("github's search found no closed PRs at all: a fine-grained token cannot search a repository its owner does not own, so the history before today is missing — run the backfill once with a classic token")
	}
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
				cout.Printf("  <fg=208>page failed (%v) — retrying this window in pages of %d</>\n", err, gh.ClosedPRsSmallPage)
				pageSize = gh.ClosedPRsSmallPage
				continue
			}
			return fetched, err
		}
		if err := d.SavePRs(bundles(page.PRs), "", ""); err != nil {
			return fetched, err
		}
		fetched += len(page.PRs)
		cout.Printf("  <gray>%s..%s: </><yellow>%d</><gray>/</><yellow>%d</><gray> fetched · rate limit: </><yellow>%d</><gray> remaining</>\n",
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
func (*FlagData) syncTimelines(d *db.DB, client *gh.Client, owner, name string) error {
	pending, err := d.IncompleteTimelines()
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		cout.Printf("  <gray>every PR's timeline is complete</>\n")
		return nil
	}
	cout.Printf("fetching the remaining timeline pages of <yellow>%d</> PRs with long histories...\n", len(pending))
	numbers := make([]int, 0, len(pending))
	for n := range pending {
		numbers = append(numbers, n)
	}
	slices.Sort(numbers)
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
			cout.Printf("  <yellow>%d</><gray>/</><yellow>%d</><gray> timelines completed (</><yellow>%d</><gray> pages)</>\n", i+1, len(numbers), pages)
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
	cout.Printf("checking every open PR against github's list: is it here, is it current, can it merge, is its CI passing...\n")
	open, err := client.OpenPRNumbers(owner, name, func(fetched, total int) {
		cout.Printf("  <yellow>%d</><gray>/</><yellow>%d</><gray> open PRs listed</>\n", fetched, total)
	})
	if err != nil {
		return err
	}
	settleMergeable(client, owner, name, open)

	states, err := d.PRStates()
	if err != nil {
		return err
	}
	// what the sync's search did not bring: github's own list of open PRs is the truth, and its search is not —
	// the index lags (a PR opened moments before a sync can be missed for good), and a token an organisation has
	// not authorised (single sign-on, or a fine-grained token) is given few results or none. So every open PR is
	// checked against the list: missing or closed here, or changed on github since it was stored, it is fetched
	// whole, and gets its timeline and diff below
	updated, err := d.OpenUpdated()
	if err != nil {
		return err
	}
	var missing, stale []int
	for number, st := range open {
		switch at, known := updated[number]; {
		case states[number] != db.PROpen:
			missing = append(missing, number)
		case known && st.UpdatedAt.After(at.Add(time.Second)):
			stale = append(stale, number)
		}
	}
	if want := slices.Sorted(slices.Values(slices.Concat(missing, stale))); len(want) > 0 {
		cout.Printf("  <yellow>%d</><gray> open PRs are missing here and </><yellow>%d</><gray> changed on github since they were stored — fetching them</>\n", len(missing), len(stale))
		nodes, failed := client.PRsByNumber(owner, name, want)
		if err := d.SavePRs(bundles(nodes), "", ""); err != nil {
			return err
		}
		for _, n := range slices.Sorted(maps.Keys(failed)) {
			clog.Log.Warnf("PR #%d is open on github and could not be fetched: %s", n, failed[n])
		}
		if states, err = d.PRStates(); err != nil {
			return err
		}
	}
	if len(missing)+len(stale) == 0 {
		cout.Printf("  <gray>every one of the </><yellow>%d</><gray> open PRs is here and current</>\n", len(open))
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
		cout.Printf("  <yellow>%d</><gray> locally-open PRs are no longer open on github — marked closed</>\n", len(gone))
		if err := d.MarkPRsClosed(gone); err != nil {
			return err
		}
	}
	changed, err := d.UpdateOpenStatuses(statuses)
	if err != nil {
		return err
	}
	cout.Printf("  <gray>mergeability and CI refreshed on </><yellow>%d</><gray> open PRs, </><yellow>%d</><gray> changed</>\n", len(statuses), changed)

	// the detail behind the CI state is worth having and not worth failing a fetch over
	current := make(map[int]gh.OpenPRStatus, len(statuses))
	for number := range statuses {
		current[number] = open[number]
	}
	repo, rerr := f.NewRepo()
	if rerr != nil {
		return rerr
	}
	if err := syncCI(d, client, repo, owner, name, current); err != nil {
		clog.Log.Warnf("ci detail not refreshed: %v", err)
	}
	return nil
}

// github works a PR's mergeability out when it is asked and answers UNKNOWN
// until it has: after the base branch moves, the walk's first ask leaves most
// of the open set unknown. Asking again a little later gets the real answers.
const (
	mergeablePasses = 4
	mergeableWait   = 6 * time.Second
)

// settleMergeable re-asks for the PRs the walk left UNKNOWN, a few times with
// a pause between, and writes what comes back into open. What is still
// unknown after that stays so: a nicety, never a failed fetch.
func settleMergeable(client *gh.Client, owner, name string, open map[int]gh.OpenPRStatus) {
	const unknown = "UNKNOWN"
	for pass := 1; pass <= mergeablePasses; pass++ {
		var ask []int
		for number, st := range open {
			if st.Mergeable == unknown {
				ask = append(ask, number)
			}
		}
		if len(ask) == 0 {
			return
		}
		slices.Sort(ask)
		cout.Printf("  <gray>mergeability of </><yellow>%d</><gray> PRs not worked out yet — asking again (</><yellow>%d</><gray>/</><yellow>%d</><gray>)</>\n", len(ask), pass, mergeablePasses)
		time.Sleep(mergeableWait)
		got, err := client.Mergeable(owner, name, ask)
		if err != nil {
			clog.Log.Warnf("mergeability not settled: %v", err)
			return
		}
		for number, m := range got {
			st := open[number]
			st.Mergeable = m
			open[number] = st
		}
	}
}

// syncCI measures what stands behind each open PR's CI state: which checks
// fail (asked only of the red ones), how far behind its base the branch is,
// and how many commits the base has had since the checks last ran. A few
// dozen requests a fetch; the walk that produced open already carries when
// the checks ran.
func syncCI(d *db.DB, client *gh.Client, repo gh.Repo, owner, name string, open map[int]gh.OpenPRStatus) error {
	failing := map[int]bool{}
	var red []int
	byBase := map[string]map[int]time.Time{}
	for number, st := range open {
		if (st.CheckState == "FAILURE" || st.CheckState == "ERROR") && st.CIAwaiting == 0 {
			failing[number] = true
			red = append(red, number)
		}
		if st.BaseRef != "" {
			if byBase[st.BaseRef] == nil {
				byBase[st.BaseRef] = map[int]time.Time{}
			}
			byBase[st.BaseRef][number] = st.CIRanAt
		}
	}
	slices.Sort(red)

	checks, err := client.FailingChecks(owner, name, red)
	if err != nil {
		return err
	}
	distances := map[int]gh.BaseDistance{}
	for base, prs := range byBase {
		ds, derr := client.BaseDistances(owner, name, base, prs)
		if derr != nil {
			return derr
		}
		maps.Copy(distances, ds)
	}

	known, err := d.AllCI()
	if err != nil {
		return err
	}
	cis := make([]db.CI, 0, len(open))
	var ask []int // behind, and the head moved (or never asked): the merge base is asked again
	for number, st := range open {
		ci := db.CI{PRNumber: number, RanAt: st.CIRanAt, Awaiting: st.CIAwaiting, Failing: checks[number], Behind: -1, Ahead: -1, Drift: -1, HeadOid: st.HeadOid}
		if dist, ok := distances[number]; ok {
			ci.Behind, ci.Ahead, ci.Drift = dist.Behind, dist.Ahead, dist.Drift
		}
		if k, ok := known[number]; ok && ci.Behind > 0 && st.HeadOid != "" && k.HeadOid == st.HeadOid && !k.BehindSince.IsZero() {
			ci.BehindSince = k.BehindSince
		} else if ci.Behind > 0 && st.BaseRef != "" {
			ask = append(ask, number)
		}
		cis = append(cis, ci)
	}
	if len(ask) > 0 {
		cout.Printf("  <gray>how long </><yellow>%d</><gray> PRs have been behind their base (one request each, kept until they change)...</>\n", len(ask))
		at := map[int]time.Time{}
		failed := 0
		for i, number := range ask {
			if t, merr := repo.MergeBase(open[number].BaseRef, number); merr == nil {
				at[number] = t
			} else {
				failed++
				clog.Log.Debugf("merge base of #%d: %v", number, merr)
			}
			if (i+1)%10 == 0 {
				cout.Printf("  <yellow>%d</><gray>/</><yellow>%d</><gray> measured</>\n", i+1, len(ask))
			}
		}
		for i := range cis {
			if t, ok := at[cis[i].PRNumber]; ok {
				cis[i].BehindSince = t
			}
		}
		if failed > 0 {
			clog.Log.Warnf("how long %d PRs have been behind their base is not known: github would not compare them", failed)
		}
	}
	if err := d.SaveCI(cis, failing); err != nil {
		return err
	}
	cout.Printf("  <gray>ci detail on </><yellow>%d</><gray> open PRs — </><yellow>%d</><gray> failing, </><yellow>%d</><gray> measured against their base</>\n", len(cis), len(red), len(distances))
	return nil
}

// the teamcity sync: when it last ran, how far behind that it looks again (a
// build queued or running when last seen has since finished), and how much
// it asks about failures — the names of up to tcFailedTestsMax tests for each
// of up to tcFailedBuildsMax builds a run.
const (
	metaTCLastSync    = "tc_last_sync"
	tcOverlap         = 3 * 24 * time.Hour
	tcFailedTestsMax  = 40
	tcFailedBuildsMax = 200
)

// syncTests collects the acceptance test builds TeamCity ran on pull request
// branches — every one it still keeps the first time (or with full), those
// since the last sync after — and then names the failed tests of each open
// PR's latest failing builds.
func (f *FlagData) syncTests(d *db.DB, full bool) error {
	last, err := d.GetMeta(metaTCLastSync)
	if err != nil {
		return err
	}
	since := time.Time{}
	if t, terr := time.Parse(time.RFC3339, last); terr == nil && !full {
		since = t.Add(-tcOverlap)
	}
	if since.IsZero() {
		cout.Printf("fetching every test build teamcity keeps for %s...\n", f.TC.Project)
	} else {
		cout.Printf("fetching %s's test builds since <yellow>%s</>...\n", f.TC.Project, since.Format("2006-01-02"))
	}

	start := db.Now()
	ctx := context.Background()
	client := tc.New(f.TC.Server, f.TC.Token)
	builds, err := client.PRBuilds(ctx, f.TC.Project, since, func(fetched int) {
		cout.Printf("  <yellow>%d</><gray> builds</>\n", fetched)
	})
	if err != nil {
		return err
	}
	rows := make([]db.TCBuild, 0, len(builds))
	for i := range builds {
		b := &builds[i]
		rows = append(rows, db.TCBuild{
			ID: b.ID, PRNumber: b.PR, BuildType: b.BuildType, Branch: b.Branch, State: b.State, Status: b.Status, StatusText: b.StatusText,
			Passed: b.Passed, Failed: b.Failed, Ignored: b.Ignored, StartedAt: b.Started, FinishedAt: b.Finished, Revision: b.Revision, URL: b.URL,
		})
	}
	if err := d.SaveTCBuilds(rows); err != nil {
		return err
	}

	wanting, err := d.TCBuildsWantingTests()
	if err != nil {
		return err
	}
	if len(wanting) > tcFailedBuildsMax {
		wanting = wanting[:tcFailedBuildsMax] // newest first: the rest are named on later runs
	}
	for _, id := range wanting {
		names, err := client.FailedTests(ctx, id, tcFailedTestsMax)
		if err != nil {
			return err
		}
		if err := d.SetTCFailedTests(id, names); err != nil {
			return err
		}
	}
	cout.Printf("  <yellow>%d</><gray> test builds, the failed tests of </><yellow>%d</><gray> named</>\n", len(rows), len(wanting))
	return d.SetMeta(metaTCLastSync, start.Format(time.RFC3339))
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
		cout.Printf("  <gray>every open PR's diff is current</>\n")
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
			cout.Printf("  <yellow>%d</><gray>/</><yellow>%d</><gray> diffs fetched</>\n", i+1, len(stale))
		}
		time.Sleep(diffThrottle)
	}
	if tooLarge > 0 {
		cout.Printf("  <yellow>%d</><gray> diffs too large for github to render — those PRs are matched on files and text only</>\n", tooLarge)
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
	cout.Printf("  %-16s <yellow>%d</> <gray>(</><yellow>%d</><gray> known)</>\n", "open PRs", open, total)

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
		cout.Printf("<fg=208>dry-run: would reopen</> <cyan>#%d</> <darkGray>%s</>\n", number, f.PRURL(number))
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

// LastSync is when the database last synced with github, zero when it never has.
func LastSync(d *db.DB) (time.Time, error) {
	v, err := d.GetMeta(metaLastSync)
	if err != nil || v == "" {
		return time.Time{}, err
	}
	t, _ := time.Parse(time.RFC3339, v)
	return t, nil
}
