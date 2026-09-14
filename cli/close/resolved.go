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

// Classes by the evidence source, strongest first: landed means a merged
// commit in the provider's history covers the PR's files (somebody else made
// the change); issue-closed means every issue the PR's closing keywords
// reference has since been closed; superseded means a maintainer's comment
// says the change was covered elsewhere.
const (
	passResolved          = "resolved"
	promptResolved        = "pr-resolved-close"
	templateResolvedClose = "resolved-close"

	reasonLanded      = "landed"
	reasonIssueClosed = "issues-closed"
	reasonSuperseded  = "superseded"

	classLanded      = "landed"
	classIssueClosed = "issue-closed"
	classSuperseded  = "superseded"

	// resolved is the one check whose judge blocks are deliberately
	// exhaustive: whether a PR is still needed hinges on details anywhere in
	// it — a side fix in the body's last paragraph, a "still broken" reply
	// deep in the thread — so everything prawn holds goes in, and the batch
	// shrinks to buy each candidate the room.
	resolvedJudgeBatch  = 4
	resolvedBodyRunes   = 8000
	resolvedTailRunes   = 1500 // per comment/review body
	resolvedTargetRunes = 4000 // a linked issue's body, when known
)

var resolvedClassRank = map[string]int{classLanded: 2, classIssueClosed: 1, classSuperseded: 0}

// reSuperseded spots a comment claiming the change was covered elsewhere.
var reSuperseded = regexp.MustCompile(`(?i)\bsupersed|\breplaced by\b|\bin favou?r of\b|\bhandled in\b|\bcontinued in\b|\bcovered by\b|\balready (been )?(fixed|implemented|added|merged)\b`)

// resolvedFinding is one open PR whose purpose appears already served: the
// issues it set out to close are closed, or the thread says the change went
// in some other way.
type resolvedFinding struct {
	pr     *db.PR
	landed *pr.Landing      // landed: the PR's new identifiers are in the provider, arrived after it opened
	closes []db.LinkedIssue // issue-closed: the linked issues, all closed (also kept alongside landed)
	claim  *db.Comment      // superseded: the maintainer's claim
	class  string
}

// Resolved finds OPEN PRs whose change has already been dealt with: every
// issue they say they fix has since been closed, or a maintainer's comment
// says the change was covered elsewhere. The AI reads the ENTIRE candidate —
// full body, every file, every review, the whole thread — because a PR doing
// more than its linked issues cover must never robo-close.
func (f *Flags) Resolved(link string) error {
	o := f.Modes
	if link == classLanded && f.Cmd.SrcDir == "" {
		return errors.New("the landed class scans the provider's git history: set --src-dir or PRAWN_SRC_DIR")
	}
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

	col, err := f.collectResolved(d, link)
	if err != nil {
		return err
	}
	if col.open == 0 {
		cout.Printf("no fetched PRs — run <cyan>prawn fetch</> first\n")
		return nil
	}
	findings := col.findings

	cout.Printf("\n<bold>%d of %d open PRs appear to already be dealt with:</>\n", len(findings), col.open)
	for _, c := range []struct{ class, tag, desc string }{
		{classLanded, cli.TagGreen, "a merged commit since covers the files they change"},
		{classIssueClosed, cli.TagYellow, "every issue they fix has since been closed"},
		{classSuperseded, cli.TagOrange, "the thread says the change was covered elsewhere"},
	} {
		if n := col.counts[c.class]; n > 0 {
			cout.Printf("  <%s>%-13s</> <yellow>%d</>  <gray>%s</>\n", c.tag, c.class, n, c.desc)
		}
	}
	cout.Printf("  <gray>%s</>\n", keepSummary(col.protected))
	switch {
	case col.history == nil:
		cout.Printf("  <yellow>landed class skipped</> <gray>— set --src-dir or PRAWN_SRC_DIR to check each PR's additions against the provider source</>\n")
	case col.noDiff > 0:
		cout.Printf("  <gray>landed: %d PRs have no diff held (run prawn fetch, or github will not render it)</>\n", col.noDiff)
	}
	if len(findings) == 0 {
		return nil
	}

	switch {
	case o.ApplyWithAI || o.ApplyWithAIAuto:
		if !f.AI.Enabled {
			return errors.New("--apply-with-ai needs the AI (--ai=false is set)")
		}
		return f.applyResolved(d, findings, o, true)
	case o.Apply:
		return f.applyResolved(d, findings, o, false)
	}

	// report: score everything (pipelined, cached) and list surest first
	var verdicts map[int]*pr.Verdict
	if f.AI.Enabled {
		items, jerr := f.resolvedJudgeItems(d, findings)
		if jerr != nil {
			return jerr
		}
		promptText, jerr := f.PreparePrompt(promptResolved)
		if jerr != nil {
			return jerr
		}
		if verdicts, err = f.JudgeBlocksBatch(d, passResolved, promptText, resolvedJudgeBatch, items, nil, nil); err != nil {
			return err
		}
	} else {
		cout.Printf("<gray>--ai=false: listing without scores</>\n")
	}
	sortByConfidence(findings, verdicts, func(fdg *resolvedFinding) int { return fdg.pr.Number })

	for n := range findings {
		f.printResolvedCard(&findings[n], n+1, len(findings), verdicts[findings[n].pr.Number])
	}
	cout.Printf("\nnext: <cyan>prawn close resolved --apply --dry-run</> to preview the closes, <cyan>--apply-with-ai</> to confirm each, <cyan>--apply-with-ai-auto</> to trust the scores\n")
	return nil
}

