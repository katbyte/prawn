package close

import (
	"errors"
	"fmt"
	"path/filepath"
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

// Classes by what the PR leans on, strongest first: a removed resource is gone
// outright, a removed property nearly so; deprecations are still working but
// on notice.
const (
	passDeprecated          = "deprecated"
	promptDeprecated        = "pr-deprecated-close"
	templateDeprecatedClose = "deprecated-close"
	reasonDeprecated        = "deprecated-removed"

	classRemovedResource    = "removed-resource"
	classRemovedProperty    = "removed-property"
	classDeprecatedResource = "deprecated-resource"
	classDeprecatedProperty = "deprecated-property"

	// property tokens word-matching more than this share of open PRs are too
	// generic to trust (resource_group, name...) and are skipped
	deprecatedDFCap = 0.03
)

var deprecatedClassRank = map[string]int{
	classRemovedResource: 3, classRemovedProperty: 2, classDeprecatedResource: 1, classDeprecatedProperty: 0,
}

// deprecatedTypeOf maps a class to its subcommand scope: resource or property.
func deprecatedTypeOf(class string) string {
	if class == classRemovedProperty || class == classDeprecatedProperty {
		return pr.RemovalKindProperty
	}
	return pr.RemovalKindResource
}

func deprecatedClassOf(r pr.Removal) string {
	prop := r.Kind == pr.RemovalKindProperty
	switch {
	case !prop && r.Action == pr.RemovalRemoved:
		return classRemovedResource
	case prop && r.Action == pr.RemovalRemoved:
		return classRemovedProperty
	case !prop:
		return classDeprecatedResource
	default:
		return classDeprecatedProperty
	}
}

// deprecatedMatch is one removal the PR leans on: the line that matched for
// property-level hits (a changed line of the PR's diff in the resource's own
// file when the diff is held — the strongest form — else a line of the
// title/body prose), and whether the source corroborates a removal (the name
// appears nowhere under internal/services any more).
type deprecatedMatch struct {
	removal pr.Removal
	quote   string
	file    string // the diff file the quote came from; "" for a prose match
	absent  bool
}

// deprecatedFinding is one open PR whose change targets removed/deprecated
// things. matches are sorted strongest first; the close comment cites the
// first. alive holds the PR's other resources — the ones with no
// resource-level removal — so the whole picture is visible: a PR that equally
// changes a living resource is probably not moot.
type deprecatedFinding struct {
	pr      *db.PR
	matches []deprecatedMatch
	alive   []string
	class   string
}

// Deprecated scans every OPEN PR against the removals inventory (upgrade
// guides + changelog DEPRECATIONS + source DeprecationMessage markers, read
// from --src-dir): PRs changing resources, data sources, or properties that no
// longer exist — or are on the way out — where the change is moot as proposed.
// The AI judges whether each PR's substance actually centres on the dead
// thing; the apply modes close with a comment pointing at the successor.
func (f *Flags) Deprecated(link string) error {
	o := f.Modes
	if f.Cmd.SrcDir == "" {
		return errors.New("--src-dir is required: a local checkout of the provider supplies the upgrade guides, changelog, and deprecation markers")
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

	col, err := f.collectDeprecated(d, link)
	if err != nil {
		return err
	}
	if col.open == 0 {
		cout.Printf("no fetched PRs — run <cyan>prawn fetch</> first\n")
		return nil
	}
	findings := col.findings

	cout.Printf("\n<bold>%d of %d open PRs change something removed or deprecated:</>\n", len(findings), col.open)
	for _, c := range []struct{ class, tag, desc string }{
		{classRemovedResource, cli.TagRed, "the resource is gone from the provider"},
		{classRemovedProperty, cli.TagOrange, "the property is gone from its resource"},
		{classDeprecatedResource, cli.TagYellow, "the resource is on the way out"},
		{classDeprecatedProperty, cli.TagGray, "the property is on the way out"},
	} {
		if n := col.counts[c.class]; n > 0 {
			cout.Printf("  <%s>%-20s</> <yellow>%d</>  <gray>%s</>\n", c.tag, c.class, n, c.desc)
		}
	}
	if len(col.noisy) > 0 {
		cout.Printf("  <gray>skipped %d too-generic property tokens: %s</>\n", len(col.noisy), text.TruncateRunes(strings.Join(col.noisy, " "), 100))
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
		return f.applyDeprecated(d, findings, o, true)
	case o.Apply:
		return f.applyDeprecated(d, findings, o, false)
	}

	verdicts, err := f.reportVerdicts(d, passDeprecated, promptDeprecated, func() ([]pr.JudgeItem, error) {
		return f.deprecatedJudgeItems(d, findings)
	})
	if err != nil {
		return err
	}
	sortByConfidence(findings, verdicts, func(fdg *deprecatedFinding) int { return fdg.pr.Number })

	for n := range findings {
		f.printDeprecatedCard(&findings[n], n+1, len(findings), verdicts[findings[n].pr.Number])
	}
	cout.Printf("\nnext: <cyan>prawn close deprecated --apply --dry-run</> to preview the closes, <cyan>--apply-with-ai</> to confirm each, <cyan>--apply-with-ai-auto</> to trust the scores\n")
	return nil
}

// deprecatedCollection is everything collectDeprecated learns in one scan.
type deprecatedCollection struct {
	findings  []deprecatedFinding
	counts    map[string]int
	noisy     []string
	open      int
	protected map[string]int
}

var (
	reAzurermToken = regexp.MustCompile(`\b(?:data\.)?(azurerm_[a-z0-9_]+)\b`)
	reSnakeToken   = regexp.MustCompile(`\b[a-z][a-z0-9_]*\b`)
	reFencedCode   = regexp.MustCompile("(?s)```.*?```")
)

// prose strips fenced code blocks: a removed property sitting in somebody's
// pasted config or test output says nothing about what the PR changes.
func prose(s string) string {
	return reFencedCode.ReplaceAllString(pr.CleanBody(s), "")
}

// resourcesOf names every resource a PR plausibly targets: azurerm_* tokens in
// the title and body prose, and the resource/doc filenames it changes.
func resourcesOf(p *db.PR, t string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, m := range reAzurermToken.FindAllStringSubmatch(t, -1) {
		// wildcard shorthand like `azurerm_hdinsight_*_cluster` leaves a
		// truncated match with a trailing underscore — not a real name
		add(strings.TrimRight(m[1], "_"))
	}
	for _, file := range p.Files {
		add(resourceOfFile(file))
	}
	return out
}

// resourceOfFile names the resource a changed file belongs to: the resource
// implementation or its doc page. "" for anything else (tests, shared code).
func resourceOfFile(file string) string {
	base := filepath.Base(file)
	switch {
	case strings.HasPrefix(file, "internal/services/") && strings.HasSuffix(base, "_resource.go") && !strings.HasSuffix(base, "_test.go"):
		return "azurerm_" + strings.TrimSuffix(base, "_resource.go")
	case strings.HasPrefix(file, "website/docs/r/") || strings.HasPrefix(file, "website/docs/d/"):
		return "azurerm_" + strings.TrimSuffix(base, ".html.markdown")
	}
	return ""
}

// collectDeprecated builds the findings: every open PR that targets a
// removed/deprecated resource, or whose text word-matches a removed/deprecated
// property of one of its resources.
func (f *Flags) collectDeprecated(d *db.DB, link string) (*deprecatedCollection, error) {
	col := &deprecatedCollection{counts: map[string]int{}, protected: map[string]int{}}
	prs, err := d.OpenPRs()
	if err != nil {
		return nil, err
	}
	col.open = len(prs)

	inv, err := pr.LoadInventory(f.Cmd.SrcDir)
	if err != nil {
		return nil, err
	}

	// prefer removed over deprecated when both exist for the same item
	byRank := func(action string) int {
		if action == pr.RemovalRemoved {
			return 1
		}
		return 0
	}
	strongest := map[string]pr.Removal{}
	for _, r := range inv.Removals {
		key := r.Kind + "|" + r.Resource + "|" + r.Property
		if cur, ok := strongest[key]; !ok || byRank(r.Action) > byRank(cur.Action) {
			strongest[key] = r
		}
	}
	resourceLevel := map[string][]pr.Removal{}
	propertyLevel := map[string][]pr.Removal{}
	for _, r := range strongest {
		if r.Kind == pr.RemovalKindProperty {
			propertyLevel[r.Resource] = append(propertyLevel[r.Resource], r)
		} else {
			resourceLevel[r.Resource] = append(resourceLevel[r.Resource], r)
		}
	}

	// property matching: leaf token of dotted paths, matched against each PR's
	// snake-token set, with a document-frequency cap so generic tokens can't
	// flood the scan
	leafOf := func(p string) string {
		parts := strings.Split(p, ".")
		return parts[len(parts)-1]
	}
	matchers := map[string]*regexp.Regexp{}
	for _, props := range propertyLevel {
		for _, r := range props {
			leaf := leafOf(r.Property)
			if _, ok := matchers[leaf]; !ok {
				// the regex only runs on actual hits, to pull the quote line
				matchers[leaf] = regexp.MustCompile(`\b` + regexp.QuoteMeta(leaf) + `\b`)
			}
		}
	}
	cout.Printf("scanning <yellow>%d</> open PRs against <yellow>%d</> removals (<yellow>%d</> property tokens) from <cyan>%s</>...\n",
		len(prs), len(inv.Removals), len(matchers), f.Cmd.SrcDir)
	texts := make(map[int]string, len(prs))
	tokens := make(map[int]map[string]bool, len(prs))
	for _, p := range prs {
		t := p.Title + "\n" + prose(p.Body)
		texts[p.Number] = t
		set := map[string]bool{}
		for _, tok := range reSnakeToken.FindAllString(t, -1) {
			set[tok] = true
		}
		tokens[p.Number] = set
	}
	// the diffs, parsed once: a property named on a changed line of the
	// resource's own file is what the PR acts on, not what it talks about
	rawDiffs, err := d.AllDiffs()
	if err != nil {
		return nil, err
	}
	diffs := make(map[int][]pr.FileDiff, len(rawDiffs))
	withDiff := 0
	for n, raw := range rawDiffs {
		if raw != "" {
			diffs[n] = pr.ParseDiff(raw)
			withDiff++
		}
	}
	if withDiff == 0 {
		cout.Printf("  <yellow>no diffs held</> <gray>— run prawn fetch to pull them; matching on title/body prose only</>\n")
	}

	df := map[string]int{}
	for leaf := range matchers {
		for _, set := range tokens {
			if set[leaf] {
				df[leaf]++
			}
		}
	}
	tooGeneric := map[string]bool{}
	for leaf, n := range df {
		if float64(n) > float64(len(prs))*deprecatedDFCap {
			tooGeneric[leaf] = true
		}
	}

	for _, p := range prs {
		switch {
		case p.ReviewDecision == db.DecisionApproved:
			col.protected["approved"]++
			continue
		case p.ThumbsUp >= f.KeepReactions:
			col.protected["high-engagement"]++
			continue
		}

		// the diff's changed lines, by the resource each file belongs to
		diffLines := map[string][]diffLine{}
		for _, fd := range diffs[p.Number] {
			res := resourceOfFile(fd.Path)
			if res == "" {
				continue
			}
			for _, l := range fd.Removed {
				diffLines[res] = append(diffLines[res], diffLine{file: fd.Path, text: "- " + l})
			}
			for _, l := range fd.Added {
				diffLines[res] = append(diffLines[res], diffLine{file: fd.Path, text: "+ " + l})
			}
		}

		fdg := deprecatedFinding{pr: p}
		for _, res := range resourcesOf(p, texts[p.Number]) {
			if len(resourceLevel[res]) == 0 && !slices.Contains(fdg.alive, res) {
				fdg.alive = append(fdg.alive, res)
			}
			for _, r := range resourceLevel[res] {
				fdg.matches = append(fdg.matches, deprecatedMatch{
					removal: r, absent: r.Action == pr.RemovalRemoved && !inv.Live[res],
				})
			}
			for _, r := range propertyLevel[res] {
				leaf := leafOf(r.Property)
				if tooGeneric[leaf] {
					continue
				}
				// the diff first: a changed line naming the property in the
				// resource's own file is the PR acting on it
				if m, ok := matchDiff(diffLines[res], matchers[leaf]); ok {
					fdg.matches = append(fdg.matches, deprecatedMatch{removal: r, quote: m.text, file: m.file})
					continue
				}
				if !tokens[p.Number][leaf] {
					continue
				}
				if quote := matchLine(texts[p.Number], matchers[leaf]); quote != "" {
					fdg.matches = append(fdg.matches, deprecatedMatch{removal: r, quote: quote})
				}
			}
		}
		if len(fdg.matches) == 0 {
			continue
		}
		for _, m := range fdg.matches {
			if c := deprecatedClassOf(m.removal); fdg.class == "" || deprecatedClassRank[c] > deprecatedClassRank[fdg.class] {
				fdg.class = c
			}
		}
		// strongest evidence first — the close comment cites matches[0]
		slices.SortStableFunc(fdg.matches, func(a, b deprecatedMatch) int {
			return deprecatedClassRank[deprecatedClassOf(b.removal)] - deprecatedClassRank[deprecatedClassOf(a.removal)]
		})
		if link != "" && deprecatedTypeOf(fdg.class) != link {
			continue
		}
		col.findings = append(col.findings, fdg)
		col.counts[fdg.class]++
	}

	slices.SortStableFunc(col.findings, func(a, b deprecatedFinding) int {
		if d := deprecatedClassRank[b.class] - deprecatedClassRank[a.class]; d != 0 {
			return d
		}
		return a.pr.Number - b.pr.Number
	})
	col.noisy = text.SortedKeys(tooGeneric)
	f.currentMajor = inv.CurrentMajor
	return col, nil
}

// applyDeprecated is both apply modes on the shared harness: plain --apply
// closes everything listed (the raw evidence includes incidental mentions, so
// it exists for pattern consistency); --apply-with-ai[-auto] gates each close
// on the judge and is the recommended path.
func (f *Flags) applyDeprecated(d *db.DB, findings []deprecatedFinding, o cli.FlagsApplyModes, withAI bool) error {
	byNumber := map[int]*deprecatedFinding{}
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
			return f.closeOneDeprecated(d, repo, byNumber[n], v, pos, total, throttle, interactive)
		})
	p.Noun = "PRs that change removed/deprecated things"
	p.GateLabel = "moot"
	p.ConfirmAll = fmt.Sprintf("comment and close up to <yellow>%d</> PRs as moot in %s?", len(findings), f.RepoTag())
	p.ConfirmAI = fmt.Sprintf("comment and close PRs the AI scores ≥ <green>%.2f</> (up to <yellow>%d</> candidates) in %s?", p.Threshold, len(findings), f.RepoTag())

	if !withAI {
		return p.ApplyAll(numbers)
	}
	return p.ApplyAI(len(findings), func(onReady func() (bool, error), onBatch func([]pr.Judged) (bool, error)) error {
		items, jerr := f.deprecatedJudgeItems(d, findings)
		if jerr != nil {
			return jerr
		}
		promptText, jerr := f.PreparePrompt(promptDeprecated)
		if jerr != nil {
			return jerr
		}
		_, jerr = f.JudgeBlocks(d, passDeprecated, promptText, items, onReady, onBatch)
		return jerr
	})
}

