// Package explore derives the review-flow stats the explore page plots from
// the fetched PRs and their timelines: which court a PR sits in and since
// when, how long the first maintainer response took, the waiting cycles, the
// daily state a PR was in over its life, the areas of the provider it
// touches, and a rough review-effort estimate. Everything is recomputed from
// the events on every run — nothing derived is stored.
package explore

import (
	"cmp"
	"encoding/json"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/katbyte/prawn/lib/db"
)

// Daily states a PR moves through, as the trends tab stacks them. The order
// is the stacking order.
const (
	StateDraft    = 0 // draft: nobody is expected to review yet
	StateAwaiting = 1 // the maintainers' court: needs a first look, or a re-look after the author pushed
	StateWaiting  = 2 // the author's court: waiting-response label, or changes requested and nothing pushed since
	StateApproved = 3 // approved and not yet merged
	StateBlocked  = 4 // the Blocked milestone
)

// StateNames is the label for each state code, by index.
var StateNames = []string{"draft", "awaiting", "waiting", "approved", "blocked"}

// Courts.
const (
	CourtAuthor     = "author"
	CourtMaintainer = "maintainer"
)

// PR states as the page names them, and review verdicts.
const (
	stateOpen      = "open"
	stateMerged    = "merged"
	stateClosed    = "closed"
	reviewApproved = "approved"
	reviewChanges  = "changes"
)

// Interval is one run of days a PR spent in a state; End is 0 while the run
// is still going (an open PR's current state).
type Interval struct {
	Start int64 `json:"s"`
	End   int64 `json:"e"`
	State int   `json:"st"`
}

// Review verdicts a side (the members, the partners) has given a PR: the
// last one that side gave.
const (
	ReviewNone      = 0 // never reviewed or commented on
	ReviewCommented = 1 // commented, or reviewed without a verdict
	ReviewChanges   = 2 // changes requested, the last verdict
	ReviewApproved  = 3 // approved, the last verdict
)

// ReviewNames is the label for each verdict, by index.
var ReviewNames = []string{"never reviewed", "commented only", "changes requested", "approved"}

// A status interval's State packs both sides' verdicts: members*4 + partners.
func statusCode(members, partners int) int { return members*4 + partners }

// Span is one run of days a PR wore a label or a milestone; End is 0 while
// it still does. Name is the label, or "ms:<title>" for a milestone.
type Span struct {
	Name  string `json:"n"`
	Start int64  `json:"s"`
	End   int64  `json:"e"`
}

// ReviewerCount is one reviewer who requested changes: how many times, and
// how many inline review comments those requests carried — ghp-sync's
// "Changes Requested By" (`login(×2 ✎10)`).
type ReviewerCount struct {
	Login    string `json:"l"`
	Requests int    `json:"n"`
	Comments int    `json:"c"`
}

// Event is the compact timeline row the page embeds: [when, kind, who, what,
// and for a review its inline comment count].
type Event struct {
	At   int64  `json:"t"`
	Kind string `json:"k"`
	Who  string `json:"w"`
	What string `json:"x,omitempty"`
	N    int    `json:"n,omitempty"`
}

