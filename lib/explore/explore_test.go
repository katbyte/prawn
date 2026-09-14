package explore

import (
	"strconv"
	"testing"
	"time"

	"github.com/katbyte/prawn/lib/db"
)

func day(n int) time.Time { return time.Date(2026, 1, 1+n, 12, 0, 0, 0, time.UTC) }

func ev(id, typ, actor string, at time.Time, label, state string) db.Event {
	return db.Event{ID: id, PRNumber: 1, Type: typ, Actor: actor, CreatedAt: at, Label: label, State: state}
}

// a community PR: reviewed with changes requested on day 2, labelled
// waiting-response on day 3, the author pushes on day 10 (which lifts the
// label), and it sits in the maintainers' court since
func TestDeriveReplay(t *testing.T) {
	t.Parallel()
	p := &db.PR{
		Number: 1, State: db.PROpen, Author: "alice", CreatedAt: day(0), UpdatedAt: day(10),
		Files: []string{"internal/services/compute/virtual_machine_resource.go", "website/docs/r/virtual_machine.html.markdown"}, Additions: 100, Deletions: 20,
	}
	changes := ev("1", db.EventReview, "kat", day(2), "", "CHANGES_REQUESTED")
	changes.Raw = `{"__typename":"PullRequestReview","state":"CHANGES_REQUESTED","comments":{"totalCount":3}}`
	events := []db.Event{
		changes,
		ev("2", db.EventLabelled, "kat", day(3), "waiting-response", ""),
		ev("3", db.EventComment, "alice", day(9), "", ""),
		ev("4", db.EventCommit, "alice", day(10), "", ""),
		ev("5", db.EventUnlabelled, "hashibot", day(10), "waiting-response", ""),
	}
	now := day(20)
	row := derive(p, events, nil, nil, nil, map[string]bool{"kat": true}, nil, now)

	if row.Court != CourtMaintainer {
		t.Fatalf("court = %q, want maintainer", row.Court)
	}
	if row.CourtSince != day(10).Unix() {
		t.Errorf("court since = %s, want day 10", time.Unix(row.CourtSince, 0).UTC())
	}
	if row.Rounds != 1 || row.Reviews != 1 {
		t.Errorf("rounds/reviews = %d/%d, want 1/1", row.Rounds, row.Reviews)
	}
	if got := int(row.FirstResponseDays + 0.5); got != 2 || row.FirstResponder != "kat" {
		t.Errorf("first response = %.1f by %q, want 2 by kat", row.FirstResponseDays, row.FirstResponder)
	}
	if row.WaitingCycles != 1 || int(row.WaitingDays+0.5) != 7 {
		t.Errorf("waiting = %.1f days over %d cycles, want 7 over 1", row.WaitingDays, row.WaitingCycles)
	}
	// awaiting (0..2), waiting from the review (2..10), awaiting again (10..)
	want := []int{StateAwaiting, StateWaiting, StateAwaiting}
	if len(row.Intervals) != len(want) {
		t.Fatalf("intervals = %+v, want states %v", row.Intervals, want)
	}
	for i, iv := range row.Intervals {
		if iv.State != want[i] {
			t.Errorf("interval %d state = %d, want %d", i, iv.State, want[i])
		}
	}
	if row.Intervals[2].End != 0 {
		t.Errorf("open PR's last interval should be open-ended, got %d", row.Intervals[2].End)
	}
	if len(row.Services) != 1 || row.Services[0] != "compute" {
		t.Errorf("services = %v, want [compute]", row.Services)
	}
	if len(row.Kinds) != 2 || row.Kinds[0] != "docs" || row.Kinds[1] != "schema" {
		t.Errorf("kinds = %v, want [docs schema]", row.Kinds)
	}
	if row.Effort != 2 {
		t.Errorf("effort = %d, want 2", row.Effort)
	}
	if row.LastMaint != day(3).Unix() || row.LastAuthor != day(10).Unix() {
		t.Errorf("last maint/author = %d/%d", row.LastMaint, row.LastAuthor)
	}
	// the review roll: kat reviewed and requested changes once with 3 comments, nobody approved
	if len(row.ReviewedBy) != 1 || row.ReviewedBy[0] != "kat" || len(row.ApprovedBy) != 0 {
		t.Errorf("reviewed/approved by = %v/%v, want [kat]/[]", row.ReviewedBy, row.ApprovedBy)
	}
	if len(row.ChangesBy) != 1 || row.ChangesBy[0] != (ReviewerCount{Login: "kat", Requests: 1, Comments: 3}) || row.ReviewComments != 3 {
		t.Errorf("changes by = %+v (%d comments), want kat ×1 ✎3", row.ChangesBy, row.ReviewComments)
	}
}

