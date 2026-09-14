package label

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/katbyte/go-kt/cout"
	"github.com/katbyte/prawn/cli"
	"github.com/katbyte/prawn/lib/db"
	"github.com/katbyte/prawn/lib/gh"
	"github.com/katbyte/prawn/lib/pr"
	"github.com/katbyte/prawn/lib/text"
)

// The labels prawn can add, and the evidence classes: linked-issue means an
// issue the PR closes carries the label; title means the PR's title reads
// like one.
const (
	LabelBug         = "bug"
	LabelEnhancement = "enhancement"

	promptLabel = "pr-label"

	classLinkedIssue = "linked-issue"
	classTitle       = "title"
)

var labelClassRank = map[string]int{classLinkedIssue: 1, classTitle: 0}

// article renders a label with its indefinite article: "a bug", "an enhancement".
func article(label string) string {
	if strings.ContainsRune("aeiou", rune(label[0])) {
		return "an " + label
	}
	return "a " + label
}

// title heuristics per label — tuned for recall, the AI supplies precision.
var reTitleByLabel = map[string]*regexp.Regexp{
	LabelBug:         regexp.MustCompile(`(?i)\bfix(es|ed|ing)?\b|\bcrash\b|\bpanic\b|\bregression\b|\bcorrect(s|ly|ed)?\b|\bbroken\b`),
	LabelEnhancement: regexp.MustCompile(`(?i)\bsupport\b|\badd(s|ed|ing)?\b|\bnew resource\b|\bnew data source\b|\bimplement(s|ed|ing)?\b|\bintroduce(s|d)?\b|\ballow(s|ing)?\b|\bexpose(s|d)?\b|\benhance`),
}

// labelFinding is one open PR the evidence says deserves the label.
type labelFinding struct {
	pr     *db.PR
	issues []db.LinkedIssue // linked-issue: the labelled issues
	class  string
}

