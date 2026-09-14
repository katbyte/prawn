// The close-candidates report: one section per check, riding the shared
// report scaffolding in package cli.

package close

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/katbyte/go-kt/cout"
	"github.com/katbyte/prawn/cli"
	"github.com/katbyte/prawn/lib/db"
	"github.com/katbyte/prawn/lib/pr"
	"github.com/katbyte/prawn/lib/text"
)

// Report writes close-<stamp>.html: every close candidate each check sees
// (resolved, duplicate, stale, deprecated), with the evidence for why it is
// listed and links to everything cited. --with-ai scores each candidate with
// the check's judge (cached verdicts are reused) and sorts surest first;
// --limit N keeps test runs cheap.
func (f *Flags) Report() error {
	o := f.Cmd.Report
	if o.WithAI {
		if !f.AI.Enabled {
			return errors.New("--with-ai needs the AI (--ai=false is set)")
		}
		if err := f.RequireAI(); err != nil {
			return err
		}
	}
	// the deprecated check reads a provider checkout; a report missing a check
	// would be acted on as if it were complete, so fail up front — before the
	// auto-fetch and the scans — rather than silently under-report
	if f.Cmd.SrcDir == "" {
		return errors.New("the deprecated check needs a provider checkout: set --src-dir or PRAWN_SRC_DIR")
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

	now := time.Now()
	data := cli.ReportData{Repo: f.GH.Repo, Noun: "close candidates", WithAI: o.WithAI, GeneratedAt: now.Format("2006-01-02 15:04")}

	resolved, err := f.resolvedReportSection(d, o, now)
	if err != nil {
		return err
	}
	duplicate, err := f.duplicateReportSection(d, o, now)
	if err != nil {
		return err
	}
	stale, err := f.staleReportSection(d, o, now)
	if err != nil {
		return err
	}
	deprecated, err := f.deprecatedReportSection(d, o, now)
	if err != nil {
		return err
	}
	data.Sections = []cli.ReportSection{resolved, duplicate, stale, deprecated}
	for _, s := range data.Sections {
		data.Total += s.Total
	}
	if data.Total == 0 {
		cout.Printf("no close candidates in any check — is the db fetched? (<cyan>prawn fetch</>)\n")
		return nil
	}

	if err := os.MkdirAll(o.Out, 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", o.Out, err)
	}
	htmlPath := filepath.Join(o.Out, cli.ReportFileName("close", now))
	if err := cli.WriteReportHTML(htmlPath, &data); err != nil {
		return err
	}
	cout.Printf("\nwrote <cyan>%s</> — <yellow>%d</> close candidates <gray>(resolved %d · duplicate %d · stale %d · deprecated %d)</>\n",
		htmlPath, data.Total, resolved.Total, duplicate.Total, stale.Total, deprecated.Total)
	if !o.WithAI {
		cout.Printf("<gray>rerun with</> <cyan>--with-ai</> <gray>to score every candidate, or</> <cyan>--limit 10</> <gray>to test cheaply</>\n")
	}
	// a file:// url so the terminal makes the page clickable
	if abs, aerr := filepath.Abs(htmlPath); aerr == nil {
		cout.Printf("<gray>open:</> <cyan>file://%s</>\n", abs)
	}
	return nil
}

// prMeta is the meta line every section's items share.
func prMeta(p *db.PR, now time.Time) string {
	draft := ""
	if p.IsDraft {
		draft = " · draft"
	}
	return fmt.Sprintf("@%s · opened %s ago · last activity %s ago · +%d/-%d over %d files · 💬 %d · 👍 %d · review %s%s",
		p.Author, text.HumanAge(p.CreatedAt, now), text.HumanAge(p.UpdatedAt, now),
		p.Additions, p.Deletions, p.ChangedFiles, p.CommentCount, p.ThumbsUp,
		strings.ToLower(text.OrDefault(p.ReviewDecision, "none")), draft)
}

// resolvedReportSection builds the "already dealt with" check section.
func (f *Flags) resolvedReportSection(d *db.DB, o cli.FlagsReport, now time.Time) (cli.ReportSection, error) {
	s := cli.ReportSection{
		Slug:     passResolved,
		Name:     "close " + passResolved,
		Question: "the change this open PR proposes has already been resolved — close it?",
		Description: "Every open PR whose purpose appears already served. Landed: a commit merged after the PR was opened " +
			"covers most of its files without being a repo-wide sweep, read from the provider checkout's git history. " +
			"Issue-closed: every issue the PR's closing keywords reference has since been closed. Superseded: a " +
			"maintainer's comment on the thread says the change was covered elsewhere. The AI reads each candidate " +
			"exhaustively — a commit that touched the same files for another reason, or a PR doing more than its linked " +
			"issues cover, scores low. Applying closes with a comment citing the landing, the closed issues, or the claim.",
		Command: "prawn close resolved [landed|issue-closed|superseded] --apply / --apply-with-ai / --apply-with-ai-auto",
	}
	col, err := f.collectResolved(d, "")
	if err != nil {
		return s, err
	}
	findings := col.findings
	s.Total = len(findings)
	s.Classes = []cli.ReportClass{
		{Name: classLanded, Count: col.counts[classLanded], Kind: cli.KindOK},
		{Name: classIssueClosed, Count: col.counts[classIssueClosed], Kind: cli.KindMid},
		{Name: classSuperseded, Count: col.counts[classSuperseded], Kind: cli.KindWarn},
	}
	s.Note = keepSummary(col.protected)
	if col.history == nil {
		s.Note += " · landed class skipped: no --src-dir / PRAWN_SRC_DIR"
	}

	findings, s.Truncated = cli.LimitFindings(findings, o.Limit)
	var verdicts map[int]*pr.Verdict
	if o.WithAI && len(findings) > 0 {
		items, jerr := f.resolvedJudgeItems(d, findings)
		if jerr != nil {
			return s, jerr
		}
		promptText, jerr := f.PreparePrompt(promptResolved)
		if jerr != nil {
			return s, jerr
		}
		if verdicts, err = f.JudgeBlocksBatch(d, passResolved, promptText, resolvedJudgeBatch, items, nil, nil); err != nil {
			return s, err
		}
		cli.SortByVerdict(findings, func(x *resolvedFinding) int { return x.pr.Number }, verdicts)
	}

	for i := range findings {
		fdg := &findings[i]
		item := cli.ReportItem{Number: fdg.pr.Number, URL: fdg.pr.URL, Title: text.OneLine(fdg.pr.Title), Meta: prMeta(fdg.pr, now)}
		if l := fdg.landed; l != nil {
			item.Evidence = append(item.Evidence, []cli.ReportSpan{
				cli.Span("landed in", cli.KindOK),
				cli.LinkSpan(f.landingRef(l), f.landingURL(l)),
				cli.Span(fmt.Sprintf("on %s by @%s ·", l.Commit.Date.Format("2006-01-02"), l.Commit.Author), cli.KindDim),
				cli.Span("shipped in "+text.OrDefault(l.ShippedIn, "no release yet"), cli.KindVer),
			}, []cli.ReportSpan{cli.Span("“"+text.OneLine(l.Commit.Subject)+"”", cli.KindQuote)})
			var row []cli.ReportSpan
			for _, part := range []struct {
				label string
				toks  []string
				kind  string
			}{
				{"now in the source:", l.Landed, cli.KindOK},
				{"not found:", l.Missing, cli.KindBad},
				{"gone as the PR removes:", l.Gone, cli.KindOK},
				{"still there:", l.Still, cli.KindBad},
			} {
				if len(part.toks) > 0 {
					row = append(row, cli.Span(part.label, cli.KindDim), cli.Span(strings.Join(part.toks, " "), part.kind))
				}
			}
			item.Evidence = append(item.Evidence, row)
		}
		switch fdg.class {
		case classLanded, classIssueClosed:
			for _, l := range fdg.closes {
				reason := strings.ToLower(strings.ReplaceAll(text.OrDefault(l.StateReason, "unknown"), "_", " "))
				kind := cli.KindMid
				if strings.Contains(reason, "completed") {
					kind = cli.KindOK
				}
				item.Evidence = append(item.Evidence, []cli.ReportSpan{
					cli.Span("fixes", cli.KindDim),
					cli.LinkSpan(fmt.Sprintf("#%d", l.IssueNumber), f.IssueURL(l.IssueNumber)),
					cli.Span("closed as "+reason, kind),
					cli.Span("· "+text.OneLine(l.Title), cli.KindDim),
				})
			}
		case classSuperseded:
			row := []cli.ReportSpan{
				cli.Span("@"+fdg.claim.Author, cli.KindOK),
				cli.Span(text.HumanAge(fdg.claim.CreatedAt, now)+" ago:", cli.KindDim),
				cli.Span("“"+text.TruncateRunes(text.OneLine(pr.CleanBody(fdg.claim.Body)), 200)+"”", cli.KindQuote),
			}
			if fdg.claim.URL != "" {
				row = append(row, cli.LinkSpan("view comment", fdg.claim.URL))
			}
			item.Evidence = append(item.Evidence, row)
		}
		cli.AttachVerdict(&item, verdicts[fdg.pr.Number])
		s.Items = append(s.Items, item)
	}
	return s, nil
}

// duplicateReportSection builds the "another open PR makes the same change"
// check section.
func (f *Flags) duplicateReportSection(d *db.DB, o cli.FlagsReport, now time.Time) (cli.ReportSection, error) {
	s := cli.ReportSection{
		Slug:     passDuplicate,
		Name:     "close " + passDuplicate,
		Question: "another open PR makes the same change — keep the review in one place?",
		Description: "Every open PR that another open PR duplicates. Same-issue: both PRs' closing keywords reference the " +
			"same issue. Similar: near-identical titles over overlapping changed files, where nobody linked the pair. The " +
			"survivor is the PR carrying more of the review (approved first, then review and comment weight, then the " +
			"older); approved PRs are never the closed side. Applying closes with a comment pointing at the survivor.",
		Command: "prawn close duplicate [same-issue|similar] --apply / --apply-with-ai / --apply-with-ai-auto",
	}
	col, err := f.collectDuplicate(d, "")
	if err != nil {
		return s, err
	}
	findings := col.findings
	s.Total = len(findings)
	s.Classes = []cli.ReportClass{
		{Name: classSameIssue, Count: col.counts[classSameIssue], Kind: cli.KindOK},
		{Name: classSimilar, Count: col.counts[classSimilar], Kind: cli.KindMid},
	}
	s.Note = keepSummary(col.protected)

	findings, s.Truncated = cli.LimitFindings(findings, o.Limit)
	var verdicts map[int]*pr.Verdict
	if o.WithAI && len(findings) > 0 {
		items, jerr := f.duplicateJudgeItems(d, findings)
		if jerr != nil {
			return s, jerr
		}
		promptText, jerr := f.PreparePrompt(promptDuplicate)
		if jerr != nil {
			return s, jerr
		}
		if verdicts, err = f.JudgeBlocks(d, passDuplicate, promptText, items, nil, nil); err != nil {
			return s, err
		}
		cli.SortByVerdict(findings, func(x *duplicateFinding) int { return x.pr.Number }, verdicts)
	}

	for i := range findings {
		fdg := &findings[i]
		item := cli.ReportItem{Number: fdg.pr.Number, URL: fdg.pr.URL, Title: text.OneLine(fdg.pr.Title), Meta: prMeta(fdg.pr, now)}
		row := []cli.ReportSpan{
			cli.Span("duplicates", cli.KindDim),
			cli.LinkSpan(fmt.Sprintf("#%d", fdg.target.Number), fdg.target.URL),
		}
		if fdg.viaIssue > 0 {
			row = append(row, cli.Span("both close", cli.KindOK), cli.LinkSpan(fmt.Sprintf("#%d", fdg.viaIssue), f.IssueURL(fdg.viaIssue)))
		} else {
			row = append(row, cli.Span("near-identical titles over overlapping files", cli.KindMid))
		}
		row = append(row, cli.Span(fmt.Sprintf("· survivor: @%s · 💬 %d · review %s · opened %s ago",
			fdg.target.Author, fdg.target.CommentCount, strings.ToLower(text.OrDefault(fdg.target.ReviewDecision, "none")),
			text.HumanAge(fdg.target.CreatedAt, now)), cli.KindDim))
		item.Evidence = append(item.Evidence, row,
			[]cli.ReportSpan{cli.Span("“"+text.OneLine(fdg.target.Title)+"”", cli.KindQuote)})
		cli.AttachVerdict(&item, verdicts[fdg.pr.Number])
		s.Items = append(s.Items, item)
	}
	return s, nil
}

// staleReportSection builds the "the author walked away" check section.
func (f *Flags) staleReportSection(d *db.DB, o cli.FlagsReport, now time.Time) (cli.ReportSection, error) {
	s := cli.ReportSection{
		Slug:     passStale,
		Name:     "close " + passStale,
		Question: "the author walked away — close out the thread?",
		Description: fmt.Sprintf("Open PRs whose author appears to have walked away: waiting (labelled waiting-response and "+
			"the author has not come back in %d days) or changes-requested (a maintainer's review requested changes and "+
			"the author neither pushed nor replied for %d days). An author who addressed the feedback and is waiting on "+
			"the maintainers scores low. Approved PRs are never touched. Applying closes with a comment inviting a fresh "+
			"rebase.", staleWaitingQuietDays, staleChangesQuietDays),
		Command: "prawn close stale [waiting|changes-requested] --apply / --apply-with-ai / --apply-with-ai-auto",
	}
	col, err := f.collectStale(d, "")
	if err != nil {
		return s, err
	}
	findings := col.findings
	s.Total = len(findings)
	s.Classes = []cli.ReportClass{
		{Name: classStaleWaiting, Count: col.counts[classStaleWaiting], Kind: cli.KindOK},
		{Name: classStaleChanges, Count: col.counts[classStaleChanges], Kind: cli.KindMid},
	}
	s.Note = fmt.Sprintf("%d more match a class but are under its window · %s", col.recent, keepSummary(col.protected))

	findings, s.Truncated = cli.LimitFindings(findings, o.Limit)
	var verdicts map[int]*pr.Verdict
	if o.WithAI && len(findings) > 0 {
		items, jerr := f.staleJudgeItems(d, findings)
		if jerr != nil {
			return s, jerr
		}
		promptText, jerr := f.PreparePrompt(promptStale)
		if jerr != nil {
			return s, jerr
		}
		if verdicts, err = f.JudgeBlocks(d, passStale, promptText, items, nil, nil); err != nil {
			return s, err
		}
		cli.SortByVerdict(findings, func(x *staleFinding) int { return x.pr.Number }, verdicts)
	}

	for i := range findings {
		fdg := &findings[i]
		item := cli.ReportItem{Number: fdg.pr.Number, URL: fdg.pr.URL, Title: text.OneLine(fdg.pr.Title), Meta: prMeta(fdg.pr, now)}
		quiet := text.HumanAge(now.Add(-fdg.quiet), now)
		switch fdg.class {
		case classStaleWaiting:
			item.Evidence = append(item.Evidence, []cli.ReportSpan{
				cli.Span("labelled "+labelWaitingResponse, cli.KindOK),
				cli.Span("· the author has been silent for "+quiet, cli.KindDim),
			})
			if fdg.last != nil {
				row := []cli.ReportSpan{
					cli.Span("@"+fdg.last.Author, cli.KindMid),
					cli.Span(text.HumanAge(fdg.last.CreatedAt, now)+" ago, unanswered since:", cli.KindDim),
					cli.Span("“"+text.TruncateRunes(text.OneLine(pr.CleanBody(fdg.last.Body)), 200)+"”", cli.KindQuote),
				}
				if fdg.last.URL != "" {
					row = append(row, cli.LinkSpan("view comment", fdg.last.URL))
				}
				item.Evidence = append(item.Evidence, row)
			}
		case classStaleChanges:
			row := []cli.ReportSpan{
				cli.Span("@"+fdg.review.Author, cli.KindMid),
				cli.Span("requested changes "+quiet+" ago, no commits or replies since", cli.KindDim),
			}
			if body := text.OneLine(pr.CleanBody(fdg.review.Body)); body != "" {
				row = append(row, cli.Span("“"+text.TruncateRunes(body, 200)+"”", cli.KindQuote))
			}
			if fdg.review.URL != "" {
				row = append(row, cli.LinkSpan("view review", fdg.review.URL))
			}
			item.Evidence = append(item.Evidence, row)
		}
		cli.AttachVerdict(&item, verdicts[fdg.pr.Number])
		s.Items = append(s.Items, item)
	}
	return s, nil
}

// deprecatedReportSection builds the "the thing it changes is gone" check
// section.
func (f *Flags) deprecatedReportSection(d *db.DB, o cli.FlagsReport, now time.Time) (cli.ReportSection, error) {
	s := cli.ReportSection{
		Slug:     passDeprecated,
		Name:     "close " + passDeprecated,
		Question: "this PR changes something the provider has removed or deprecated — moot as proposed?",
		Description: "Every open PR whose change targets a resource, data source, or property that no longer exists in " +
			"the provider — or is formally on the way out — per the upgrade guides, the changelog's DEPRECATIONS " +
			"sections, and DeprecationMessage markers in the source. A PR that equally changes a living resource is not " +
			"moot, and deprecated things still work, so those score low. Applying closes with a comment naming what is " +
			"gone and the successor to use.",
		Command: "prawn close deprecated [resource|property] --src-dir <checkout> --apply / --apply-with-ai / --apply-with-ai-auto",
	}
	col, err := f.collectDeprecated(d, "")
	if err != nil {
		return s, err
	}
	findings := col.findings
	s.Total = len(findings)
	s.Classes = []cli.ReportClass{
		{Name: classRemovedResource, Count: col.counts[classRemovedResource], Kind: cli.KindBad},
		{Name: classRemovedProperty, Count: col.counts[classRemovedProperty], Kind: cli.KindWarn},
		{Name: classDeprecatedResource, Count: col.counts[classDeprecatedResource], Kind: cli.KindMid},
		{Name: classDeprecatedProperty, Count: col.counts[classDeprecatedProperty], Kind: cli.KindDim},
	}
	note := keepSummary(col.protected)
	if len(col.noisy) > 0 {
		note += fmt.Sprintf(" · skipped %d too-generic property tokens: %s", len(col.noisy), text.TruncateRunes(strings.Join(col.noisy, " "), 100))
	}
	s.Note = note

	findings, s.Truncated = cli.LimitFindings(findings, o.Limit)
	var verdicts map[int]*pr.Verdict
	if o.WithAI && len(findings) > 0 {
		items, jerr := f.deprecatedJudgeItems(d, findings)
		if jerr != nil {
			return s, jerr
		}
		promptText, jerr := f.PreparePrompt(promptDeprecated)
		if jerr != nil {
			return s, jerr
		}
		if verdicts, err = f.JudgeBlocks(d, passDeprecated, promptText, items, nil, nil); err != nil {
			return s, err
		}
		cli.SortByVerdict(findings, func(x *deprecatedFinding) int { return x.pr.Number }, verdicts)
	}

	for i := range findings {
		fdg := &findings[i]
		item := cli.ReportItem{Number: fdg.pr.Number, URL: fdg.pr.URL, Title: text.OneLine(fdg.pr.Title), Meta: prMeta(fdg.pr, now)}
		for n := range fdg.matches {
			m := &fdg.matches[n]
			r := m.removal
			actionKind := cli.KindMid
			if r.Action == pr.RemovalRemoved {
				actionKind = cli.KindBad
			}
			row := make([]cli.ReportSpan, 0, 7)
			if r.Kind == pr.RemovalKindProperty {
				row = append(row, cli.Span(r.Property, ""), cli.Span("on "+r.Resource, cli.KindDim))
			} else {
				row = append(row, cli.Span(r.Resource, ""), cli.Span("("+strings.ReplaceAll(r.Kind, "-", " ")+")", cli.KindDim))
			}
			row = append(row, cli.Span(r.Action, actionKind))
			var where string
			switch {
			case strings.HasPrefix(r.Source, "changelog "):
				where = "in " + strings.TrimPrefix(r.Source, "changelog ")
			case r.Major > 0:
				where = fmt.Sprintf("in v%d.0 (%s)", r.Major, r.Source)
			default:
				where = "(" + r.Source + ")"
			}
			if url := f.removalURL(r); url != "" {
				row = append(row, cli.LinkSpan(where, url))
			} else {
				row = append(row, cli.Span(where, cli.KindDim))
			}
			if r.Successor != "" {
				row = append(row, cli.Span("· use "+r.Successor, cli.KindOK))
			}
			if m.absent {
				row = append(row, cli.Span("· absent from source", cli.KindBad))
			}
			item.Evidence = append(item.Evidence, row)
			switch {
			case m.file != "":
				item.Evidence = append(item.Evidence, []cli.ReportSpan{
					cli.Span("in the PR's diff of "+filepath.Base(m.file)+":", cli.KindOK), cli.Span("“"+m.quote+"”", cli.KindQuote),
				})
			case m.quote != "":
				item.Evidence = append(item.Evidence, []cli.ReportSpan{
					cli.Span("matched in the title/body:", cli.KindDim), cli.Span("“"+m.quote+"”", cli.KindQuote),
				})
			}
		}
		if len(fdg.alive) > 0 {
			item.Evidence = append(item.Evidence, []cli.ReportSpan{
				cli.Span("also touches (not removed or deprecated):", cli.KindDim),
				cli.Span(strings.Join(fdg.alive, " · "), cli.KindOK),
			})
		}
		cli.AttachVerdict(&item, verdicts[fdg.pr.Number])
		s.Items = append(s.Items, item)
	}
	return s, nil
}