// closeOneDeprecated handles one candidate: card, the deprecated-close comment
// (citing the strongest match and its successor), and the close.
func (f *Flags) closeOneDeprecated(d *db.DB, repo gh.Repo, fdg *deprecatedFinding, v *pr.Verdict, pos, total int, throttle func(), ask bool) (int, error) {
	f.printDeprecatedCard(fdg, pos, total, v)

	comment, err := f.renderDeprecatedComment(fdg)
	if err != nil {
		return pr.ApplyFailed, err
	}

	best := fdg.matches[0].removal
	what := best.Resource
	if best.Kind == pr.RemovalKindProperty {
		what = best.Property + " on " + best.Resource
	}

	return f.doClose(d, repo, closeReq{
		p: fdg.pr, class: fdg.class, reason: reasonDeprecated, template: templateDeprecatedClose,
		comment: comment, source: passDeprecated,
		evidence: map[string]string{"what": what, "action": best.Action, "source": best.Source, "successor": best.Successor},
		question: fmt.Sprintf("close <cyan>#%d</> as moot?", fdg.pr.Number),
	}, v, throttle, ask)
}

// renderDeprecatedComment renders the close comment citing the strongest match.
func (f *Flags) renderDeprecatedComment(fdg *deprecatedFinding) (string, error) {
	tt, err := assets.CommentTemplate(templateDeprecatedClose)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New(templateDeprecatedClose).Parse(tt)
	if err != nil {
		return "", fmt.Errorf("parsing template %s: %w", templateDeprecatedClose, err)
	}
	r := fdg.matches[0].removal
	data := struct {
		IsProperty   bool
		Name         string
		OnResource   string
		Removed      bool
		Major        int
		Successor    string
		SuccessorMD  string // successors backticked and or-joined for markdown
		SourceURL    string // where the removal/deprecation is documented
		SourceLabel  string // e.g. "v5.0 upgrade guide" | "v3.74.0 changelog"
		CurrentMajor int
	}{
		IsProperty: r.Kind == pr.RemovalKindProperty, Name: r.Resource, OnResource: "",
		Removed: r.Action == pr.RemovalRemoved, Major: r.Major, Successor: r.Successor,
		SourceURL: f.removalURL(r), CurrentMajor: f.currentMajor,
	}
	switch {
	case strings.HasPrefix(r.Source, "changelog "):
		data.SourceLabel = strings.TrimPrefix(r.Source, "changelog ") + " changelog"
	case r.Source == "source DeprecationMessage":
		data.SourceLabel = ""
	default:
		data.SourceLabel = fmt.Sprintf("v%d.0 upgrade guide", r.Major)
	}
	if data.IsProperty {
		data.Name, data.OnResource = r.Property, r.Resource
	}
	// a removed resource is often superseded by several ("azurerm_linux_web_app
	// or azurerm_windows_web_app") — render them backticked and or-joined
	if r.Successor != "" {
		parts := strings.Split(r.Successor, ", ")
		for i := range parts {
			parts[i] = "`" + parts[i] + "`"
		}
		data.SuccessorMD = strings.Join(parts, " or ")
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("rendering template %s: %w", templateDeprecatedClose, err)
	}
	return strings.TrimSpace(b.String()), nil
}

