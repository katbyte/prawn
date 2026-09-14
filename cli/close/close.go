// Package close holds the checks: every command that closes pull requests on
// evidence — fixed, duplicate, stale.
package close

import (
	"fmt"
	"slices"
	"strings"

	"github.com/katbyte/go-kt/cout"
	"github.com/katbyte/prawn/cli"
	"github.com/katbyte/prawn/lib/db"
	"github.com/katbyte/prawn/lib/gh"
	"github.com/katbyte/prawn/lib/pr"
	"github.com/katbyte/prawn/lib/text"
)

// Flags wraps the shared flag data so the checks keep their method form; the
// shared plumbing (JudgeBlocks, NewApplyPass, PreparePrompt...) promotes
// through the embedding. currentMajor is learned by collectDeprecated from the
// --src-dir checkout's upgrade guides, for the close comment's wording.
type Flags struct {
	*cli.FlagData
	currentMajor int
}

// flags is every check RunE's entry point to the fully populated Flags.
func flags() *Flags { return &Flags{FlagData: cli.GetFlags()} }

// NewFlags wraps already-read flag data — for commands outside this package
// (prawn explore) that run the checks.
func NewFlags(f *cli.FlagData) *Flags { return &Flags{FlagData: f} }

// evidence keys every check's action rows share.
const (
	evidenceKeyClass = "class"
	evidenceKeyAI    = "ai"
)

// closeReq is everything doClose needs to act on one candidate: the PR, the
// rendered comment, and the action-row metadata.
type closeReq struct {
	p        *db.PR
	class    string
	reason   string // the action row's reason code
	template string // which comment template rendered the comment
	comment  string
	evidence map[string]string
	source   string // the pass that proposed it
	question string // the interactive ask's wording
}

// doClose is the shared back half of every check's closeOne: the dry-run
// preview, the a/s ask when interactive, the live-state guard, the comment,
// the close itself, and the action record. The caller prints its card first.
func (f *Flags) doClose(d *db.DB, repo gh.Repo, req closeReq, v *pr.Verdict, throttle func(), ask bool) (int, error) {
	if f.DryRun {
		cout.Printf("      <yellow>dry-run: would comment (%d chars, %s.md) then close</>\n",
			len(req.comment), req.template)
		return pr.ApplyPreviewed, nil
	}

	if ask {
		res, perr := pr.Ask(req.question, req.comment, req.p.URL)
		if perr != nil || res != pr.AskAccept {
			return res, perr
		}
	}

	throttle()
	live, err := repo.GetPull(req.p.Number)
	if err != nil {
		cout.Errorf("      <red>fetching live state: %v</>\n", err)
		return pr.ApplyFailed, nil
	}
	if live.State != cli.RESTStateOpen {
		cout.Printf("      <gray>already closed on github — skipped</>\n")
		return pr.ApplySkipped, nil
	}

	throttle()
	if err := repo.CreateComment(req.p.Number, req.comment); err != nil {
		cout.Errorf("      <red>comment failed: %v</>\n", err)
		return pr.ApplyFailed, nil
	}
	throttle()
	if err := repo.ClosePull(req.p.Number); err != nil {
		cout.Errorf("      <red>close failed (comment was posted): %v</>\n", err)
		return pr.ApplyFailed, nil
	}

	cout.Printf("      <fg=28>commented + closed</>\n")
	cout.QuietOnlyf("%d@closed@%s\n", req.p.Number, req.reason)

	a := &db.Action{
		PRNumber: req.p.Number, Action: db.ActionClose, Reason: req.reason,
		Template: req.template, Evidence: req.evidence,
		Source: req.source, Status: db.StatusApplied, DecidedBy: f.Decider(),
	}
	if a.Evidence == nil {
		a.Evidence = map[string]string{}
	}
	a.Evidence[evidenceKeyClass] = req.class
	if v != nil {
		a.Confidence = v.Confidence
		a.Evidence[evidenceKeyAI] = v.Reason
	}
	if err := d.RecordAction(a); err != nil {
		return pr.ApplyFailed, err
	}
	return pr.ApplySet, nil
}

// keepSummary renders a check's keep-guard tallies as one line, e.g.
// "15 protected (approved 12 · high-engagement 3)".
func keepSummary(protected map[string]int) string {
	total := 0
	parts := make([]string, 0, len(protected))
	for _, k := range text.SortedKeys(protected) {
		total += protected[k]
		parts = append(parts, fmt.Sprintf("%s %d", k, protected[k]))
	}
	if total == 0 {
		return "0 protected"
	}
	return fmt.Sprintf("%d protected (%s)", total, strings.Join(parts, " · "))
}