// PR is one row of the explore data set, json-tagged short since thousands
// are embedded in the page.
type PR struct {
	Number    int      `json:"n"`
	Title     string   `json:"t"`
	Author    string   `json:"a"`
	Group     string   `json:"g"` // the configured group the author is in, or "community"
	Assoc     string   `json:"as"`
	State     string   `json:"s"` // open | merged | closed
	Draft     bool     `json:"d"`
	Created   int64    `json:"c"`
	Updated   int64    `json:"u"`
	Closed    int64    `json:"x,omitempty"`
	Merged    int64    `json:"m,omitempty"`
	MergedBy  string   `json:"mb,omitempty"`
	Milestone string   `json:"ms,omitempty"`
	Labels    []string `json:"l"`
	Services  []string `json:"sv"`
	Kinds     []string `json:"k"` // docs, vendor, tests, ci, schema, changelog, other
	Files     int      `json:"f"`
	Adds      int      `json:"ad"`
	Dels      int      `json:"de"`
	Comments  int      `json:"cm"`
	Reviews   int      `json:"rv"`
	Rounds    int      `json:"rr"` // changes-requested reviews by maintainers
	Approvals int      `json:"ap"`
	Reviewers []string `json:"rw"` // maintainers who reviewed or commented, most active first

	// the review roll, everyone but the author and the bots, as ghp-sync
	// stamps it on a project: who reviewed at all (first review first), who
	// approved (first approval first), who requested changes and how hard
	ReviewedBy     []string        `json:"rb"`
	ApprovedBy     []string        `json:"ab"`
	ChangesBy      []ReviewerCount `json:"cb"`
	ReviewComments int             `json:"rc"`           // inline comments across those reviews
	CI             string          `json:"ci,omitempty"` // the head commit's checks: passing | failing | running, open PRs only

	FirstResponseDays float64 `json:"fr"` // days to the first maintainer response, -1 when none yet
	FirstResponder    string  `json:"fw,omitempty"`
	ResolveDays       float64 `json:"td"` // days open until merged or closed, -1 while open

	Court         string             `json:"ct"` // author | maintainer ("" once closed)
	CourtSince    int64              `json:"cs,omitempty"`
	LastActivity  int64              `json:"la"`
	LastMaint     int64              `json:"lm,omitempty"` // last maintainer activity
	LastAuthor    int64              `json:"lu,omitempty"` // last author activity
	WaitingDays   float64            `json:"wd"`           // total days under waiting-response
	WaitingCycles int                `json:"wc"`
	Effort        int                `json:"ef"` // 1 (trivial) .. 5 (a day's review)
	Decision      string             `json:"rd,omitempty"`
	Mergeable     string             `json:"mg,omitempty"`
	Thumbs        int                `json:"th"`
	LinkedIssues  []int              `json:"li,omitempty"`
	Intervals     []Interval         `json:"iv"`
	Statuses      []Interval         `json:"rs"` // the review status over time, State a Review* code
	Spans         []Span             `json:"ls"` // the labels and milestones over time
	Events        []Event            `json:"ev"`
	AI            map[string]float64 `json:"ai,omitempty"` // check pass → confidence
	URL           string             `json:"url"`
}

// Release is one provider release tag, for the trends x-axis markers.
type Release struct {
	Tag  string `json:"tag"`
	Date int64  `json:"date"`
}

// CheckItem is one close-candidate on the checks tab.
type CheckItem struct {
	Check    string   `json:"check"`
	Number   int      `json:"n"`
	Evidence []string `json:"ev"`
	Score    string   `json:"score,omitempty"`
	Reason   string   `json:"reason,omitempty"`
}

// Data is everything the page embeds.
type Data struct {
	Repo            string              `json:"repo"`
	GeneratedAt     string              `json:"generated"`
	Version         string              `json:"version"` // the prawn that wrote the page
	Since           string              `json:"since"`
	ViewFrom        string              `json:"viewFrom"` // the period the page opens on, yyyy-mm-dd
	Now             int64               `json:"now"`
	Groups          map[string][]string `json:"groups"`
	MaintainerGroup string              `json:"maintainerGroup"`
	Maintainers     []string            `json:"maintainers"`
	PartnerGroup    string              `json:"partnerGroup"`
	Partners        []string            `json:"partners"`
	Services        []string            `json:"services"`
	Labels          []string            `json:"labels"`
	PRs             []PR                `json:"prs"`
	Releases        []Release           `json:"releases"`
	Checks          []CheckItem         `json:"checks"`
	ChecksNote      string              `json:"checksNote"`
}

// Config is what Build needs beyond the db rows.
type Config struct {
	Groups          map[string][]string
	MaintainerGroup string
	Maintainers     []string
	PartnerGroup    string
	Partners        []string // the second review side; nil when no partners group is set
	Now             time.Time
}

// Input is the fetched material Build derives from.
type Input struct {
	PRs      []*db.PR
	Events   map[int][]db.Event
	Commits  map[int][]db.Commit
	Verdicts map[int]map[string]db.Verdict
	Closes   map[int][]db.LinkedIssue
}

// labels and milestones the state machine reads
const (
	labelWaiting     = "waiting-response"
	milestoneBlocked = "blocked"
)