// resolvedCollection is everything collectResolved learns in one scan.
type resolvedCollection struct {
	findings  []resolvedFinding
	counts    map[string]int
	open      int // open PRs in the db
	protected map[string]int
	history   *pr.History // nil when no --src-dir: the landed class was skipped
	noDiff    int         // PRs the landed class could not see: no diff held
}

// collectResolved walks every open PR looking for served purposes: all linked
// issues closed, or a maintainer's superseded claim in the thread.
func (f *Flags) collectResolved(d *db.DB, link string) (*resolvedCollection, error) {
	col := &resolvedCollection{counts: map[string]int{}, protected: map[string]int{}}
	prs, err := d.OpenPRs()
	if err != nil {
		return nil, err
	}
	col.open = len(prs)

	// the provider checkout, for the landed class: each PR's diff names what
	// it adds, and the checkout says whether that is already there
	var diffs map[int]string
	if f.Cmd.SrcDir != "" {
		hist, herr := pr.LoadHistory(f.Cmd.SrcDir)
		if herr != nil {
			return nil, herr
		}
		col.history = hist
		if diffs, err = d.AllDiffs(); err != nil {
			return nil, err
		}
		cout.Printf("checking each PR's additions against the provider source at <cyan>%s</>...\n", f.Cmd.SrcDir)
	}

	cout.Printf("scanning <yellow>%d</> open PRs for changes already dealt with...\n", len(prs))
	for _, p := range prs {
		switch {
		case p.ReviewDecision == db.DecisionApproved:
			col.protected["approved"]++
			continue
		case p.ThumbsUp >= f.KeepReactions:
			col.protected["high-engagement"]++
			continue
		}

		fdg := resolvedFinding{pr: p}
		closes, cerr := d.ClosesFor(p.Number)
		if cerr != nil {
			return nil, cerr
		}
		allClosed := len(closes) > 0
		for _, l := range closes {
			if l.State != db.PRClosed { // issue states share the CLOSED spelling
				allClosed = false
				break
			}
		}
		if allClosed {
			fdg.closes = closes
		}
		if col.history != nil {
			if raw := diffs[p.Number]; raw == "" {
				col.noDiff++
			} else if fdg.landed, err = col.history.Landing(p, pr.ParseDiff(raw)); err != nil {
				return nil, err
			}
		}

		switch {
		case fdg.landed != nil:
			fdg.class = classLanded
		case allClosed:
			fdg.class = classIssueClosed
		default:
			comments, err := d.CommentsFor(p.Number)
			if err != nil {
				return nil, err
			}
			for ci := len(comments) - 1; ci >= 0; ci-- {
				c := comments[ci]
				if c.IsMaintainer() && !pr.Bot(c.Author) && reSuperseded.MatchString(c.Body) {
					fdg.class, fdg.claim = classSuperseded, &comments[ci]
					break
				}
			}
			if fdg.claim == nil {
				continue
			}
		}

		if link != "" && fdg.class != link {
			continue
		}
		col.findings = append(col.findings, fdg)
		col.counts[fdg.class]++
	}

	slices.SortStableFunc(col.findings, func(a, b resolvedFinding) int {
		if d := resolvedClassRank[b.class] - resolvedClassRank[a.class]; d != 0 {
			return d
		}
		return a.pr.Number - b.pr.Number
	})
	return col, nil
}