// deprecatedJudgeItems renders one judge block per finding: the PR's substance
// (body, files, thread digest) and every removed/deprecated thing it leans on,
// so the AI can tell a moot change from an incidental mention.
func (*Flags) deprecatedJudgeItems(d *db.DB, findings []deprecatedFinding) ([]pr.JudgeItem, error) {
	items := make([]pr.JudgeItem, 0, len(findings))
	for i := range findings {
		fdg := &findings[i]
		var b strings.Builder
		writePRBlockHeader(&b, fdg.pr, fdg.class)

		b.WriteString("REMOVED/DEPRECATED THINGS THE PR CHANGES OR REFERENCES:\n")
		for _, m := range fdg.matches {
			r := m.removal
			what := fmt.Sprintf("%s `%s`", strings.ReplaceAll(r.Kind, "-", " "), r.Resource)
			if r.Kind == pr.RemovalKindProperty {
				what = fmt.Sprintf("property `%s` on `%s`", r.Property, r.Resource)
			}
			line := fmt.Sprintf("- %s: %s (%s)", what, r.Action, r.Source)
			if r.Successor != "" {
				line += ", use `" + r.Successor + "` instead"
			}
			if m.absent {
				line += " — the name no longer appears anywhere in the provider source"
			}
			fmt.Fprintf(&b, "%s\n", line)
			if r.Note != "" {
				fmt.Fprintf(&b, "  NOTE: %s\n", r.Note)
			}
			switch {
			case m.file != "":
				fmt.Fprintf(&b, "  CHANGED LINE IN THE PR'S DIFF OF %s: %s\n", m.file, m.quote)
			case m.quote != "":
				fmt.Fprintf(&b, "  MATCHED PR LINE (title/body): %s\n", m.quote)
			}
		}
		if len(fdg.alive) > 0 {
			fmt.Fprintf(&b, "RESOURCES THE PR ALSO TOUCHES THAT ARE NOT REMOVED OR DEPRECATED: %s\n", strings.Join(fdg.alive, ", "))
		}

		if err := writeThreadDigest(&b, d, fdg.pr.Number); err != nil {
			return nil, err
		}
		items = append(items, pr.JudgeItem{Number: fdg.pr.Number, Block: b.String()})
	}
	return items, nil
}

