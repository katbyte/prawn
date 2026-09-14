package close

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/katbyte/go-kt/cout"
	"github.com/katbyte/prawn/assets"
	"github.com/katbyte/prawn/cli"
	"github.com/katbyte/prawn/lib/db"
	"github.com/katbyte/prawn/lib/gh"
	"github.com/katbyte/prawn/lib/pr"
	"github.com/katbyte/prawn/lib/text"
)

// Classes by the shape of the abandonment: waiting means the maintainers'
// explicit "the ball is with the author" label sat unanswered; changes-requested
// means a review asked for changes the author never engaged with. Merge
// conflicts alone are not abandonment and are not a class here.
const (
	passStale          = "stale"
	promptStale        = "pr-stale-close"
	templateStaleClose = "stale-close"
	reasonAbandoned    = "abandoned"

	classStaleWaiting = "waiting"
	classStaleChanges = "changes-requested"

	// the label maintainers apply when they are explicitly waiting on the
	// author — the strongest signal there is, so its window is far shorter
	labelWaitingResponse = "waiting-response"

	// how long each class's silence must have lasted — the explicit
	// waiting-response label needs far less benefit of the doubt
	staleWaitingQuietDays = 90
	staleChangesQuietDays = 180
)

var staleClassRank = map[string]int{classStaleWaiting: 1, classStaleChanges: 0}

// staleFinding is one open PR whose author appears to have walked away — the
// work stopped, only the close is missing.
type staleFinding struct {
	pr     *db.PR
	review *db.Review  // changes-requested: the unanswered review
	last   *db.Comment // waiting: the maintainer's unanswered last word (may be nil)
	quiet  time.Duration
	class  string
}

// Stale finds OPEN PRs that look abandoned: waiting-response with no author
// reply, or changes requested that were never engaged with. The AI reads the thread — an author who did
// everything asked and is waiting on the maintainers scores low — before
// blessing a close. Approved PRs are never touched: those are merges waiting
// to happen, not abandonments.
func (f *Flags) Stale(link string) error {
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

	col, err := f.collectStale(d, link)
	if err != nil {
		return err
	}
	if col.open == 0 {
		cout.Printf("no fetched PRs — run <cyan>prawn fetch</> first\n")
		return nil
	}
	findings := col.findings

	cout.Printf("\n<bold>%d of %d open PRs look abandoned:</>\n", len(findings), col.open)
	for _, c := range []struct{ class, tag, desc string }{
		{classStaleWaiting, cli.TagGreen, "labelled waiting-response and the author never came back"},
		{classStaleChanges, cli.TagYellow, "changes were requested and the author never engaged"},
	} {
		if n := col.counts[c.class]; n > 0 {
			cout.Printf("  <%s>%-18s</> <yellow>%d</>  <gray>%s</>\n", c.tag, c.class, n, c.desc)
		}
	}
	cout.Printf("  <gray>skipped: %d where the silence is under the class's window · %s</>\n", col.recent, keepSummary(col.protected))
	if len(findings) == 0 {
		return nil
	}

	switch {
	case o.ApplyWithAI || o.ApplyWithAIAuto:
		if !f.AI.Enabled {
			return errors.New("--apply-with-ai needs the AI (--ai=false is set)")
		}
		return f.applyStale(d, findings, o, true)
	case o.Apply:
		return f.applyStale(d, findings, o, false)
	}

	verdicts, err := f.reportVerdicts(d, passStale, promptStale, func() ([]pr.JudgeItem, error) {
		return f.staleJudgeItems(d, findings)
	})
	if err != nil {
		return err
	}
	sortByConfidence(findings, verdicts, func(fdg *staleFinding) int { return fdg.pr.Number })

	for n := range findings {
		f.printStaleCard(&findings[n], n+1, len(findings), verdicts[findings[n].pr.Number])
	}
	cout.Printf("\nnext: <cyan>prawn close stale --apply --dry-run</> to preview the closes, <cyan>--apply-with-ai</> to confirm each, <cyan>--apply-with-ai-auto</> to trust the scores\n")
	return nil
}

// staleCollection is everything collectStale learns in one scan.
type staleCollection struct {
	findings  []staleFinding
	counts    map[string]int
	open      int
	recent    int // matched a class's shape, but not its window yet
	protected map[string]int
}