// Label finds OPEN PRs whose evidence supports the given label they don't
// carry, has the AI judge whether it fits what the change actually does, and
// adds it (add-only — a low score merely leaves the PR untouched).
func (f *Flags) Label(label, link string) error {
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

	col, err := f.collectLabel(d, label, link)
	if err != nil {
		return err
	}
	if col.open == 0 {
		cout.Printf("no fetched PRs — run <cyan>prawn fetch</> first\n")
		return nil
	}
	findings := col.findings

	cout.Printf("\n<bold>%d of %d open PRs read as %s their labels don't record:</>\n", len(findings), col.open, article(label))
	for _, c := range []struct{ class, tag, desc string }{
		{classLinkedIssue, cli.TagGreen, "a linked issue carries the " + label + " label"},
		{classTitle, cli.TagYellow, "the title reads like " + article(label)},
	} {
		if n := col.counts[c.class]; n > 0 {
			cout.Printf("  <%s>%-13s</> <yellow>%d</>  <gray>%s</>\n", c.tag, c.class, n, c.desc)
		}
	}
	cout.Printf("  <gray>%d already labelled bug or enhancement</>\n", col.labelled)
	if len(findings) == 0 {
		return nil
	}

	pass := "label-" + label
	switch {
	case o.ApplyWithAI || o.ApplyWithAIAuto:
		if !f.AI.Enabled {
			return errors.New("--apply-with-ai needs the AI (--ai=false is set)")
		}
		return f.applyLabel(d, label, findings, o, true)
	case o.Apply:
		return f.applyLabel(d, label, findings, o, false)
	}

	// report: score everything (pipelined, cached) and list surest first
	var verdicts map[int]*pr.Verdict
	if f.AI.Enabled {
		promptText, perr := f.labelPrompt(label)
		if perr != nil {
			return perr
		}
		if verdicts, err = f.JudgeBlocks(d, pass, promptText, f.labelJudgeItems(findings), nil, nil); err != nil {
			return err
		}
		slices.SortStableFunc(findings, func(a, b labelFinding) int {
			av, bv := -1.0, -1.0
			if v := verdicts[a.pr.Number]; v != nil {
				av = v.Confidence
			}
			if v := verdicts[b.pr.Number]; v != nil {
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
	} else {
		cout.Printf("<gray>--ai=false: listing without scores</>\n")
	}

	for n := range findings {
		f.printLabelCard(label, &findings[n], n+1, len(findings), verdicts[findings[n].pr.Number])
	}
	cout.Printf("\nnext: <cyan>prawn label %s --apply --dry-run</> to preview, <cyan>--apply-with-ai</> to confirm each, <cyan>--apply-with-ai-auto</> to trust the scores\n", label)
	return nil
}

// labelCollection is everything collectLabel learns in one scan.
type labelCollection struct {
	findings []labelFinding
	counts   map[string]int
	open     int
	labelled int // PRs already carrying bug or enhancement
}

// collectLabel walks every open PR lacking both category labels for evidence
// of this one: a labelled linked issue, or a title reading like one.
func (*Flags) collectLabel(d *db.DB, label, link string) (*labelCollection, error) {
	col := &labelCollection{counts: map[string]int{}}
	prs, err := d.OpenPRs()
	if err != nil {
		return nil, err
	}
	col.open = len(prs)

	cout.Printf("scanning <yellow>%d</> open PRs for missing <lightYellow>%s</> labels...\n", len(prs), label)
	for _, p := range prs {
		// a PR already categorised either way is not this labeller's business
		if p.HasLabel(LabelBug) || p.HasLabel(LabelEnhancement) {
			col.labelled++
			continue
		}

		fdg := labelFinding{pr: p}
		closes, cerr := d.ClosesFor(p.Number)
		if cerr != nil {
			return nil, cerr
		}
		for _, l := range closes {
			if l.HasLabel(label) {
				fdg.issues = append(fdg.issues, l)
			}
		}

		switch {
		case len(fdg.issues) > 0:
			fdg.class = classLinkedIssue
		case reTitleByLabel[label].MatchString(p.Title):
			fdg.class = classTitle
		default:
			continue
		}

		if link != "" && fdg.class != link {
			continue
		}
		col.findings = append(col.findings, fdg)
		col.counts[fdg.class]++
	}

	slices.SortStableFunc(col.findings, func(a, b labelFinding) int {
		if d := labelClassRank[b.class] - labelClassRank[a.class]; d != 0 {
			return d
		}
		return a.pr.Number - b.pr.Number
	})
	return col, nil
}

// applyLabel is both apply modes on the shared harness, worded around
// labelling rather than closing.
func (f *Flags) applyLabel(d *db.DB, label string, findings []labelFinding, o cli.FlagsApplyModes, withAI bool) error {
	byNumber := map[int]*labelFinding{}
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
			return f.labelOne(d, repo, label, byNumber[n], v, pos, total, throttle, interactive)
		})
	p.Noun = "PRs reading as an unlabelled " + label
	p.GateLabel = "fit"
	p.One, p.Verb, p.Done = "label", "labelling", "labelled"
	p.ConfirmAll = fmt.Sprintf("add the <lightYellow>%s</> label to up to <yellow>%d</> PRs in %s?", label, len(findings), f.RepoTag())
	p.ConfirmAI = fmt.Sprintf("add <lightYellow>%s</> where the AI scores ≥ <green>%.2f</> (up to <yellow>%d</> candidates) in %s?", label, p.Threshold, len(findings), f.RepoTag())

	if !withAI {
		return p.ApplyAll(numbers)
	}
	return p.ApplyAI(len(findings), func(onReady func() (bool, error), onBatch func([]pr.Judged) (bool, error)) error {
		promptText, jerr := f.labelPrompt(label)
		if jerr != nil {
			return jerr
		}
		_, jerr = f.JudgeBlocks(d, "label-"+label, promptText, f.labelJudgeItems(findings), onReady, onBatch)
		return jerr
	})
}

// labelOne handles one candidate: card, the live-state guard, and the label
// itself (or a preview under dry-run, or the a/s ask when interactive).
func (f *Flags) labelOne(d *db.DB, repo gh.Repo, label string, fdg *labelFinding, v *pr.Verdict, pos, total int, throttle func(), ask bool) (int, error) {
	f.printLabelCard(label, fdg, pos, total, v)

	if f.DryRun {
		cout.Printf("      <yellow>dry-run: would add the %s label</>\n", label)
		return pr.ApplyPreviewed, nil
	}

	if ask {
		res, perr := pr.Ask(fmt.Sprintf("label <cyan>#%d</> as <lightYellow>%s</>?", fdg.pr.Number, label), "", fdg.pr.URL)
		if perr != nil || res != pr.AskAccept {
			return res, perr
		}
	}

	throttle()
	live, err := repo.GetPull(fdg.pr.Number)
	if err != nil {
		cout.Errorf("      <red>fetching live state: %v</>\n", err)
		return pr.ApplyFailed, nil
	}
	if live.State != cli.RESTStateOpen {
		cout.Printf("      <gray>already closed on github — skipped</>\n")
		return pr.ApplySkipped, nil
	}

	throttle()
	if err := repo.AddLabels(fdg.pr.Number, []string{label}); err != nil {
		cout.Errorf("      <red>labelling failed: %v</>\n", err)
		return pr.ApplyFailed, nil
	}

	cout.Printf("      <fg=28>labelled</> <lightYellow>%s</>\n", label)
	cout.QuietOnlyf("%d@labelled@%s\n", fdg.pr.Number, label)

	a := &db.Action{
		PRNumber: fdg.pr.Number, Action: db.ActionLabel, Reason: label,
		Evidence: map[string]string{"class": fdg.class}, Source: "label-" + label,
		Status: db.StatusApplied, DecidedBy: f.Decider(),
	}
	if len(fdg.issues) > 0 {
		nums := make([]string, 0, len(fdg.issues))
		for _, l := range fdg.issues {
			nums = append(nums, fmt.Sprintf("#%d", l.IssueNumber))
		}
		a.Evidence["issues"] = strings.Join(nums, ", ")
	}
	if v != nil {
		a.Confidence = v.Confidence
		a.Evidence["ai"] = v.Reason
	}
	if err := d.RecordAction(a); err != nil {
		return pr.ApplyFailed, err
	}
	return pr.ApplySet, nil
}

// labelPrompt loads the shared label prompt with the label substituted in.
func (f *Flags) labelPrompt(label string) (string, error) {
	p, err := f.PreparePrompt(promptLabel)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(p, "{{LABEL}}", label), nil
}

// labelJudgeItems renders one judge block per finding: the PR, its class, and
// the labelled linked issues.
func (*Flags) labelJudgeItems(findings []labelFinding) []pr.JudgeItem {
	items := make([]pr.JudgeItem, 0, len(findings))
	for i := range findings {
		fdg := &findings[i]
		var b strings.Builder
		fmt.Fprintf(&b, "### PR #%d: %s\n", fdg.pr.Number, text.OneLine(fdg.pr.Title))
		fmt.Fprintf(&b, "by @%s (%s), opened %s\n", fdg.pr.Author, fdg.pr.AuthorAssociation, fdg.pr.CreatedAt.Format("2006-01-02"))
		fmt.Fprintf(&b, "CLASS: %s\n", strings.ToUpper(fdg.class))
		fmt.Fprintf(&b, "CHANGES: +%d/-%d over %d files: %s\n",
			fdg.pr.Additions, fdg.pr.Deletions, fdg.pr.ChangedFiles, strings.Join(fdg.pr.Files, ", "))
		for _, l := range fdg.issues {
			fmt.Fprintf(&b, "LINKED ISSUE #%d %q — labels: %s\n", l.IssueNumber, text.OneLine(l.Title), strings.Join(l.Labels, ", "))
		}
		fmt.Fprintf(&b, "PR BODY:\n%s\n", text.TruncateRunes(pr.CleanBody(fdg.pr.Body), cli.PRBodyRunes))
		items = append(items, pr.JudgeItem{Number: fdg.pr.Number, Block: b.String()})
	}
	return items
}

// printLabelCard is one candidate: the PR, the evidence for the label, and
// the AI's score when judged.
func (*Flags) printLabelCard(label string, fdg *labelFinding, pos, total int, v *pr.Verdict) {
	p := fdg.pr
	cout.Printf("\n  <gray>%d/%d</> <cyan>#%d</> %s<bold>%s</> <darkGray>%s</>\n",
		pos, total, p.Number, text.StateTag(p.State),
		text.TruncateRunes(text.OneLine(p.Title), 90), p.URL)
	switch fdg.class {
	case classLinkedIssue:
		for _, l := range fdg.issues {
			cout.Printf("      <gray>closes</> <cyan>#%d</> <gray>labelled</> <lightYellow>%s</> <gray>—</> %s\n",
				l.IssueNumber, label, text.TruncateRunes(text.OneLine(l.Title), 70))
		}
	case classTitle:
		cout.Printf("      <gray>the title reads like a</> <lightYellow>%s</>\n", label)
	}
	cli.PrintVerdict(v)
}