// removalURL deep-links a removal to where it is documented: the registry's
// rendered upgrade guide (anchored on the resource heading) for guide-sourced
// rows, the github release for changelog-sourced ones, nothing for
// source-marker rows. Best effort — anchors follow the registry's heading-id
// scheme.
func (f *Flags) removalURL(r pr.Removal) string {
	if v, ok := strings.CutPrefix(r.Source, "changelog "); ok {
		return fmt.Sprintf("https://github.com/%s/releases/tag/%s", f.GH.Repo, v)
	}
	if r.Major == 0 {
		return ""
	}
	return fmt.Sprintf("https://registry.terraform.io/providers/hashicorp/azurerm/latest/docs/guides/%d.0-upgrade-guide#%s", r.Major, r.Resource)
}

// matchLine returns the first line of t the matcher hits, for the card quote.
func matchLine(t string, re *regexp.Regexp) string {
	for line := range strings.SplitSeq(t, "\n") {
		if re.MatchString(line) {
			return text.TruncateRunes(text.OneLine(strings.TrimSpace(line)), 90)
		}
	}
	return ""
}

// diffLine is one changed line of a PR's diff with the file it came from,
// prefixed "+ " or "- " so the quote says which way the change went.
type diffLine struct {
	file, text string
}

// matchDiff returns the first changed line the matcher hits, for the card
// quote and the judge, truncated the same way as prose quotes.
func matchDiff(lines []diffLine, re *regexp.Regexp) (diffLine, bool) {
	for _, l := range lines {
		if re.MatchString(l.text) {
			return diffLine{file: l.file, text: text.TruncateRunes(text.OneLine(strings.TrimSpace(l.text)), 110)}, true
		}
	}
	return diffLine{}, false
}

