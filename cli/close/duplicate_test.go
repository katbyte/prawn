package close

import (
	"testing"

	"github.com/katbyte/prawn/lib/db"
)

func TestNormaliseTitle(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"`azurerm_storage_account` - fix soft delete!", "azurerm storage account fix soft delete"},
		{"Fix: soft-delete on azurerm_storage_account", "fix soft delete on azurerm storage account"},
		{"WIP", ""}, // too short to mean anything
		{"", ""},
	}
	for _, c := range cases {
		if got := normaliseTitle(c.in); got != c.want {
			t.Errorf("normaliseTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPickSurvivor(t *testing.T) {
	t.Parallel()
	older := &db.PR{Number: 10, CommentCount: 1}
	busy := &db.PR{Number: 20, CommentCount: 8}
	approved := &db.PR{Number: 30, ReviewDecision: db.DecisionApproved}
	draft := &db.PR{Number: 5, IsDraft: true, CommentCount: 50}

	// approval beats everything, discussion beats age, age breaks ties, and a
	// draft never survives over a ready PR
	if got := pickSurvivor([]*db.PR{older, busy, approved}); got != approved {
		t.Errorf("approved should survive, got #%d", got.Number)
	}
	if got := pickSurvivor([]*db.PR{older, busy}); got != busy {
		t.Errorf("the discussed PR should survive, got #%d", got.Number)
	}
	if got := pickSurvivor([]*db.PR{{Number: 40}, {Number: 41}}); got.Number != 40 {
		t.Errorf("the older PR should survive the tie, got #%d", got.Number)
	}
	if got := pickSurvivor([]*db.PR{draft, older}); got != older {
		t.Errorf("a ready PR should survive over a draft, got #%d", got.Number)
	}
}

func TestFilesOverlap(t *testing.T) {
	t.Parallel()
	a := &db.PR{Files: []string{"internal/services/storage/a.go", "internal/services/storage/a_test.go"}}
	b := &db.PR{Files: []string{"internal/services/storage/a.go"}}
	c := &db.PR{Files: []string{"internal/services/compute/b.go"}}
	empty := &db.PR{}

	if !filesOverlap(a, b) {
		t.Error("a and b share a path")
	}
	if filesOverlap(a, c) {
		t.Error("a and c share nothing")
	}
	if filesOverlap(a, empty) {
		t.Error("no files known means no overlap claim")
	}
}
