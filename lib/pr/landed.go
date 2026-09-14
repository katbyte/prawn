package pr

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/katbyte/prawn/lib/db"
)

// Landed detection: does the provider already contain what the PR adds? The
// PR's diff names the identifiers it introduces and the ones it deletes —
// schema properties, doc properties. If what it adds was absent from the file
// when the PR was opened and is in a local checkout (--src-dir) at HEAD, or
// what it deletes was there then and is gone now, somebody else made the
// change (a rename lands as "the old name is gone" even when the new name
// differs); git log -S names the commit — the landing. The judge then reads
// the substance.

const (
	// landedMinShare is the share of a PR's new identifiers that must have
	// landed (arrived after it opened), or of its deleted identifiers that
	// must be gone, for the PR to count as landed — identifiers still missing
	// from, or still in, the source are the PR's undelivered part.
	landedMinShare = 0.75
	// landedMinDeletions is how many (identifier, file) deletions must be gone
	// for deletions alone to make a landing — one bare removal is as likely a
	// cleanup by someone else as the PR's change.
	landedMinDeletions = 2
	// landedMaxAttribute caps the git log -S calls per PR.
	landedMaxAttribute = 6
	// landedMinIdentLen: a token without an underscore must be this long to
	// count — bare words ("sku", "enabled", "dashboards") are everywhere.
	landedMinIdentLen = 12
)

// Commit is one merged commit: identity, and the PR it came from when the
// squash subject names one.
type Commit struct {
	Hash    string
	Date    time.Time
	Author  string
	Subject string
	PR      int // from the "(#N)" squash-merge suffix; 0 when absent
}

// Landing is the evidence that a PR's change already exists in the provider.
type Landing struct {
	Commit    *Commit  // the earliest commit that landed one of the identifiers
	Landed    []string // the PR's new identifiers now in the source, arrived after the PR opened (distinct)
	Missing   []string // the PR's new identifiers absent from the source (distinct)
	Gone      []string // the identifiers the PR deletes that are gone from the source since (distinct)
	Still     []string // the identifiers the PR deletes that the source still holds (distinct)
	ShippedIn string   // the first release tag after the commit ("" = not yet released)
}

// History is a provider checkout: where to read files and history, plus its
// release tags for dating what shipped when.
type History struct {
	SrcDir string
	tags   []releaseTag // by date, ascending

	// injectable for tests: the file at HEAD; the file as of the last commit
	// before a date (empty when absent); the commits since a date that
	// changed how often an identifier appears in a file
	readFile  func(path string) ([]byte, error)
	baseFile  func(before time.Time, path string) ([]byte, error)
	attribute func(since time.Time, ident, file string) ([]Commit, error)
}

type releaseTag struct {
	name string
	date time.Time
}

var (
	reSubjectPR  = regexp.MustCompile(`\(#(\d+)\)\s*$`)
	reReleaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	// identifiers a diff line introduces: Go string literals and markdown
	// backticks holding a snake-case token
	reGoIdent = regexp.MustCompile(`"([a-z][a-z0-9_]{2,})"`)
	reMDIdent = regexp.MustCompile("`([a-z][a-z0-9_]{2,})`")

	// tokens every resource carries — their presence proves nothing
	landedStopIdents = map[string]bool{
		"resource_group_name": true, "resource_group_id": true, "subscription_id": true, "tenant_id": true,
		"client_id": true, "principal_id": true, "object_id": true, "location": true, "identity": true,
		"description": true, "display_name": true, "user_assigned": true, "system_assigned": true,
		"identity_ids": true, "required": true, "optional": true, "computed": true, "default": true,
		"provider": true, "resource": true, "terraform": true, "azurerm": true, "example": true,
	}
)

// LoadHistory opens the checkout and reads its release tags.
func LoadHistory(srcDir string) (*History, error) {
	h := &History{
		SrcDir:   srcDir,
		readFile: func(path string) ([]byte, error) { return os.ReadFile(filepath.Join(srcDir, path)) }, //nolint:gosec // G304: under the user's --src-dir
	}
	h.baseFile = h.gitBaseFile
	h.attribute = h.gitAttribute

	out, err := exec.CommandContext(context.Background(), "git", "-C", srcDir, "for-each-ref", "--sort=creatordate", //nolint:gosec // G204: srcDir is the user's --src-dir
		"--format=%(refname:short) %(creatordate:iso-strict)", "refs/tags").Output()
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref in %s: %w", srcDir, err)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		name, date, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || !reReleaseTag.MatchString(name) {
			continue
		}
		if t, terr := time.Parse(time.RFC3339, date); terr == nil {
			h.tags = append(h.tags, releaseTag{name: name, date: t})
		}
	}
	return h, nil
}