// applyResolved is both apply modes on the shared harness: plain --apply
// closes everything listed; --apply-with-ai[-auto] gates each close on the
// judge reading the whole candidate, and is the recommended path — a PR that
// does more than its linked issues cover must never robo-close.
func (f *Flags) applyResolved(d *db.DB, findings []resolvedFinding, o cli.FlagsApplyModes, withAI bool) error {
	byNumber := map[int]*resolvedFinding{}
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
			return f.closeOneResolved(d, repo, byNumber[n], v, pos, total, throttle, interactive)
		})
	p.Noun = "PRs whose change is already dealt with"
	p.GateLabel = "resolved"
	p.ConfirmAll = fmt.Sprintf("comment and close up to <yellow>%d</> PRs in %s?", len(findings), f.RepoTag())
	p.ConfirmAI = fmt.Sprintf("comment and close PRs the AI scores ≥ <green>%.2f</> (up to <yellow>%d</> candidates) in %s?", p.Threshold, len(findings), f.RepoTag())

	if !withAI {
		return p.ApplyAll(numbers)
	}
	return p.ApplyAI(len(findings), func(onReady func() (bool, error), onBatch func([]pr.Judged) (bool, error)) error {
		items, jerr := f.resolvedJudgeItems(d, findings)
		if jerr != nil {
			return jerr
		}
		promptText, jerr := f.PreparePrompt(promptResolved)
		if jerr != nil {
			return jerr
		}
		_, jerr = f.JudgeBlocksBatch(d, passResolved, promptText, resolvedJudgeBatch, items, onReady, onBatch)
		return jerr
	})
}

// closeOneResolved handles one candidate: card, the resolved-close comment
// citing the evidence, and the close (or a preview under dry-run, or the a/s
// ask when interactive).
func (f *Flags) closeOneResolved(d *db.DB, repo gh.Repo, fdg *resolvedFinding, v *pr.Verdict, pos, total int, throttle func(), ask bool) (int, error) {
	f.printResolvedCard(fdg, pos, total, v)

	comment, err := f.renderResolvedComment(fdg)
	if err != nil {
		return pr.ApplyFailed, err
	}

	reason, evidence := reasonIssueClosed, map[string]string{}
	switch fdg.class {
	case classLanded:
		reason = reasonLanded
		l := fdg.landed
		evidence["landed-in"] = f.landingRef(l)
		evidence["commit"], evidence["shipped"] = l.Commit.Hash[:10], text.OrDefault(l.ShippedIn, "unreleased")
		for k, v := range map[string][]string{"landed": l.Landed, "missing": l.Missing, "gone": l.Gone, "still": l.Still} {
			if len(v) > 0 {
				evidence[k] = strings.Join(v, " ")
			}
		}
		if len(fdg.closes) > 0 {
			evidence["issues"] = issueList(fdg.closes)
		}
	case classSuperseded:
		reason = reasonSuperseded
		evidence["claim"] = text.TruncateRunes(text.OneLine(pr.CleanBody(fdg.claim.Body)), 120)
		evidence["author"], evidence["comment-url"] = fdg.claim.Author, fdg.claim.URL
	default:
		evidence["issues"] = issueList(fdg.closes)
	}

	return f.doClose(d, repo, closeReq{
		p: fdg.pr, class: fdg.class, reason: reason, template: templateResolvedClose,
		comment: comment, evidence: evidence, source: passResolved,
		question: fmt.Sprintf("close <cyan>#%d</> as already dealt with?", fdg.pr.Number),
	}, v, throttle, ask)
}

// renderResolvedComment renders the close comment citing the closed issues or
// the superseding claim.
func (f *Flags) renderResolvedComment(fdg *resolvedFinding) (string, error) {
	tt, err := assets.CommentTemplate(templateResolvedClose)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New(templateResolvedClose).Parse(tt)
	if err != nil {
		return "", fmt.Errorf("parsing template %s: %w", templateResolvedClose, err)
	}
	data := struct {
		Landed     bool
		LandedRef  string // "#32765" or a short commit hash
		LandedURL  string
		Shipped    string // release tag, "" when not yet released
		Superseded bool
		URL        string
		Issues     string
		Plural     bool
	}{Landed: fdg.class == classLanded, Superseded: fdg.class == classSuperseded, Issues: issueList(fdg.closes), Plural: len(fdg.closes) > 1}
	if fdg.claim != nil {
		data.URL = fdg.claim.URL
	}
	if data.Landed {
		l := fdg.landed
		data.LandedRef, data.LandedURL, data.Shipped = f.landingRef(l), f.landingURL(l), l.ShippedIn
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("rendering template %s: %w", templateResolvedClose, err)
	}
	return strings.TrimSpace(b.String()), nil
}

