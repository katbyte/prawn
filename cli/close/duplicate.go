package close

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"github.com/katbyte/go-kt/cout"
	"github.com/katbyte/prawn/assets"
	"github.com/katbyte/prawn/cli"
	"github.com/katbyte/prawn/lib/db"
	"github.com/katbyte/prawn/lib/gh"
	"github.com/katbyte/prawn/lib/pr"
	"github.com/katbyte/prawn/lib/text"
)

// Classes by how the pair was found: same-issue means both PRs' closing
// keywords reference the same issue; similar means near-identical titles over
// overlapping changed files where nobody linked the pair.
const (
	passDuplicate          = "duplicate"
	promptDuplicate        = "pr-duplicate-close"
	templateDuplicateClose = "duplicate-close"
	reasonDuplicate        = "duplicate"

	classSameIssue = "same-issue"
	classSimilar   = "similar"

	// similarMinTitleRunes keeps trivial titles ("fix typo") from pairing PRs
	// that merely share a phrasing; below it the files must overlap anyway.
	similarMinTitleRunes = 25
)

var duplicateClassRank = map[string]int{classSameIssue: 1, classSimilar: 0}

// duplicateFinding is one open PR another open PR covers: the survivor keeps
// the review, this one closes towards it.
type duplicateFinding struct {
	pr       *db.PR
	target   *db.PR // the surviving PR the close points at
	viaIssue int    // same-issue: the shared closing issue (0 for similar)
	class    string
}

// Duplicate finds OPEN PRs that another open PR duplicates — the same change
// heading for the same review. Pairs come from shared closing issues and from
// near-identical titles over overlapping files; the survivor is the PR
// carrying more of the review (approved first, then review and comment
// weight, then the older on a tie). The AI compares the substance of both
// changes — same files with different fixes score low — before blessing a
// close towards the survivor.
func (f *Flags) Duplicate(link string) error {
	o := f.Modes
	if !f.NoAutoFetch {
		if err := f.AutoFetch(); err != nil {
			return err
		}
	}

	d, err := f.OpenDB()
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()

	col, err := f.collectDuplicate(d, link)
	if err != nil {
		return err
	}
	if col.open == 0 {
		cout.Printf("no fetched PRs — run <cyan>prawn fetch</> first\n")
		return nil
	}
	findings := col.findings

	cout.Printf("\n<bold>%d of %d open PRs appear to duplicate another open PR:</>\n", len(findings), col.open)
	for _, c := range []struct{ class, tag, desc string }{
		{classSameIssue, cli.TagGreen, "both PRs close the same issue"},
		{classSimilar, cli.TagYellow, "near-identical titles over overlapping files, nobody linked them"},
	} {
		if n := col.counts[c.class]; n > 0 {
			cout.Printf("  <%s>%-11s</> <yellow>%d</>  <gray>%s</>\n", c.tag, c.class, n, c.desc)
		}
	}
	cout.Printf("  <gray>%s</>\n", keepSummary(col.protected))
	if len(findings) == 0 {
		return nil
	}

	switch {
	case o.ApplyWithAI || o.ApplyWithAIAuto:
		if !f.AI.Enabled {
			return errors.New("--apply-with-ai needs the AI (--ai=false is set)")
		}
		return f.applyDuplicate(d, findings, o, true)
	case o.Apply:
		return f.applyDuplicate(d, findings, o, false)
	}

	verdicts, err := f.reportVerdicts(d, passDuplicate, promptDuplicate, func() ([]pr.JudgeItem, error) {
		return f.duplicateJudgeItems(d, findings)
	})
	if err != nil {
		return err
	}
	sortByConfidence(findings, verdicts, func(fdg *duplicateFinding) int { return fdg.pr.Number })

	for n := range findings {
		f.printDuplicateCard(&findings[n], n+1, len(findings), verdicts[findings[n].pr.Number])
	}
	cout.Printf("\nnext: <cyan>prawn close duplicate --apply --dry-run</> to preview the closes, <cyan>--apply-with-ai</> to confirm each, <cyan>--apply-with-ai-auto</> to trust the scores\n")
	return nil
}