// printDeprecatedCard is one PR with every removed/deprecated thing it leans
// on, successors included, and the AI's moot score when judged.
func (f *Flags) printDeprecatedCard(fdg *deprecatedFinding, pos, total int, v *pr.Verdict) {
	printCardHeader(fdg.pr, pos, total)
	cout.Printf("      %s\n", authorLine(fdg.pr))
	shown := 0
	for _, m := range fdg.matches {
		if shown == 6 {
			cout.Printf("      <gray>… and %d more</>\n", len(fdg.matches)-shown)
			break
		}
		shown++
		r := m.removal
		actionTag := cli.TagYellow
		if r.Action == pr.RemovalRemoved {
			actionTag = cli.TagRed
		}
		var b strings.Builder
		if r.Kind == pr.RemovalKindProperty {
			fmt.Fprintf(&b, "<lightCyan>%s</> <gray>on</> %s", r.Property, r.Resource)
		} else {
			kind := strings.ReplaceAll(r.Kind, "-", " ")
			fmt.Fprintf(&b, "<lightCyan>%s</> <gray>(%s)</>", r.Resource, kind)
		}
		switch {
		case strings.HasPrefix(r.Source, "changelog "):
			fmt.Fprintf(&b, " <%s>%s</> <gray>in</> <lightMagenta>%s</>", actionTag, r.Action, strings.TrimPrefix(r.Source, "changelog "))
		case r.Major > 0:
			fmt.Fprintf(&b, " <%s>%s</> <gray>in</> <lightMagenta>v%d.0</> <gray>(%s)</>", actionTag, r.Action, r.Major, r.Source)
		default:
			fmt.Fprintf(&b, " <%s>%s</> <gray>(%s)</>", actionTag, r.Action, r.Source)
		}
		if r.Successor != "" {
			fmt.Fprintf(&b, " <gray>· use</> <cyan>%s</>", r.Successor)
		}
		if m.absent {
			fmt.Fprint(&b, " <gray>· absent from source</>")
		}
		if url := f.removalURL(r); url != "" {
			fmt.Fprintf(&b, " <darkGray>%s</>", url)
		}
		cout.Printf("      %s\n", b.String())
		switch {
		case m.file != "":
			cout.Printf("        <gray>in the diff of %s:</> %s\n", filepath.Base(m.file), m.quote)
		case m.quote != "":
			cout.Printf("        <gray>matched:</> %s\n", m.quote)
		}
	}
	if len(fdg.alive) > 0 {
		cout.Printf("      <gray>also touches (not removed or deprecated):</> <green>%s</>\n",
			strings.Join(fdg.alive, "</> <gray>·</> <green>"))
	}
	cli.PrintVerdict(v)
}