// isBot tells GitHub apps and the repo's bots from people.
func isBot(login string) bool {
	l := strings.ToLower(login)
	return l == "" || strings.HasSuffix(l, "[bot]") || l == "hashibot" || l == "github-actions" || l == "dependabot" || strings.HasSuffix(l, "-bot")
}

// Build derives the data set.
func Build(in Input, cfg Config) *Data {
	now := cfg.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	// who is a maintainer: the configured group, or (the caller's fallback)
	// whoever the fetched reviews and comments mark MEMBER / OWNER / COLLABORATOR
	maint := map[string]bool{}
	for _, l := range cfg.Maintainers {
		if !isBot(l) {
			maint[strings.ToLower(l)] = true
		}
	}
	partners := map[string]bool{}
	for _, l := range cfg.Partners {
		if !isBot(l) {
			partners[strings.ToLower(l)] = true
		}
	}
	groupOf := map[string]string{}
	for _, name := range sortedKeys(cfg.Groups) {
		for _, l := range cfg.Groups[name] {
			if _, taken := groupOf[strings.ToLower(l)]; !taken {
				groupOf[strings.ToLower(l)] = name
			}
		}
	}

	d := &Data{
		Repo: "", GeneratedAt: now.Local().Format("2006-01-02 15:04 MST"), Now: now.Unix(),
		Groups: cfg.Groups, MaintainerGroup: cfg.MaintainerGroup, PartnerGroup: cfg.PartnerGroup,
	}
	for l := range maint {
		d.Maintainers = append(d.Maintainers, l)
	}
	slices.Sort(d.Maintainers)
	d.Partners = make([]string, 0, len(partners))
	for l := range partners {
		d.Partners = append(d.Partners, l)
	}
	slices.Sort(d.Partners)

	services := map[string]bool{}
	labels := map[string]bool{}
	d.PRs = make([]PR, 0, len(in.PRs))
	for _, p := range in.PRs {
		row := derive(p, in.Events[p.Number], in.Commits[p.Number], in.Verdicts[p.Number], in.Closes[p.Number], maint, partners, now)
		if g, ok := groupOf[strings.ToLower(p.Author)]; ok {
			row.Group = g
		} else {
			row.Group = "community"
		}
		for _, s := range row.Services {
			services[s] = true
		}
		for _, l := range row.Labels {
			labels[l] = true
		}
		d.PRs = append(d.PRs, row)
	}
	d.Services = sortedKeys(services)
	d.Labels = sortedKeys(labels)
	return d
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// derive computes one PR's row by replaying its timeline.
func derive(p *db.PR, events []db.Event, commits []db.Commit, verdicts map[string]db.Verdict, closes []db.LinkedIssue, maint, partners map[string]bool, now time.Time) PR {
	author := strings.ToLower(p.Author)
	isMaint := func(login string) bool {
		return maint[strings.ToLower(login)] && !strings.EqualFold(login, author) && !isBot(login)
	}
	isPartner := func(login string) bool {
		return partners[strings.ToLower(login)] && !strings.EqualFold(login, author) && !isBot(login)
	}
	isAuthor := func(login string) bool { return strings.EqualFold(login, author) }

	row := PR{
		Number: p.Number, Title: p.Title, Author: p.Author, Assoc: p.AuthorAssociation,
		Draft: p.IsDraft, Created: p.CreatedAt.Unix(), Updated: p.UpdatedAt.Unix(),
		MergedBy: p.MergedBy, Milestone: p.Milestone, Labels: p.Labels,
		Files: p.ChangedFiles, Adds: p.Additions, Dels: p.Deletions, Comments: p.CommentCount,
		Decision: strings.ToLower(p.ReviewDecision), Mergeable: strings.ToLower(p.Mergeable), Thumbs: p.ThumbsUp,
		FirstResponseDays: -1, ResolveDays: -1, URL: p.URL, CI: ci(p),
		ReviewedBy: []string{}, ApprovedBy: []string{}, ChangesBy: []ReviewerCount{},
	}
	if row.Labels == nil {
		row.Labels = []string{}
	}
	switch p.State {
	case db.PRMerged:
		row.State = stateMerged
		row.Merged = p.MergedAt.Unix()
		row.Closed = p.ClosedAt.Unix()
		if row.Closed <= 0 {
			row.Closed = row.Merged
		}
	case db.PRClosed:
		row.State = stateClosed
		row.Closed = p.ClosedAt.Unix()
	default:
		row.State = stateOpen
	}
	end := now
	if row.State != stateOpen && row.Closed > 0 {
		end = time.Unix(row.Closed, 0).UTC()
		row.ResolveDays = days(p.CreatedAt, end)
	}
	row.Services, row.Kinds = areas(p.Files)
	for _, c := range closes {
		row.LinkedIssues = append(row.LinkedIssues, c.IssueNumber)
	}
	if len(verdicts) > 0 {
		row.AI = map[string]float64{}
		for pass, v := range verdicts {
			row.AI[pass] = v.Confidence
		}
	}

	// ---- the replay ----
	slices.SortStableFunc(events, func(a, b db.Event) int { return a.CreatedAt.Compare(b.CreatedAt) })

	// did it start as a draft? the first draft-flip tells: a ready-for-review
	// first means it opened draft; nothing at all means it is what it is now
	draft := p.IsDraft
	for _, e := range events {
		if e.Type == db.EventReadyForReview {
			draft = true
			break
		}
		if e.Type == db.EventConvertToDraft {
			draft = false
			break
		}
	}

	type st struct {
		draft, waiting, blocked bool
		review                  string // "" | reviewChanges | reviewApproved
		authorActed             bool   // pushed since the last changes-requested
	}
	s := st{draft: draft}
	state := func() int {
		switch {
		case s.draft:
			return StateDraft
		case s.blocked:
			return StateBlocked
		case s.waiting:
			return StateWaiting
		case s.review == reviewChanges && !s.authorActed:
			return StateWaiting
		case s.review == reviewApproved:
			return StateApproved
		default:
			return StateAwaiting
		}
	}
	court := func() string {
		switch state() {
		case StateDraft, StateWaiting:
			return CourtAuthor
		default:
			return CourtMaintainer
		}
	}

	cur := state()
	curStart := p.CreatedAt
	curCourt := court()
	courtSince := p.CreatedAt
	// the review status: each side's last verdict, packed into one code
	vm, vp := ReviewNone, ReviewNone // the members', the partners'
	rs, rsStart := statusCode(vm, vp), p.CreatedAt
	setStatus := func(at time.Time) {
		ns := statusCode(vm, vp)
		if ns == rs {
			return
		}
		row.Statuses = append(row.Statuses, Interval{Start: rsStart.Unix(), End: at.Unix(), State: rs})
		rs, rsStart = ns, at
	}
	// verdict applies a review or comment by one side
	verdict := func(side *int, state string, at time.Time) {
		switch state {
		case "CHANGES_REQUESTED":
			*side = ReviewChanges
		case "APPROVED":
			*side = ReviewApproved
		case "DISMISSED":
			if *side != ReviewNone {
				*side = ReviewCommented
			}
		default:
			if *side == ReviewNone {
				*side = ReviewCommented
			}
		}
		setStatus(at)
	}
	wearing := map[string]time.Time{} // label or ms:milestone → since when
	wear := func(name string, at time.Time) {
		if _, on := wearing[name]; !on {
			wearing[name] = at
		}
	}
	shed := func(name string, at time.Time) {
		if since, on := wearing[name]; on {
			row.Spans = append(row.Spans, Span{Name: name, Start: since.Unix(), End: at.Unix()})
			delete(wearing, name)
		}
	}
	var waitingStart time.Time
	if s.waiting {
		waitingStart = p.CreatedAt
	}
	lastActivity := p.CreatedAt
	reviewers := map[string]int{}
	// the review roll: dedupe by login, keep the first spelling seen
	reviewedBy, approvedBy := map[string]bool{}, map[string]bool{}
	changesAt := map[string]int{} // login → index into row.ChangesBy
	noteReview := func(e *db.Event) {
		who, n := strings.ToLower(e.Actor), reviewComments(e)
		row.ReviewComments += n
		if !reviewedBy[who] {
			reviewedBy[who] = true
			row.ReviewedBy = append(row.ReviewedBy, e.Actor)
		}
		switch e.State {
		case db.DecisionApproved:
			if !approvedBy[who] {
				approvedBy[who] = true
				row.ApprovedBy = append(row.ApprovedBy, e.Actor)
			}
		case db.ReviewChangesRequested:
			i, seen := changesAt[who]
			if !seen {
				i = len(row.ChangesBy)
				changesAt[who] = i
				row.ChangesBy = append(row.ChangesBy, ReviewerCount{Login: e.Actor})
			}
			row.ChangesBy[i].Requests++
			row.ChangesBy[i].Comments += n
		}
	}

	push := func(at time.Time) {
		ns := state()
		if ns != cur {
			row.Intervals = append(row.Intervals, Interval{Start: curStart.Unix(), End: at.Unix(), State: cur})
			cur, curStart = ns, at
		}
		if c := court(); c != curCourt {
			curCourt, courtSince = c, at
		}
	}

	row.Events = make([]Event, 0, len(events))
	for i := range events {
		e := &events[i]
		at := e.CreatedAt
		if at.IsZero() {
			continue
		}
		if row.State != stateOpen && at.After(end) {
			// activity after the close (a reopen would have flipped the state on github)
			row.Events = append(row.Events, compact(e))
			continue
		}
		who := strings.ToLower(e.Actor)
		human := !isBot(e.Actor)
		if human && !at.Before(lastActivity) {
			lastActivity = at
		}
		if human && isMaint(e.Actor) {
			row.LastMaint = at.Unix()
		}
		if isAuthor(e.Actor) {
			row.LastAuthor = at.Unix()
		}

		switch e.Type {
		case db.EventLabelled:
			wear(e.Label, at)
			if e.Label == labelWaiting && !s.waiting {
				s.waiting = true
				waitingStart = at
				row.WaitingCycles++
			}
		case db.EventUnlabelled:
			shed(e.Label, at)
			if e.Label == labelWaiting && s.waiting {
				s.waiting = false
				row.WaitingDays += days(waitingStart, at)
			}
		case db.EventMilestoned:
			for name := range wearing { // one milestone at a time
				if strings.HasPrefix(name, "ms:") {
					shed(name, at)
				}
			}
			wear("ms:"+e.Milestone, at)
			s.blocked = strings.EqualFold(e.Milestone, milestoneBlocked)
		case db.EventDemilestoned:
			shed("ms:"+e.Milestone, at)
			if strings.EqualFold(e.Milestone, milestoneBlocked) {
				s.blocked = false
			}
		case db.EventReadyForReview:
			s.draft = false
		case db.EventConvertToDraft:
			s.draft = true
		case db.EventReview:
			if human && !isAuthor(e.Actor) {
				noteReview(e)
			}
			if isMaint(e.Actor) {
				reviewers[who]++
				row.Reviews++
				switch e.State {
				case "CHANGES_REQUESTED":
					s.review, s.authorActed = reviewChanges, false
					row.Rounds++
				case "APPROVED":
					s.review = reviewApproved
					row.Approvals++
				}
				verdict(&vm, e.State, at)
				firstResponse(&row, e.Actor, p.CreatedAt, at)
			} else if !isAuthor(e.Actor) && human {
				row.Reviews++
				if isPartner(e.Actor) {
					verdict(&vp, e.State, at)
				}
			}
		case db.EventReviewDismissed:
			s.review = ""
			// whose review was dismissed: the dismissed review's author is the subject
			switch {
			case isMaint(e.Subject):
				verdict(&vm, "DISMISSED", at)
			case isPartner(e.Subject):
				verdict(&vp, "DISMISSED", at)
			}
		case db.EventComment:
			switch {
			case isMaint(e.Actor):
				reviewers[who]++
				firstResponse(&row, e.Actor, p.CreatedAt, at)
				verdict(&vm, "", at)
			case isPartner(e.Actor):
				verdict(&vp, "", at)
			}
		case db.EventCommit, db.EventHeadRefForcePush:
			// the author (or anyone with push) moving the code: a changes-requested
			// round is answered, and an approval on old code is stale
			if !isMaint(e.Actor) {
				s.authorActed = true
			}
		case db.EventClosed, db.EventMerged:
			if isMaint(e.Actor) {
				firstResponse(&row, e.Actor, p.CreatedAt, at)
			}
		}
		push(at)
		row.Events = append(row.Events, compact(e))
	}
	// commits missing from the events (a truncated timeline) still count as
	// author activity for the last-activity stamp
	for _, c := range commits {
		if !c.CommittedAt.IsZero() && c.CommittedAt.After(lastActivity) && !c.CommittedAt.After(end) {
			lastActivity = c.CommittedAt
		}
	}

	// close out the running intervals, spans, and the waiting tally
	row.Intervals = append(row.Intervals, Interval{Start: curStart.Unix(), End: intervalEnd(row.State, end), State: cur})
	row.Statuses = append(row.Statuses, Interval{Start: rsStart.Unix(), End: intervalEnd(row.State, end), State: rs})
	for name, since := range wearing {
		row.Spans = append(row.Spans, Span{Name: name, Start: since.Unix(), End: intervalEnd(row.State, end)})
	}
	slices.SortFunc(row.Spans, func(a, b Span) int {
		if c := cmp.Compare(a.Start, b.Start); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	if row.Spans == nil {
		row.Spans = []Span{}
	}
	if s.waiting {
		row.WaitingDays += days(waitingStart, end)
	}
	row.LastActivity = lastActivity.Unix()
	if row.State == stateOpen {
		row.Court = curCourt
		row.CourtSince = courtSince.Unix()
	}
	row.Reviewers = make([]string, 0, len(reviewers))
	for r := range reviewers {
		row.Reviewers = append(row.Reviewers, r)
	}
	if row.Intervals == nil {
		row.Intervals = []Interval{}
	}
	slices.SortFunc(row.Reviewers, func(a, b string) int {
		if reviewers[a] != reviewers[b] {
			return cmp.Compare(reviewers[b], reviewers[a]) // most active first
		}
		return strings.Compare(a, b)
	})
	row.Effort = effort(p, row.Services, row.Kinds)
	return row
}

// ci names the head commit's combined check state the way ghp-sync's CI
// field does; "" for a closed PR (stale) or one with no checks.
func ci(p *db.PR) string {
	if p.State != db.PROpen {
		return ""
	}
	switch p.CheckState {
	case "SUCCESS":
		return "passing"
	case "FAILURE", "ERROR":
		return "failing"
	case "PENDING", "EXPECTED":
		return "running"
	default:
		return ""
	}
}

// reviewComments reads a review's inline comment count off the raw node
// (`comments { totalCount }`); 0 for events fetched before it was selected.
func reviewComments(e *db.Event) int {
	var n struct {
		Comments struct {
			TotalCount int `json:"totalCount"`
		} `json:"comments"`
	}
	if e.Raw == "" || json.Unmarshal([]byte(e.Raw), &n) != nil {
		return 0
	}
	return n.Comments.TotalCount
}

func intervalEnd(state string, end time.Time) int64 {
	if state == stateOpen {
		return 0
	}
	return end.Unix()
}

func firstResponse(row *PR, who string, created, at time.Time) {
	if row.FirstResponseDays < 0 {
		row.FirstResponseDays = days(created, at)
		row.FirstResponder = who
	}
}

func days(from, to time.Time) float64 {
	if to.Before(from) {
		return 0
	}
	return float64(to.Sub(from)) / float64(24*time.Hour)
}

// compact shortens an event for the page: [when, kind, who, what].
func compact(e *db.Event) Event {
	ev := Event{At: e.CreatedAt.Unix(), Who: e.Actor}
	switch e.Type {
	case db.EventLabelled:
		ev.Kind, ev.What = "label+", e.Label
	case db.EventUnlabelled:
		ev.Kind, ev.What = "label-", e.Label
	case db.EventClosed:
		ev.Kind = "close"
	case db.EventReopened:
		ev.Kind = "reopen"
	case db.EventMerged:
		ev.Kind = "merge"
	case db.EventReviewRequested:
		ev.Kind, ev.What = "review?", e.Subject
	case db.EventReviewRequestGone:
		ev.Kind, ev.What = "review?-", e.Subject
	case db.EventReadyForReview:
		ev.Kind = "ready"
	case db.EventConvertToDraft:
		ev.Kind = "draft"
	case db.EventMilestoned:
		ev.Kind, ev.What = "milestone+", e.Milestone
	case db.EventDemilestoned:
		ev.Kind, ev.What = "milestone-", e.Milestone
	case db.EventAssigned:
		ev.Kind, ev.What = "assign", e.Subject
	case "UnassignedEvent":
		ev.Kind, ev.What = "assign-", e.Subject
	case db.EventHeadRefForcePush:
		ev.Kind = "force-push"
	case db.EventRenamedTitle:
		ev.Kind, ev.What = "rename", e.Subject
	case "BaseRefChangedEvent":
		ev.Kind = "rebase"
	case db.EventReviewDismissed:
		ev.Kind, ev.What = "dismiss", e.Subject
	case db.EventCrossReferenced:
		ev.Kind, ev.What = "xref", e.Subject
	case db.EventCommit:
		ev.Kind = "commit"
		var n struct {
			Commit struct {
				MessageHeadline string `json:"messageHeadline"`
			} `json:"commit"`
		}
		if json.Unmarshal([]byte(e.Raw), &n) == nil {
			ev.What = n.Commit.MessageHeadline
		}
	case db.EventReview:
		ev.Kind, ev.What, ev.N = "review", strings.ToLower(e.State), reviewComments(e)
	case db.EventComment:
		ev.Kind = "comment"
	case "HeadRefDeletedEvent":
		ev.Kind = "branch-deleted"
	default:
		ev.Kind = strings.ToLower(strings.TrimSuffix(e.Type, "Event"))
	}
	return ev
}

// areas reads the provider's layout off the changed paths: the service
// packages touched and the kinds of change (docs, vendor, tests, ci, schema,
// changelog, other).
func areas(files []string) (services, kinds []string) {
	svc := map[string]bool{}
	kind := map[string]bool{}
	for _, f := range files {
		switch {
		case strings.HasPrefix(f, "internal/services/"):
			if name, _, ok := strings.Cut(strings.TrimPrefix(f, "internal/services/"), "/"); ok {
				svc[name] = true
			}
			base := path.Base(f)
			switch {
			case strings.HasSuffix(base, "_test.go"):
				kind["tests"] = true
			case strings.HasSuffix(base, ".go") && (strings.Contains(base, "resource") || strings.Contains(base, "data_source")):
				kind["schema"] = true
			case strings.HasSuffix(base, ".go"):
				kind["code"] = true
			default:
				kind["other"] = true
			}
		case strings.HasPrefix(f, "website/") || strings.HasPrefix(f, "docs/"):
			kind["docs"] = true
		case strings.HasPrefix(f, "vendor/") || f == "go.mod" || f == "go.sum":
			kind["vendor"] = true
		case strings.HasPrefix(f, ".github/") || strings.HasPrefix(f, "scripts/") || strings.HasPrefix(f, ".teamcity/") || strings.HasSuffix(f, "Makefile") || strings.HasSuffix(f, "GNUmakefile"):
			kind["ci"] = true
		case strings.EqualFold(path.Base(f), "CHANGELOG.md"):
			kind["changelog"] = true
		case strings.HasSuffix(f, "_test.go"):
			kind["tests"] = true
		case strings.HasSuffix(f, ".go"):
			kind["code"] = true
		default:
			kind["other"] = true
		}
	}
	services = sortedKeys(svc)
	kinds = sortedKeys(kind)
	if services == nil {
		services = []string{}
	}
	if kinds == nil {
		kinds = []string{}
	}
	return services, kinds
}

// effort is a 1..5 review-cost estimate from the diff shape: lines changed,
// discounted when the change is docs or vendor only, bumped when it spans
// several services or touches schema.
func effort(p *db.PR, services, kinds []string) int {
	lines := float64(p.Additions + p.Deletions)
	only := func(k string) bool { return len(kinds) == 1 && kinds[0] == k }
	if only("vendor") || only("docs") || only("changelog") {
		lines *= 0.15
	}
	var e int
	switch {
	case lines < 40:
		e = 1
	case lines < 200:
		e = 2
	case lines < 700:
		e = 3
	case lines < 2000:
		e = 4
	default:
		e = 5
	}
	if len(services) >= 3 && e < 5 {
		e++
	}
	return e
}