// the review roll dedupes by login and counts every change request; the
// author's own review and bots are left out
func TestDeriveReviewRoll(t *testing.T) {
	t.Parallel()
	p := &db.PR{Number: 4, State: db.PROpen, Author: "alice", CreatedAt: day(0), CheckState: "FAILURE"}
	cr := func(id, who string, d, comments int) db.Event {
		e := ev(id, db.EventReview, who, day(d), "", "CHANGES_REQUESTED")
		e.Raw = `{"comments":{"totalCount":` + strconv.Itoa(comments) + `}}`
		return e
	}
	events := []db.Event{
		cr("1", "matt", 1, 4),
		ev("2", db.EventReview, "alice", day(2), "", "COMMENTED"),
		ev("3", db.EventReview, "github-actions", day(2), "", "COMMENTED"),
		cr("4", "matt", 3, 2),
		ev("5", db.EventReview, "kat", day(4), "", "APPROVED"),
		ev("6", db.EventReview, "kat", day(5), "", "APPROVED"),
	}
	row := derive(p, events, nil, nil, nil, map[string]bool{"kat": true, "matt": true}, nil, day(10))
	if got := row.ReviewedBy; len(got) != 2 || got[0] != "matt" || got[1] != "kat" {
		t.Errorf("reviewed by = %v, want [matt kat]", got)
	}
	if got := row.ApprovedBy; len(got) != 1 || got[0] != "kat" {
		t.Errorf("approved by = %v, want [kat]", got)
	}
	if len(row.ChangesBy) != 1 || row.ChangesBy[0] != (ReviewerCount{Login: "matt", Requests: 2, Comments: 6}) {
		t.Errorf("changes by = %+v, want matt ×2 ✎6", row.ChangesBy)
	}
	if row.ReviewComments != 6 {
		t.Errorf("review comments = %d, want 6", row.ReviewComments)
	}
	// the compact events carry each review's comment count for the trends
	if row.Events[0].Kind != "review" || row.Events[0].N != 4 || row.Events[3].N != 2 {
		t.Errorf("compact review events = %+v, want comment counts 4 and 2", row.Events[:4])
	}
	if row.CI != "failing" {
		t.Errorf("ci = %q, want failing", row.CI)
	}
}

func TestCI(t *testing.T) {
	t.Parallel()
	cases := []struct{ state, check, want string }{
		{db.PROpen, "SUCCESS", "passing"},
		{db.PROpen, "FAILURE", "failing"},
		{db.PROpen, "ERROR", "failing"},
		{db.PROpen, "PENDING", "running"},
		{db.PROpen, "EXPECTED", "running"},
		{db.PROpen, "", ""},
		{db.PRMerged, "SUCCESS", ""},
		{db.PRClosed, "FAILURE", ""},
	}
	for _, tc := range cases {
		if got := ci(&db.PR{State: tc.state, CheckState: tc.check}); got != tc.want {
			t.Errorf("ci(%s, %q) = %q, want %q", tc.state, tc.check, got, tc.want)
		}
	}
}

// approved and merged: the interval ends at the merge and the court is empty
func TestDeriveMerged(t *testing.T) {
	t.Parallel()
	p := &db.PR{Number: 2, State: db.PRMerged, Author: "bob", CreatedAt: day(0), MergedAt: day(4), ClosedAt: day(4), MergedBy: "kat"}
	events := []db.Event{
		ev("1", db.EventReview, "kat", day(3), "", "APPROVED"),
		ev("2", db.EventMerged, "kat", day(4), "", ""),
	}
	row := derive(p, events, nil, nil, nil, map[string]bool{"kat": true}, nil, day(30))
	if row.State != "merged" || row.Court != "" || int(row.ResolveDays+0.5) != 4 {
		t.Errorf("state/court/resolve = %q/%q/%.1f", row.State, row.Court, row.ResolveDays)
	}
	if row.Approvals != 1 || row.Rounds != 0 {
		t.Errorf("approvals/rounds = %d/%d", row.Approvals, row.Rounds)
	}
	last := row.Intervals[len(row.Intervals)-1]
	if last.State != StateApproved || last.End != day(4).Unix() {
		t.Errorf("last interval = %+v, want approved ending at the merge", last)
	}
}

func TestEffortDiscountsDocsOnly(t *testing.T) {
	t.Parallel()
	docs := &db.PR{Additions: 900, Deletions: 100}
	if e := effort(docs, nil, []string{"docs"}); e != 2 {
		t.Errorf("docs-only 1000 lines: effort %d, want 2", e)
	}
	code := &db.PR{Additions: 900, Deletions: 100}
	if e := effort(code, []string{"a", "b", "c"}, []string{"code"}); e != 5 {
		t.Errorf("1000 lines over 3 services: effort %d, want 5", e)
	}
}

// the two review sides: a partner comments, a member approves — the packed
// status ends as members approved (3) × partners commented (1)
func TestDeriveReviewSides(t *testing.T) {
	t.Parallel()
	p := &db.PR{Number: 3, State: db.PROpen, Author: "carol", CreatedAt: day(0)}
	events := []db.Event{
		ev("1", db.EventComment, "magodo", day(1), "", ""),
		ev("2", db.EventReview, "kat", day(2), "", "APPROVED"),
	}
	row := derive(p, events, nil, nil, nil, map[string]bool{"kat": true}, map[string]bool{"magodo": true}, day(10))
	want := []int{statusCode(0, 0), statusCode(0, 1), statusCode(3, 1)}
	if len(row.Statuses) != len(want) {
		t.Fatalf("statuses = %+v, want codes %v", row.Statuses, want)
	}
	for i, iv := range row.Statuses {
		if iv.State != want[i] {
			t.Errorf("status %d = %d, want %d", i, iv.State, want[i])
		}
	}
}
