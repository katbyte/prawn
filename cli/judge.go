// The shared AI-judging plumbing every check rides: the thin wrapper over
// lib/pr's judge engine, prompt loading, and score colouring.

package cli

import (
	"time"

	"github.com/katbyte/prawn/assets"
	"github.com/katbyte/prawn/lib/db"
	"github.com/katbyte/prawn/lib/pr"
	"github.com/katbyte/prawn/lib/text"

	"github.com/katbyte/go-kt/cout"
)

// JudgeBlocks runs the shared judge (pr.Judge) configured from the flags.
// The model canonicalises inside NewJudge; it is copied back so every later
// display matches the identity verdicts are cached under.
func (f *FlagData) JudgeBlocks(d *db.DB, pass, promptText string, items []pr.JudgeItem,
	onReady func() (bool, error), onBatch func([]pr.Judged) (bool, error),
) (map[int]*pr.Verdict, error) {
	return f.JudgeBlocksBatch(d, pass, promptText, 0, items, onReady, onBatch)
}

// JudgeBlocksBatch is JudgeBlocks with a smaller batch for passes whose blocks
// are huge — resolved ships the full thread, so fewer candidates per call buys
// each one more room. batch <= 0 keeps the judge's default.
func (f *FlagData) JudgeBlocksBatch(d *db.DB, pass, promptText string, batch int, items []pr.JudgeItem,
	onReady func() (bool, error), onBatch func([]pr.Judged) (bool, error),
) (map[int]*pr.Verdict, error) {
	if err := f.RequireAI(); err != nil {
		return nil, err
	}
	j := pr.NewJudge(d, f.AI.Cmd, f.AI.Model, time.Duration(f.AI.TimeoutMinutes)*time.Minute)
	if batch > 0 {
		j.BatchSize = batch
	}
	f.AI.Model = j.Model()
	return j.Blocks(pass, promptText, items, onReady, onBatch)
}

// PreparePrompt loads a prompt template by name.
func (f *FlagData) PreparePrompt(name string) (string, error) {
	return assets.Prompt(name)
}

const (
	// JudgeThreshold is the minimum AI confidence for an apply mode to act;
	// below it the finding is reported and skipped. Also --apply-with-ai-auto's
	// bare-flag default.
	JudgeThreshold = 0.7

	// judge-block budgets: how much of a PR body and a single comment go in —
	// accuracy beats cost, so the slices are generous.
	PRBodyRunes  = 3000
	CommentRunes = 400

	// shared colour tag names for the class/score/state tag helpers.
	TagGreen     = "green"
	TagYellow    = "yellow"
	TagOrange    = "fg=208"
	TagRed       = "red"
	TagLightBlue = "lightBlue"
	TagGray      = "gray"
)

// PrintVerdict prints the AI's score and reason for a judged finding.
func PrintVerdict(v *pr.Verdict) {
	if v == nil {
		return
	}
	cout.Printf("\n      <gray>AI:</> <%s>%.2f</>\n", ScoreTag(v.Confidence), v.Confidence)
	cout.Printf("        <lightWhite>%s</>\n", text.OneLine(v.Reason))
}

// ScoreTag colours a match confidence: green at or above the apply threshold,
// orange in the murky middle, red for a clear non-match.
func ScoreTag(confidence float64) string {
	switch {
	case confidence >= JudgeThreshold:
		return TagGreen
	case confidence >= 0.4:
		return TagOrange
	default:
		return TagRed
	}
}

// AssocDisplay returns an author's colour tag and the association label to
// show ("" = say nothing): maintainers green, contributors light blue, plain
// users white with no label — NONE just means no repo affiliation. Orange is
// deliberately avoided: the open-state tag owns it in these cards.
func AssocDisplay(assoc string) (tag, label string) {
	switch assoc {
	case "MEMBER", "OWNER", "COLLABORATOR":
		return TagGreen, "maintainer"
	case "CONTRIBUTOR":
		return TagLightBlue, "contributor"
	case "FIRST_TIME_CONTRIBUTOR", "FIRST_TIMER":
		return "white", "first-time"
	default:
		return "white", ""
	}
}

// OrDash renders an empty string as an em-dash for tabular output.
func OrDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