// duplicateCollection is everything collectDuplicate learns in one scan.
type duplicateCollection struct {
	findings  []duplicateFinding
	counts    map[string]int
	open      int
	protected map[string]int
}

// collectDuplicate pairs open PRs by shared closing issue, then by normalised
// title + overlapping files, and aims each pair's close at the weaker side.
func (f *Flags) collectDuplicate(d *db.DB, link string) (*duplicateCollection, error) {
	col := &duplicateCollection{counts: map[string]int{}, protected: map[string]int{}}
	prs, err := d.OpenPRs()
	if err != nil {
		return nil, err
	}
	col.open = len(prs)
	byNumber := map[int]*db.PR{}
	for _, p := range prs {
		byNumber[p.Number] = p
	}

	closes, err := d.AllCloses()
	if err != nil {
		return nil, err
	}

	cout.Printf("scanning <yellow>%d</> open PRs for duplicate pairs...\n", len(prs))

	// same-issue: group open PRs by the issues they close
	byIssue := map[int][]*db.PR{}
	for _, p := range prs {
		for _, l := range closes[p.Number] {
			byIssue[l.IssueNumber] = append(byIssue[l.IssueNumber], p)
		}
	}
	paired := map[int]bool{} // PRs already listed as a finding
	record := func(p, target *db.PR, class string, viaIssue int) {
		switch {
		case paired[p.Number]:
			return
		case p.ReviewDecision == db.DecisionApproved:
			col.protected["approved"]++
			return
		case p.ThumbsUp >= f.KeepReactions:
			col.protected["high-engagement"]++
			return
		}
		paired[p.Number] = true
		if link != "" && class != link {
			return
		}
		col.findings = append(col.findings, duplicateFinding{pr: p, target: target, viaIssue: viaIssue, class: class})
		col.counts[class]++
	}

	for _, issue := range text.SortedKeys(byIssue) {
		group := byIssue[issue]
		if len(group) < 2 {
			continue
		}
		survivor := pickSurvivor(group)
		for _, p := range group {
			if p.Number != survivor.Number {
				record(p, survivor, classSameIssue, issue)
			}
		}
	}

	// similar: normalised-title groups among PRs not already paired
	byTitle := map[string][]*db.PR{}
	for _, p := range prs {
		if key := normaliseTitle(p.Title); key != "" {
			byTitle[key] = append(byTitle[key], p)
		}
	}
	for _, key := range text.SortedKeys(byTitle) {
		group := byTitle[key]
		if len(group) < 2 {
			continue
		}
		survivor := pickSurvivor(group)
		for _, p := range group {
			if p.Number == survivor.Number || paired[p.Number] {
				continue
			}
			// a shared trivial title proves nothing on its own — the changes
			// must actually overlap unless the title is distinctive
			if !filesOverlap(p, survivor) && len([]rune(key)) < similarMinTitleRunes {
				continue
			}
			record(p, survivor, classSimilar, 0)
		}
	}

	slices.SortStableFunc(col.findings, func(a, b duplicateFinding) int {
		if d := duplicateClassRank[b.class] - duplicateClassRank[a.class]; d != 0 {
			return d
		}
		return a.pr.Number - b.pr.Number
	})
	return col, nil
}

// pickSurvivor picks the PR a duplicate group's review should live on:
// approved first, then the most review and comment weight, then the older.
func pickSurvivor(group []*db.PR) *db.PR {
	best := group[0]
	for _, p := range group[1:] {
		if survivorLess(best, p) {
			best = p
		}
	}
	return best
}