// reportVerdicts scores a check's findings for the bare (report) invocation:
// everything judged (pipelined, cached), or nil with a note when --ai=false.
func (f *Flags) reportVerdicts(d *db.DB, pass, promptName string, items func() ([]pr.JudgeItem, error)) (map[int]*pr.Verdict, error) {
	if !f.AI.Enabled {
		cout.Printf("<gray>--ai=false: listing without scores</>\n")
		return nil, nil
	}
	its, err := items()
	if err != nil {
		return nil, err
	}
	promptText, err := f.PreparePrompt(promptName)
	if err != nil {
		return nil, err
	}
	return f.JudgeBlocks(d, pass, promptText, its, nil, nil)
}

// sortByConfidence orders findings surest first; unjudged findings sink.
func sortByConfidence[T any](findings []T, verdicts map[int]*pr.Verdict, number func(*T) int) {
	if verdicts == nil {
		return
	}
	slices.SortStableFunc(findings, func(a, b T) int {
		av, bv := -1.0, -1.0
		if v := verdicts[number(&a)]; v != nil {
			av = v.Confidence
		}
		if v := verdicts[number(&b)]; v != nil {
			bv = v.Confidence
		}
		switch {
		case av > bv:
			return -1
		case av < bv:
			return 1
		default:
			return 0
		}
	})
}

// writePRBlockHeader opens a judge block: the PR's identity, dates, size,
// files, and cleaned body.
func writePRBlockHeader(b *strings.Builder, p *db.PR, class string) {
	fmt.Fprintf(b, "### PR #%d: %s\n", p.Number, text.OneLine(p.Title))
	draft := ""
	if p.IsDraft {
		draft = ", DRAFT"
	}
	fmt.Fprintf(b, "by @%s (%s)%s, opened %s, last activity %s, review state: %s\n",
		p.Author, p.AuthorAssociation, draft,
		p.CreatedAt.Format("2006-01-02"), p.UpdatedAt.Format("2006-01-02"), text.OrDefault(p.ReviewDecision, "none"))
	fmt.Fprintf(b, "CLASS: %s\n", strings.ToUpper(class))
	fmt.Fprintf(b, "CHANGES: +%d/-%d over %d files: %s\n",
		p.Additions, p.Deletions, p.ChangedFiles, strings.Join(firstN(p.Files, 20), ", "))
	fmt.Fprintf(b, "PR BODY:\n%s\n", text.TruncateRunes(pr.CleanBody(p.Body), cli.PRBodyRunes))
}

// writeThreadDigest appends the informative slice of a PR's thread to a judge
// block.
func writeThreadDigest(b *strings.Builder, d *db.DB, number int) error {
	comments, err := d.CommentsFor(number)
	if err != nil {
		return err
	}
	picked := pr.DigestComments(comments, 10)
	if len(picked) == 0 {
		return nil
	}
	fmt.Fprintf(b, "THREAD (%d of %d comments):\n", len(picked), len(comments))
	for _, c := range picked {
		fmt.Fprintf(b, "- [%s] %s (%s): %s\n", c.CreatedAt.Format("2006-01-02"), c.Author, c.AuthorAssociation,
			text.TruncateRunes(text.OneLine(pr.CleanBody(c.Body)), cli.CommentRunes))
	}
	return nil
}

// firstN returns at most n leading elements of s.
func firstN(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// printCardHeader is the first line every check's card shares: position, the
// PR, its state, title, and url.
func printCardHeader(p *db.PR, pos, total int) {
	draft := ""
	if p.IsDraft {
		draft = " <gray>(draft)</>"
	}
	cout.Printf("\n  <gray>%d/%d</> <cyan>#%d</> %s<bold>%s</>%s <darkGray>%s</>\n",
		pos, total, p.Number, text.StateTag(p.State),
		text.TruncateRunes(text.OneLine(p.Title), 90), draft, p.URL)
}

// authorLine renders the PR's author, association, age, and size for cards.
func authorLine(p *db.PR) string {
	assocTag, assocName := cli.AssocDisplay(p.AuthorAssociation)
	who := fmt.Sprintf("<%s>%s</>", assocTag, p.Author)
	if assocName != "" {
		who += fmt.Sprintf(" <gray>(%s)</>", assocName)
	}
	return fmt.Sprintf("%s <gray>· +%d/-%d over %d files · 💬 %d · 👍 %d</>",
		who, p.Additions, p.Deletions, p.ChangedFiles, p.CommentCount, p.ThumbsUp)
}
