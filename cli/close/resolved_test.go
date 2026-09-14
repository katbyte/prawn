package close

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/prawn/cli"
	"github.com/katbyte/prawn/lib/db"
)

// TestResolvedJudgeItemsExhaustive pins the resolved check's contract with
// its judge: the block carries EVERYTHING prawn holds on a candidate — the
// full body, every changed file, every review, every human comment (bots
// omitted but counted), and the evidence — not a digest.
func TestResolvedJudgeItemsExhaustive(t *testing.T) {
	t.Parallel()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening db: %v", err)
	}
	defer func() { _ = d.Close() }()

	when := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	bundle := db.PRBundle{
		PR: db.PR{
			Number: 42, Title: "azurerm_widget - fix frobulation", State: db.PROpen,
			Author: "someone", AuthorAssociation: "CONTRIBUTOR",
			CreatedAt: when, UpdatedAt: when, Labels: []string{"size/S"},
			Mergeable: "MERGEABLE", ReviewDecision: db.DecisionChangesRequested,
			Additions: 10, Deletions: 2, ChangedFiles: 2,
			Files: []string{"internal/services/widget/widget_resource.go", "internal/services/widget/widget_resource_test.go"},
			Body:  "This fixes frobulation.\n\nAlso sneaks in a refactor of the defrobnicator.",
		},
		Comments: []db.Comment{
			{ID: "c1", PRNumber: 42, Author: "maint", AuthorAssociation: "MEMBER", CreatedAt: when, Body: "still broken after #100 shipped"},
			{ID: "c2", PRNumber: 42, Author: "github-actions", CreatedAt: when, Body: "CLA signed"},
		},
		Reviews: []db.Review{
			{ID: "r1", PRNumber: 42, Author: "maint", AuthorAssociation: "MEMBER", State: db.ReviewChangesRequested, SubmittedAt: when, Body: "please gate this behind a feature flag"},
		},
		Closes: []db.LinkedIssue{
			{PRNumber: 42, IssueNumber: 7, Title: "frobulation is broken", State: db.PRClosed, StateReason: "COMPLETED", Labels: []string{"bug"}},
		},
	}
	if err := d.SavePRs([]db.PRBundle{bundle}, "", ""); err != nil {
		t.Fatalf("saving bundle: %v", err)
	}
	p, err := d.GetPR(42)
	if err != nil || p == nil {
		t.Fatalf("reading PR back: %v", err)
	}

	f := &Flags{FlagData: &cli.FlagData{}}
	closes, _ := d.ClosesFor(42)
	items, err := f.resolvedJudgeItems(d, []resolvedFinding{{pr: p, closes: closes, class: classIssueClosed}})
	if err != nil {
		t.Fatalf("building judge items: %v", err)
	}
	if len(items) != 1 || items[0].Number != 42 {
		t.Fatalf("expected one item for #42, got %+v", items)
	}
	block := items[0].Block

	for _, want := range []string{
		"sneaks in a refactor",                        // the FULL body, not a truncation of it
		"widget_resource.go",                          // every changed file
		"widget_resource_test.go",                     //
		"labels: size/S",                              // labels and state
		"review decision CHANGES_REQUESTED",           //
		"please gate this behind",                     // every review, with its body
		"still broken after #100",                     // every human comment
		"1 bot comments omitted",                      // bots dropped but accounted for
		"#7 \"frobulation is broken\"",                // the evidence: the linked issue
		"closed as COMPLETED (labels: bug)",           //
		"CLASS: " + strings.ToUpper(classIssueClosed), //
	} {
		if !strings.Contains(block, want) {
			t.Errorf("judge block is missing %q\nblock:\n%s", want, block)
		}
	}
	if strings.Contains(block, "CLA signed") {
		t.Error("bot comment bodies should be omitted from the block")
	}
}