// survivorLess reports whether b makes a better survivor than a.
func survivorLess(a, b *db.PR) bool {
	aApproved, bApproved := a.ReviewDecision == db.DecisionApproved, b.ReviewDecision == db.DecisionApproved
	if aApproved != bApproved {
		return bApproved
	}
	if aDraft, bDraft := a.IsDraft, b.IsDraft; aDraft != bDraft {
		return aDraft // a draft loses to a ready PR
	}
	aw, bw := reviewWeight(a), reviewWeight(b)
	if aw != bw {
		return bw > aw
	}
	return b.Number < a.Number // older wins the tie
}

// reviewWeight is how much review discussion a PR carries.
func reviewWeight(p *db.PR) int {
	return p.CommentCount + 2*p.ThumbsUp
}

// reTitleNoise strips punctuation and collapses whitespace for title matching.
var reTitleNoise = regexp.MustCompile(`[^a-z0-9 ]+`)

// normaliseTitle folds a title for near-identical matching: lowercase, no
// punctuation, single spaces. Too-short results ("wip") return "".
func normaliseTitle(title string) string {
	t := text.LowerASCII(title)
	t = reTitleNoise.ReplaceAllString(t, " ")
	t = text.OneLine(t)
	if len([]rune(t)) < 10 {
		return ""
	}
	return t
}

// filesOverlap reports whether two PRs touch any common path.
func filesOverlap(a, b *db.PR) bool {
	if len(a.Files) == 0 || len(b.Files) == 0 {
		return false
	}
	seen := make(map[string]bool, len(a.Files))
	for _, p := range a.Files {
		seen[p] = true
	}
	return slices.ContainsFunc(b.Files, func(p string) bool { return seen[p] })
}

// applyDuplicate is both apply modes on the shared harness.
func (f *Flags) applyDuplicate(d *db.DB, findings []duplicateFinding, o cli.FlagsApplyModes, withAI bool) error {
	byNumber := map[int]*duplicateFinding{}
	numbers := make([]int, len(findings))
	for i := range findings {
		byNumber[findings[i].pr.Number] = &findings[i]
		numbers[i] = findings[i].pr.Number
	}

	repo, err := f.NewRepo()
	if err != nil {
		return err
	}
	throttle := cli.NewThrottle()

	p := f.NewApplyPass(o,
		func(n int) string { return byNumber[n].pr.Title },
		func(n int, v *pr.Verdict, pos, total int, interactive bool) (int, error) {
			return f.closeOneDuplicate(d, repo, byNumber[n], v, pos, total, throttle, interactive)
		})
	p.Noun = "PRs duplicating another open PR"
	p.GateLabel = "match"
	p.ConfirmAll = fmt.Sprintf("comment and close up to <yellow>%d</> duplicate PRs in %s?", len(findings), f.RepoTag())
	p.ConfirmAI = fmt.Sprintf("comment and close duplicates the AI scores ≥ <green>%.2f</> (up to <yellow>%d</> candidates) in %s?", p.Threshold, len(findings), f.RepoTag())

	if !withAI {
		return p.ApplyAll(numbers)
	}
	return p.ApplyAI(len(findings), func(onReady func() (bool, error), onBatch func([]pr.Judged) (bool, error)) error {
		items, jerr := f.duplicateJudgeItems(d, findings)
		if jerr != nil {
			return jerr
		}
		promptText, jerr := f.PreparePrompt(promptDuplicate)
		if jerr != nil {
			return jerr
		}
		_, jerr = f.JudgeBlocks(d, passDuplicate, promptText, items, onReady, onBatch)
		return jerr
	})
}

// closeOneDuplicate handles one candidate: card, the duplicate-close comment
// pointing at the survivor, and the close.
func (f *Flags) closeOneDuplicate(d *db.DB, repo gh.Repo, fdg *duplicateFinding, v *pr.Verdict, pos, total int, throttle func(), ask bool) (int, error) {
	f.printDuplicateCard(fdg, pos, total, v)

	comment, err := f.renderDuplicateComment(fdg)
	if err != nil {
		return pr.ApplyFailed, err
	}

	evidence := map[string]string{"target": fmt.Sprintf("#%d", fdg.target.Number)}
	if fdg.viaIssue > 0 {
		evidence["via-issue"] = fmt.Sprintf("#%d", fdg.viaIssue)
	}

	return f.doClose(d, repo, closeReq{
		p: fdg.pr, class: fdg.class, reason: reasonDuplicate, template: templateDuplicateClose,
		comment: comment, evidence: evidence, source: passDuplicate,
		question: fmt.Sprintf("close <cyan>#%d</> towards <cyan>#%d</>?", fdg.pr.Number, fdg.target.Number),
	}, v, throttle, ask)
}