// issueList renders linked issues as "#12 and #34" for comments and evidence.
func issueList(closes []db.LinkedIssue) string {
	parts := make([]string, 0, len(closes))
	for _, l := range closes {
		parts = append(parts, fmt.Sprintf("#%d", l.IssueNumber))
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
}

// landingRef names a landing for comments and evidence: the merged PR when
// the squash subject carried one, else the short commit hash.
func (f *Flags) landingRef(l *pr.Landing) string {
	if l.Commit.PR > 0 {
		return fmt.Sprintf("#%d", l.Commit.PR)
	}
	return l.Commit.Hash[:10]
}

// landingURL links a landing: the PR page, or the commit page without one.
func (f *Flags) landingURL(l *pr.Landing) string {
	if l.Commit.PR > 0 {
		return f.PRURL(l.Commit.PR)
	}
	return fmt.Sprintf("https://github.com/%s/commit/%s", f.GH.Repo, l.Commit.Hash)
}

// resolvedJudgeItems renders one judge block per finding, exhaustively:
// everything prawn holds on the PR goes in — the full body, every changed
// file, every label, every review, every human comment (not a digest), and
// each linked issue in full — because "is this still needed" hinges on
// details a digest would drop.
func (f *Flags) resolvedJudgeItems(d *db.DB, findings []resolvedFinding) ([]pr.JudgeItem, error) {
	items := make([]pr.JudgeItem, 0, len(findings))
	for i := range findings {
		fdg := &findings[i]
		p := fdg.pr
		var b strings.Builder

		fmt.Fprintf(&b, "### PR #%d: %s\n", p.Number, text.OneLine(p.Title))
		draft := ""
		if p.IsDraft {
			draft = ", DRAFT"
		}
		fmt.Fprintf(&b, "by @%s (%s)%s, opened %s, last activity %s, last commit %s\n",
			p.Author, p.AuthorAssociation, draft,
			p.CreatedAt.Format("2006-01-02"), p.UpdatedAt.Format("2006-01-02"), p.LastCommitAt.Format("2006-01-02"))
		fmt.Fprintf(&b, "CLASS: %s\n", strings.ToUpper(fdg.class))
		fmt.Fprintf(&b, "STATE: review decision %s · mergeable %s · labels: %s · 👍 %d\n",
			text.OrDefault(p.ReviewDecision, "none"), text.OrDefault(p.Mergeable, "unknown"),
			text.OrDefault(strings.Join(p.Labels, ", "), "none"), p.ThumbsUp)
		fmt.Fprintf(&b, "CHANGES: +%d/-%d over %d files:\n", p.Additions, p.Deletions, p.ChangedFiles)
		for _, path := range p.Files {
			fmt.Fprintf(&b, "- %s\n", path)
		}
		fmt.Fprintf(&b, "PR BODY:\n%s\n", text.TruncateRunes(pr.CleanBody(p.Body), resolvedBodyRunes))

		if l := fdg.landed; l != nil {
			fmt.Fprintf(&b, "EVIDENCE — the provider source already reflects this PR's change (it did not when the PR was opened):\n")
			if len(l.Landed) > 0 {
				fmt.Fprintf(&b, "- identifiers the PR's diff introduces that the source now holds: %s\n", strings.Join(l.Landed, ", "))
			}
			if len(l.Missing) > 0 {
				fmt.Fprintf(&b, "- identifiers the PR's diff introduces that the source does NOT hold: %s\n", strings.Join(l.Missing, ", "))
			}
			if len(l.Gone) > 0 {
				fmt.Fprintf(&b, "- identifiers the PR's diff deletes that are now gone from the source: %s\n", strings.Join(l.Gone, ", "))
			}
			if len(l.Still) > 0 {
				fmt.Fprintf(&b, "- identifiers the PR's diff deletes that the source STILL holds: %s\n", strings.Join(l.Still, ", "))
			}
			if len(l.Missing) > 0 && len(l.Gone) > 0 {
				fmt.Fprintf(&b, "  (deleted names gone but the PR's new names absent usually means the same change landed under different names — a rename done differently)\n")
			}
			fmt.Fprintf(&b, "- introduced by commit %s on %s by %s: %q (%s, shipped in %s)\n",
				l.Commit.Hash[:10], l.Commit.Date.Format("2006-01-02"), l.Commit.Author, text.OneLine(l.Commit.Subject),
				f.landingRef(l), text.OrDefault(l.ShippedIn, "no release yet"))
		}
		switch {
		case len(fdg.closes) > 0:
			fmt.Fprintf(&b, "EVIDENCE — every linked issue is closed:\n")
			for _, l := range fdg.closes {
				fmt.Fprintf(&b, "- #%d %q closed as %s (labels: %s)\n",
					l.IssueNumber, text.OneLine(l.Title), text.OrDefault(l.StateReason, "unknown"), strings.Join(l.Labels, ", "))
			}
		case fdg.class == classSuperseded:
			fmt.Fprintf(&b, "EVIDENCE — the superseding claim, by %s (%s) on %s:\n%s\n",
				fdg.claim.Author, fdg.claim.AuthorAssociation, fdg.claim.CreatedAt.Format("2006-01-02"),
				text.TruncateRunes(pr.CleanBody(fdg.claim.Body), resolvedTargetRunes))
		}

		reviews, rerr := d.ReviewsFor(p.Number)
		if rerr != nil {
			return nil, rerr
		}
		if len(reviews) > 0 {
			fmt.Fprintf(&b, "REVIEWS (all %d):\n", len(reviews))
			for _, r := range reviews {
				body := text.TruncateRunes(text.OneLine(pr.CleanBody(r.Body)), resolvedTailRunes)
				fmt.Fprintf(&b, "- [%s] %s (%s) %s: %s\n", r.SubmittedAt.Format("2006-01-02"),
					r.Author, r.AuthorAssociation, r.State, text.OrDefault(body, "(no comment)"))
			}
		}

		comments, cerr := d.CommentsFor(p.Number)
		if cerr != nil {
			return nil, cerr
		}
		human, bots := 0, 0
		for _, c := range comments {
			if pr.Bot(c.Author) {
				bots++
			} else {
				human++
			}
		}
		if human > 0 {
			fmt.Fprintf(&b, "THREAD (all %d human comments", human)
			if bots > 0 {
				fmt.Fprintf(&b, "; %d bot comments omitted", bots)
			}
			fmt.Fprintf(&b, "):\n")
			for _, c := range comments {
				if pr.Bot(c.Author) {
					continue
				}
				fmt.Fprintf(&b, "- [%s] %s (%s): %s\n", c.CreatedAt.Format("2006-01-02"), c.Author, c.AuthorAssociation,
					text.TruncateRunes(text.OneLine(pr.CleanBody(c.Body)), resolvedTailRunes))
			}
		}

		items = append(items, pr.JudgeItem{Number: p.Number, Block: b.String()})
	}
	return items, nil
}

// printResolvedCard is one candidate: the PR, its evidence, and the AI's
// score when judged.
func (f *Flags) printResolvedCard(fdg *resolvedFinding, pos, total int, v *pr.Verdict) {
	printCardHeader(fdg.pr, pos, total)
	cout.Printf("      %s\n", authorLine(fdg.pr))
	if l := fdg.landed; l != nil {
		cout.Printf("      <gray>landed in</> <cyan>%s</> <gray>on %s by %s · shipped in</> <lightMagenta>%s</> <darkGray>%s</>\n",
			f.landingRef(l), l.Commit.Date.Format("2006-01-02"), l.Commit.Author, text.OrDefault(l.ShippedIn, "unreleased"), f.landingURL(l))
		cout.Printf("      <gray>“</>%s<gray>”</>\n", text.TruncateRunes(text.OneLine(l.Commit.Subject), 120))
		var parts []string
		if len(l.Landed) > 0 {
			parts = append(parts, "<gray>now in the source:</> <green>"+strings.Join(l.Landed, " ")+"</>")
		}
		if len(l.Missing) > 0 {
			parts = append(parts, "<gray>not found:</> <red>"+strings.Join(l.Missing, " ")+"</>")
		}
		if len(l.Gone) > 0 {
			parts = append(parts, "<gray>gone as the PR removes:</> <green>"+strings.Join(l.Gone, " ")+"</>")
		}
		if len(l.Still) > 0 {
			parts = append(parts, "<gray>still there:</> <red>"+strings.Join(l.Still, " ")+"</>")
		}
		cout.Printf("      %s\n", strings.Join(parts, " <gray>·</> "))
	}
	switch fdg.class {
	case classLanded, classIssueClosed:
		for _, l := range fdg.closes {
			cout.Printf("      <gray>fixes</> <cyan>#%d</> %s <gray>closed as</> <lightMagenta>%s</> <gray>—</> %s\n",
				l.IssueNumber, text.StateTag(l.State), text.OrDefault(l.StateReason, "unknown"),
				text.TruncateRunes(text.OneLine(l.Title), 70))
		}
	case classSuperseded:
		cout.Printf("      <yellow>%s</> <gray>said %s:</> <gray>“</>%s<gray>”</> <darkGray>%s</>\n",
			fdg.claim.Author, text.HumanAge(fdg.claim.CreatedAt, db.Now()),
			text.TruncateRunes(text.OneLine(pr.CleanBody(fdg.claim.Body)), 140), fdg.claim.URL)
	}
	cli.PrintVerdict(v)
}