// collectStale walks every open PR looking for abandonment: the author's last
// activity (commit or comment) against what the maintainers asked, and how
// long the silence has run.
func (f *Flags) collectStale(d *db.DB, link string) (*staleCollection, error) {
	col := &staleCollection{counts: map[string]int{}, protected: map[string]int{}}
	prs, err := d.OpenPRs()
	if err != nil {
		return nil, err
	}
	col.open = len(prs)
	now := time.Now()

	cout.Printf("scanning <yellow>%d</> open PRs for abandoned work...\n", len(prs))
	for _, p := range prs {
		switch {
		case p.ReviewDecision == db.DecisionApproved:
			col.protected["approved"]++
			continue
		case p.ThumbsUp >= f.KeepReactions:
			col.protected["high-engagement"]++
			continue
		}

		comments, cerr := d.CommentsFor(p.Number)
		if cerr != nil {
			return nil, cerr
		}
		reviews, rerr := d.ReviewsFor(p.Number)
		if rerr != nil {
			return nil, rerr
		}

		// when the author last did anything: pushed, commented, or opened it
		authorLast := p.CreatedAt
		if p.LastCommitAt.After(authorLast) {
			authorLast = p.LastCommitAt
		}
		for _, c := range comments {
			if c.Author == p.Author && c.CreatedAt.After(authorLast) {
				authorLast = c.CreatedAt
			}
		}

		fdg := staleFinding{pr: p}
		switch {
		case p.HasLabel(labelWaitingResponse):
			fdg.class = classStaleWaiting
			// the maintainer's last word, for the card and the judge
			for ci := len(comments) - 1; ci >= 0; ci-- {
				if comments[ci].IsMaintainer() && !pr.Bot(comments[ci].Author) {
					fdg.last = &comments[ci]
					break
				}
			}
			since := p.UpdatedAt
			if fdg.last != nil && authorLast.Before(fdg.last.CreatedAt) {
				since = fdg.last.CreatedAt
			}
			fdg.quiet = now.Sub(since)
			if fdg.quiet < staleWaitingQuietDays*24*time.Hour {
				col.recent++
				continue
			}
		case p.ReviewDecision == db.DecisionChangesRequested:
			fdg.class = classStaleChanges
			for ri := len(reviews) - 1; ri >= 0; ri-- {
				if reviews[ri].State == db.ReviewChangesRequested {
					fdg.review = &reviews[ri]
					break
				}
			}
			// the author pushing or replying after the review means they
			// engaged — the ball is back with the maintainers
			if fdg.review == nil || authorLast.After(fdg.review.SubmittedAt) {
				continue
			}
			fdg.quiet = now.Sub(fdg.review.SubmittedAt)
			if fdg.quiet < staleChangesQuietDays*24*time.Hour {
				col.recent++
				continue
			}
		default:
			continue
		}

		if link != "" && fdg.class != link {
			continue
		}
		col.findings = append(col.findings, fdg)
		col.counts[fdg.class]++
	}

	slices.SortStableFunc(col.findings, func(a, b staleFinding) int {
		if d := staleClassRank[b.class] - staleClassRank[a.class]; d != 0 {
			return d
		}
		return a.pr.Number - b.pr.Number
	})
	return col, nil
}

// applyStale is both apply modes on the shared harness: plain --apply closes
// everything listed; --apply-with-ai[-auto] gates each close on the judge
// reading the thread, and is the recommended path — an author waiting on the
// maintainers must never robo-close.
func (f *Flags) applyStale(d *db.DB, findings []staleFinding, o cli.FlagsApplyModes, withAI bool) error {
	byNumber := map[int]*staleFinding{}
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
			return f.closeOneStale(d, repo, byNumber[n], v, pos, total, throttle, interactive)
		})
	p.Noun = "PRs that look abandoned"
	p.GateLabel = "abandonment"
	p.ConfirmAll = fmt.Sprintf("comment and close up to <yellow>%d</> abandoned PRs in %s?", len(findings), f.RepoTag())
	p.ConfirmAI = fmt.Sprintf("comment and close PRs the AI scores ≥ <green>%.2f</> (up to <yellow>%d</> candidates) in %s?", p.Threshold, len(findings), f.RepoTag())

	if !withAI {
		return p.ApplyAll(numbers)
	}
	return p.ApplyAI(len(findings), func(onReady func() (bool, error), onBatch func([]pr.Judged) (bool, error)) error {
		items, jerr := f.staleJudgeItems(d, findings)
		if jerr != nil {
			return jerr
		}
		promptText, jerr := f.PreparePrompt(promptStale)
		if jerr != nil {
			return jerr
		}
		_, jerr = f.JudgeBlocks(d, passStale, promptText, items, onReady, onBatch)
		return jerr
	})
}