// renderDuplicateComment renders the close comment pointing at the survivor.
func (f *Flags) renderDuplicateComment(fdg *duplicateFinding) (string, error) {
	tt, err := assets.CommentTemplate(templateDuplicateClose)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New(templateDuplicateClose).Parse(tt)
	if err != nil {
		return "", fmt.Errorf("parsing template %s: %w", templateDuplicateClose, err)
	}
	data := struct {
		Target      int
		TargetTitle string
	}{fdg.target.Number, text.OneLine(fdg.target.Title)}
	var b strings.Builder
	if err := tmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("rendering template %s: %w", templateDuplicateClose, err)
	}
	return strings.TrimSpace(b.String()), nil
}

// duplicateJudgeItems renders one judge block per finding: the candidate, the
// survivor, how they were paired, and the candidate's thread digest.
func (f *Flags) duplicateJudgeItems(d *db.DB, findings []duplicateFinding) ([]pr.JudgeItem, error) {
	items := make([]pr.JudgeItem, 0, len(findings))
	for i := range findings {
		fdg := &findings[i]
		var b strings.Builder
		writePRBlockHeader(&b, fdg.pr, fdg.class)

		if fdg.viaIssue > 0 {
			fmt.Fprintf(&b, "PAIRED VIA: both close issue #%d\n", fdg.viaIssue)
		} else {
			fmt.Fprintf(&b, "PAIRED VIA: near-identical titles\n")
		}
		t := fdg.target
		fmt.Fprintf(&b, "THE SURVIVOR — PR #%d: %s\n", t.Number, text.OneLine(t.Title))
		fmt.Fprintf(&b, "by @%s (%s), opened %s, review state: %s, +%d/-%d over %d files: %s\n",
			t.Author, t.AuthorAssociation, t.CreatedAt.Format("2006-01-02"),
			text.OrDefault(t.ReviewDecision, "none"), t.Additions, t.Deletions, t.ChangedFiles,
			strings.Join(firstN(t.Files, 20), ", "))
		fmt.Fprintf(&b, "SURVIVOR BODY:\n%s\n", text.TruncateRunes(pr.CleanBody(t.Body), 1500))

		if err := writeThreadDigest(&b, d, fdg.pr.Number); err != nil {
			return nil, err
		}
		items = append(items, pr.JudgeItem{Number: fdg.pr.Number, Block: b.String()})
	}
	return items, nil
}

// printDuplicateCard is one candidate: the PR, the survivor it would close
// towards, and the AI's score when judged.
func (f *Flags) printDuplicateCard(fdg *duplicateFinding, pos, total int, v *pr.Verdict) {
	printCardHeader(fdg.pr, pos, total)
	cout.Printf("      %s\n", authorLine(fdg.pr))
	via := "<gray>(near-identical titles)</>"
	if fdg.viaIssue > 0 {
		via = fmt.Sprintf("<gray>(both close</> <cyan>#%d</><gray>)</>", fdg.viaIssue)
	}
	cout.Printf("      <gray>duplicates</> <cyan>#%d</> %s <gray>· survivor has 💬 %d · review state %s</>\n",
		fdg.target.Number, via, fdg.target.CommentCount, text.OrDefault(fdg.target.ReviewDecision, "none"))
	cout.Printf("      <gray>“</>%s<gray>”</> <darkGray>%s</>\n",
		text.TruncateRunes(text.OneLine(fdg.target.Title), 90), fdg.target.URL)
	cli.PrintVerdict(v)
}