// gitBaseFile reads the file as it was at the last commit before the date —
// the provider as the PR author saw it. A file that did not exist reads as
// empty: everything the PR adds there is new.
func (h *History) gitBaseFile(before time.Time, path string) ([]byte, error) {
	rev, err := exec.CommandContext(context.Background(), "git", "-C", h.SrcDir, "rev-list", "-1", //nolint:gosec // G204: srcDir is the user's --src-dir
		"--before="+before.Format(time.RFC3339), "HEAD").Output()
	if err != nil {
		return nil, fmt.Errorf("git rev-list in %s: %w", h.SrcDir, err)
	}
	hash := strings.TrimSpace(string(rev))
	if hash == "" {
		return nil, nil
	}
	out, err := exec.CommandContext(context.Background(), "git", "-C", h.SrcDir, "show", hash+":"+path).Output() //nolint:gosec // G204: srcDir is the user's --src-dir
	if err != nil {
		return nil, nil //nolint:nilerr // the path did not exist at that commit: an empty base is the answer, not an error
	}
	return out, nil
}

// gitAttribute lists the commits since the date that changed how often the
// identifier appears in the file — for a token the PR introduces, the first
// is whoever introduced it instead.
func (h *History) gitAttribute(since time.Time, ident, file string) ([]Commit, error) {
	out, err := exec.CommandContext(context.Background(), "git", "-C", h.SrcDir, "log", "--since="+since.Format(time.RFC3339), //nolint:gosec // G204: srcDir is the user's --src-dir
		"--reverse", "--date=iso-strict", "--format=%H%x1f%ad%x1f%an%x1f%s", "-S"+ident, "--", file).Output()
	if err != nil {
		return nil, fmt.Errorf("git log -S in %s: %w", h.SrcDir, err)
	}
	var commits []Commit
	for line := range strings.SplitSeq(string(out), "\n") {
		parts := strings.Split(strings.TrimSpace(line), "\x1f")
		if len(parts) != 4 {
			continue
		}
		c := Commit{Hash: parts[0], Author: parts[2], Subject: parts[3]}
		if t, terr := time.Parse(time.RFC3339, parts[1]); terr == nil {
			c.Date = t
		}
		if m := reSubjectPR.FindStringSubmatch(c.Subject); m != nil {
			c.PR, _ = strconv.Atoi(m[1])
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// ShippedIn names the first release tagged after the date, "" when none has.
func (h *History) ShippedIn(t time.Time) string {
	for _, tag := range h.tags {
		if !tag.date.Before(t) {
			return tag.name
		}
	}
	return ""
}

// ident is one identifier in one file of a PR's diff.
type ident struct{ token, file string }

func (id ident) tok() string { return id.token }

// Landing reports whether the provider already holds the PR's change: the
// identifiers its diff adds are in the source at HEAD and were absent when
// the PR opened, or the identifiers it deletes were there then and are gone
// now. nil when the diff names no identifiers, or too few have landed.
func (h *History) Landing(p *db.PR, diff []FileDiff) (*Landing, error) {
	var adds, dels []ident
	seenAdd, seenDel := map[ident]bool{}, map[ident]bool{}
	for _, fd := range diff {
		if !landedFile(fd.Path) {
			continue
		}
		re := reGoIdent
		if strings.HasSuffix(fd.Path, ".markdown") {
			re = reMDIdent
		}
		inAdded, inRemoved := map[string]bool{}, map[string]bool{}
		for _, l := range fd.Added {
			for _, m := range re.FindAllStringSubmatch(l, -1) {
				inAdded[m[1]] = true
			}
		}
		for _, l := range fd.Removed {
			for _, m := range re.FindAllStringSubmatch(l, -1) {
				inRemoved[m[1]] = true
			}
		}
		// a token on both sides is moved or reworded, not introduced or deleted
		for tok := range inAdded {
			id := ident{token: tok, file: fd.Path}
			if !inRemoved[tok] && !seenAdd[id] && landedIdent(tok) {
				seenAdd[id] = true
				adds = append(adds, id)
			}
		}
		for tok := range inRemoved {
			id := ident{token: tok, file: fd.Path}
			if !inAdded[tok] && !seenDel[id] && landedIdent(tok) {
				seenDel[id] = true
				dels = append(dels, id)
			}
		}
	}
	slices.SortFunc(adds, func(a, b ident) int { return strings.Compare(a.file+a.token, b.file+b.token) })
	slices.SortFunc(dels, func(a, b ident) int { return strings.Compare(a.file+a.token, b.file+b.token) })
	if len(adds) == 0 && len(dels) == 0 {
		return nil, nil
	}

	// gate one, cheap: which added identifiers are at HEAD, which deleted
	// ones are gone? Files gone or renamed at HEAD read as empty.
	heads := map[string][]byte{}
	head := func(file string) []byte {
		if b, ok := heads[file]; ok {
			return b
		}
		b, err := h.readFile(file)
		if err != nil {
			b = nil
		}
		heads[file] = b
		return b
	}
	var present, missing, gone, still []ident
	for _, id := range adds {
		if strings.Contains(string(head(id.file)), quoteFor(id.file, id.token)) {
			present = append(present, id)
		} else {
			missing = append(missing, id)
		}
	}
	for _, id := range dels {
		if strings.Contains(string(head(id.file)), quoteFor(id.file, id.token)) {
			still = append(still, id)
		} else {
			gone = append(gone, id)
		}
	}
	addsOK := len(present) > 0 && float64(len(present)) >= landedMinShare*float64(len(adds))
	delsOK := len(gone) > 0 && float64(len(gone)) >= landedMinShare*float64(len(dels))
	if !addsOK && !delsOK {
		return nil, nil
	}

	// gate two: against the file as the PR author saw it. An added token
	// that was already there is neutral (the PR reused it); a deleted token
	// that was not there is neutral too (the PR was already behind).
	bases := map[string][]byte{}
	base := func(file string) ([]byte, error) {
		if b, ok := bases[file]; ok {
			return b, nil
		}
		b, err := h.baseFile(p.CreatedAt, file)
		if err != nil {
			return nil, err
		}
		bases[file] = b
		return b, nil
	}
	l := &Landing{}
	var landed, missingNew, goneReal, stillReal []ident
	for _, id := range present {
		b, err := base(id.file)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(string(b), quoteFor(id.file, id.token)) {
			landed = append(landed, id)
		}
	}
	for _, id := range missing {
		b, err := base(id.file)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(string(b), quoteFor(id.file, id.token)) {
			missingNew = append(missingNew, id)
		}
	}
	for _, id := range gone {
		b, err := base(id.file)
		if err != nil {
			return nil, err
		}
		if strings.Contains(string(b), quoteFor(id.file, id.token)) {
			goneReal = append(goneReal, id)
		}
	}
	for _, id := range still {
		b, err := base(id.file)
		if err != nil {
			return nil, err
		}
		if strings.Contains(string(b), quoteFor(id.file, id.token)) {
			stillReal = append(stillReal, id)
		}
	}
	addsOK = len(landed) > 0 && float64(len(landed)) >= landedMinShare*float64(len(landed)+len(missingNew))
	delsOK = len(goneReal) >= landedMinDeletions && float64(len(goneReal)) >= landedMinShare*float64(len(goneReal)+len(stillReal))
	if !addsOK && !delsOK {
		return nil, nil
	}
	l.Landed, l.Missing, l.Gone, l.Still = tokensOf(landed), tokensOf(missingNew), tokensOf(goneReal), tokensOf(stillReal)

	// who did it: the earliest commit since the PR opened that brought a
	// landed identifier in, or took a gone one out
	attributed := 0
	for _, id := range append(slices.Clone(landed), goneReal...) {
		if attributed == landedMaxAttribute {
			break
		}
		attributed++
		commits, err := h.attribute(p.CreatedAt, quoteFor(id.file, id.token), id.file)
		if err != nil {
			return nil, err
		}
		commits = slices.DeleteFunc(commits, func(c Commit) bool { return c.PR == p.Number || Bot(c.Author) })
		if len(commits) > 0 && (l.Commit == nil || commits[0].Date.Before(l.Commit.Date)) {
			l.Commit = new(commits[0])
		}
	}
	if l.Commit == nil {
		return nil, nil
	}
	l.ShippedIn = h.ShippedIn(l.Commit.Date)
	return l, nil
}

// tokensOf lists the distinct tokens of the idents, first-seen order.
func tokensOf[T interface{ tok() string }](ids []T) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		if t := id.tok(); !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// landedFile is a file whose added identifiers mean something: resource and
// data source code and their docs. Tests, the changelog, vendor, CI are not.
func landedFile(path string) bool {
	switch {
	case strings.HasSuffix(path, "_test.go"), strings.HasPrefix(path, "vendor/"), strings.HasPrefix(path, ".github/"):
		return false
	case strings.HasPrefix(path, "internal/") && strings.HasSuffix(path, ".go"):
		return true
	case strings.HasPrefix(path, "website/docs/") && strings.HasSuffix(path, ".markdown"):
		return true
	}
	return false
}

// landedIdent keeps the tokens distinctive enough to prove anything.
func landedIdent(tok string) bool {
	if landedStopIdents[tok] {
		return false
	}
	return strings.Contains(tok, "_") || len(tok) >= landedMinIdentLen
}

// quoteFor renders a token the way its file spells it: a Go string literal or
// a markdown backtick.
func quoteFor(file, tok string) string {
	if strings.HasSuffix(file, ".markdown") {
		return "`" + tok + "`"
	}
	return `"` + tok + `"`
}