// closeOneStale handles one candidate: card, the stale-close comment, and the
// close.
func (f *Flags) closeOneStale(d *db.DB, repo gh.Repo, fdg *staleFinding, v *pr.Verdict, pos, total int, throttle func(), ask bool) (int, error) {
	f.printStaleCard(fdg, pos, total, v)

	comment, err := f.renderStaleComment(fdg)
	if err != nil {
		return pr.ApplyFailed, err
	}

	evidence := map[string]string{"quiet": fdg.quiet.Round(24 * time.Hour).String()}
	switch {
	case fdg.review != nil:
		evidence["review-by"], evidence["review-url"] = fdg.review.Author, fdg.review.URL
	case fdg.last != nil:
		evidence["last-word"] = text.TruncateRunes(text.OneLine(pr.CleanBody(fdg.last.Body)), 120)
		evidence["author"], evidence["comment-url"] = fdg.last.Author, fdg.last.URL
	}

	return f.doClose(d, repo, closeReq{
		p: fdg.pr, class: fdg.class, reason: reasonAbandoned, template: templateStaleClose,
		comment: comment, evidence: evidence, source: passStale,
		question: fmt.Sprintf("close <cyan>#%d</> as abandoned?", fdg.pr.Number),
	}, v, throttle, ask)
}

// renderStaleComment renders the close comment for the finding's class.
func (f *Flags) renderStaleComment(fdg *staleFinding) (string, error) {
	tt, err := assets.CommentTemplate(templateStaleClose)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New(templateStaleClose).Parse(tt)
	if err != nil {
		return "", fmt.Errorf("parsing template %s: %w", templateStaleClose, err)
	}
	data := struct {
		Class  string
		Author string
		URL    string
	}{fdg.class, "", ""}
	if fdg.review != nil {
		data.Author, data.URL = fdg.review.Author, fdg.review.URL
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("rendering template %s: %w", templateStaleClose, err)
	}
	return strings.TrimSpace(b.String()), nil
}

// staleJudgeItems renders one judge block per finding: the PR, its class, the
// unanswered review or last word, and the thread digest so the AI can see who
// the ball is actually with.
func (f *Flags) staleJudgeItems(d *db.DB, findings []staleFinding) ([]pr.JudgeItem, error) {
	items := make([]pr.JudgeItem, 0, len(findings))
	for i := range findings {
		fdg := &findings[i]
		var b strings.Builder
		writePRBlockHeader(&b, fdg.pr, fdg.class)

		fmt.Fprintf(&b, "MERGEABLE: %s · last commit %s · quiet for %s\n",
			text.OrDefault(fdg.pr.Mergeable, "unknown"), fdg.pr.LastCommitAt.Format("2006-01-02"),
			text.HumanAge(time.Now().Add(-fdg.quiet), time.Now()))
		switch {
		case fdg.review != nil:
			fmt.Fprintf(&b, "THE UNANSWERED REVIEW, by %s (%s) on %s:\n%s\n",
				fdg.review.Author, fdg.review.AuthorAssociation, fdg.review.SubmittedAt.Format("2006-01-02"),
				text.TruncateRunes(pr.CleanBody(fdg.review.Body), 1500))
		case fdg.last != nil:
			fmt.Fprintf(&b, "THE MAINTAINER'S LAST WORD, unanswered since, by %s (%s) on %s:\n%s\n",
				fdg.last.Author, fdg.last.AuthorAssociation, fdg.last.CreatedAt.Format("2006-01-02"),
				text.TruncateRunes(pr.CleanBody(fdg.last.Body), 1500))
		}

		if err := writeThreadDigest(&b, d, fdg.pr.Number); err != nil {
			return nil, err
		}
		items = append(items, pr.JudgeItem{Number: fdg.pr.Number, Block: b.String()})
	}
	return items, nil
}

// printStaleCard is one candidate: the PR, why it looks abandoned and for how
// long, and the AI's score when judged.
func (f *Flags) printStaleCard(fdg *staleFinding, pos, total int, v *pr.Verdict) {
	printCardHeader(fdg.pr, pos, total)
	cout.Printf("      %s\n", authorLine(fdg.pr))
	now := time.Now()
	quiet := text.HumanAge(now.Add(-fdg.quiet), now)
	switch fdg.class {
	case classStaleWaiting:
		cout.Printf("      <gray>labelled</> <lightYellow>%s</> <gray>· the author has been silent for %s</>\n", labelWaitingResponse, quiet)
		if fdg.last != nil {
			cout.Printf("      <gray>“</>%s<gray>”</> <darkGray>%s</>\n",
				text.TruncateRunes(text.OneLine(pr.CleanBody(fdg.last.Body)), 140), fdg.last.URL)
		}
	case classStaleChanges:
		cout.Printf("      <yellow>%s</> <gray>requested changes %s ago, no commits or replies since</>\n", fdg.review.Author, quiet)
		if body := text.OneLine(pr.CleanBody(fdg.review.Body)); body != "" {
			cout.Printf("      <gray>“</>%s<gray>”</> <darkGray>%s</>\n", text.TruncateRunes(body, 140), fdg.review.URL)
		}
	}
	cli.PrintVerdict(v)
}
