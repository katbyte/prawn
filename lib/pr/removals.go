package pr

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/katbyte/prawn/lib/text"
)

// Removals inventory: every resource, data source, and property the provider
// has removed or formally deprecated, read from a local provider checkout
// (--src-dir) — the upgrade guides and changelog DEPRECATIONS sections name
// what went and what replaced it, and DeprecationMessage markers in the source
// catch deprecations announced in code before any guide mentions them. prawn
// close deprecated scans open PRs against this inventory. Ported from koi's
// issue-side inventory (which fetches the same guides via the API).

// Removal actions.
const (
	RemovalRemoved    = "removed"
	RemovalDeprecated = "deprecated"

	// Removal kinds: what the removed/deprecated thing is.
	RemovalKindResource   = "resource"
	RemovalKindDataSource = "data-source"
	RemovalKindProperty   = "property"
)

// Removal is one removed or deprecated resource, data source, or property.
type Removal struct {
	Kind      string // resource | data-source | property
	Resource  string // the azurerm_* name it belongs to
	Property  string // "" for resource-level rows
	Action    string // removed | deprecated
	Major     int    // major that removed it (deprecations: major it was announced in; 0 = source-only)
	Successor string // what to use instead ("" = unknown)
	Note      string // the guide/changelog/source sentence, truncated
	Source    string // e.g. "4.0 upgrade guide" | "changelog v3.109.0" | "source DeprecationMessage"
}

// Upgrade-guide parsing: the 4.0/5.0 guides share a structure — "## Removed
// Resources" and "## Removed Data Sources" hold one "### `azurerm_x`" per
// removed item with a body sentence naming the successor, and the per-resource
// breaking-changes sections ("## Breaking Changes in Resources", "## Behaviour
// changes and removed properties in Resources", + Data Sources variants) hold
// bullets like "The deprecated `x` property has been removed in favour of the
// `y` property." The 3.0 guide predates this format and is not parsed.

var (
	reGuideItem  = regexp.MustCompile("^###\\s+`([a-z0-9_.]+)`")
	reBacktick   = regexp.MustCompile("`([a-zA-Z0-9_.]+)`")
	reProperty   = regexp.MustCompile(`^[a-z][a-z0-9_.]*$`)
	reGuideFile  = regexp.MustCompile(`^(\d+)\.0-upgrade-guide`)
	reVersion    = regexp.MustCompile(`^##\s+(\d+)\.(\d+)\.(\d+)`)
	reResourceID = regexp.MustCompile(`"(azurerm_[a-z0-9_]+)"`)
)

// propertyToken reports whether a backticked token looks like a schema
// property or block name (lowercase snake/dotted) rather than a value like
// `All` or a resource name.
func propertyToken(t string) bool {
	if strings.HasPrefix(t, "azurerm_") || strings.HasPrefix(t, "data.azurerm_") {
		return false
	}
	return reProperty.MatchString(t)
}

// successorIn returns the first backticked token in s that differs from self —
// guide and changelog sentences name the replacement right after the removal.
func successorIn(s, self string) string {
	for _, m := range reBacktick.FindAllStringSubmatch(s, -1) {
		if m[1] != self {
			return m[1]
		}
	}
	return ""
}

// successorsIn returns every backticked azurerm_* token in s that differs from
// self, comma-joined — a removed resource is often superseded by several (e.g.
// azurerm_app_service by both the linux and windows web app resources).
func successorsIn(s, self string) string {
	var out []string
	seen := map[string]bool{}
	for _, m := range reBacktick.FindAllStringSubmatch(s, -1) {
		t := m[1]
		if t == self || seen[t] || !strings.HasPrefix(strings.TrimPrefix(t, "data."), "azurerm_") {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return strings.Join(out, ", ")
}

const (
	guideModeOff = iota
	guideModeResources
	guideModeDataSources
	guideModePropsResources
	guideModePropsDataSources
)

// ParseUpgradeGuide extracts every removed resource, data source, and property
// from one major-version upgrade guide.
func ParseUpgradeGuide(content string, major int) []Removal {
	source := fmt.Sprintf("%d.0 upgrade guide", major)
	mode := guideModeOff
	cur := "" // current resource inside a per-resource breaking-changes section
	var out []Removal
	var pending *Removal // removed resource/data source awaiting its note line

	flush := func() {
		if pending != nil {
			out = append(out, *pending)
			pending = nil
		}
	}

	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "## ") {
			flush()
			h := strings.ToLower(trimmed)
			switch {
			case strings.Contains(h, "removed resources"):
				mode = guideModeResources
			case strings.Contains(h, "removed data sources"):
				mode = guideModeDataSources
			case strings.Contains(h, "in data sources"):
				mode = guideModePropsDataSources
			case strings.Contains(h, "in resources"):
				mode = guideModePropsResources
			default:
				mode = guideModeOff
			}
			cur = ""
			continue
		}

		if m := reGuideItem.FindStringSubmatch(trimmed); m != nil {
			flush()
			switch mode {
			case guideModeResources, guideModeDataSources:
				kind := RemovalKindResource
				if mode == guideModeDataSources {
					kind = RemovalKindDataSource
				}
				pending = &Removal{Kind: kind, Resource: m[1], Action: RemovalRemoved, Major: major, Source: source}
			case guideModePropsResources, guideModePropsDataSources:
				cur = m[1]
			}
			continue
		}

		// the first body line under a removed item carries the note + successors
		if pending != nil && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			pending.Note = text.TruncateRunes(text.OneLine(trimmed), 240)
			pending.Successor = successorsIn(trimmed, pending.Resource)
			flush()
			continue
		}

		// property bullets: only "been removed" bullets are removals — the rest
		// of the breaking-changes bullets are default/behaviour changes
		isBullet := strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "- ")
		if cur != "" && isBullet && strings.Contains(trimmed, "been removed") {
			before, after, _ := strings.Cut(trimmed, "been removed")
			succ := successorIn(after, "")
			for _, m := range reBacktick.FindAllStringSubmatch(before, -1) {
				if !propertyToken(m[1]) {
					continue
				}
				out = append(out, Removal{
					Kind: RemovalKindProperty, Resource: cur, Property: m[1],
					Action: RemovalRemoved, Major: major, Successor: succ,
					Note: text.TruncateRunes(text.OneLine(strings.TrimLeft(trimmed, "*- ")), 240), Source: source,
				})
			}
		}
	}
	flush()
	return out
}

// ParseChangelogDeprecations turns a changelog's DEPRECATIONS bullets into
// removal rows with action "deprecated" — announced but possibly not yet
// removed. A bullet deprecates the whole resource ("deprecated since the
// service is retiring") or individual properties ("`x` has been superseded by
// `y`").
func ParseChangelogDeprecations(content string) []Removal {
	var out []Removal
	version, major := "", 0
	inDeprecations := false

	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)

		if m := reVersion.FindStringSubmatch(trimmed); m != nil {
			version = strings.TrimSuffix(strings.TrimPrefix(trimmed, "## "), " (Unreleased)")
			if i := strings.IndexByte(version, ' '); i > 0 {
				version = version[:i]
			}
			major, _ = strconv.Atoi(m[1])
			inDeprecations = false
			continue
		}
		if strings.HasSuffix(trimmed, ":") && strings.ToUpper(trimmed) == trimmed {
			inDeprecations = trimmed == "DEPRECATIONS:"
			continue
		}
		if !inDeprecations || version == "" {
			continue
		}
		isBullet := strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "- ")
		if !isBullet {
			continue
		}
		bullet := strings.TrimSpace(strings.TrimLeft(trimmed, "*- "))

		// the bullet's subject: its first backticked azurerm_* token
		resource := ""
		for _, m := range reBacktick.FindAllStringSubmatch(bullet, -1) {
			if strings.HasPrefix(strings.TrimPrefix(m[1], "data."), "azurerm_") {
				resource = strings.TrimPrefix(m[1], "data.")
				break
			}
		}
		if resource == "" {
			continue
		}
		out = append(out, mineDeprecationBullet(bullet, resource, version, major)...)
	}
	return out
}

// mineDeprecationBullet applies koi's DEPRECATIONS phrasing heuristics to one
// bullet: a whole-resource deprecation, or property tokens named before
// "superseded by"/"in favour of" (or after "deprecate" when no successor
// phrasing exists).
func mineDeprecationBullet(bullet, resource, version string, major int) []Removal {
	var out []Removal
	source := "changelog v" + version
	lower := strings.ToLower(bullet)
	kind := RemovalKindResource
	if strings.HasPrefix(bullet, "Data Source") {
		kind = RemovalKindDataSource
	}

	// whole resource: "`x` - deprecated since ..." / "... is retiring"
	rest, _ := strings.CutPrefix(lower, "data source: ")
	if strings.HasPrefix(rest, "`"+resource+"` - deprecated") || strings.Contains(lower, "retiring") {
		return append(out, Removal{
			Kind: kind, Resource: resource, Action: RemovalDeprecated, Major: major,
			Successor: successorsIn(bullet, resource),
			Note:      text.TruncateRunes(text.OneLine(bullet), 240), Source: source,
		})
	}

	// properties: tokens before "superseded"/"in favour of" or after "deprecate"
	before := bullet
	succ := ""
	for _, cut := range []string{"superseded by", "in favour of"} {
		if b, a, found := strings.Cut(bullet, cut); found {
			before, succ = b, successorIn(a, "")
			break
		}
	}
	if before == bullet { // no successor phrasing: only trust explicit "deprecate the `x`" wording
		if _, a, found := strings.Cut(lower, "deprecate"); found {
			before = a[:min(len(a), 120)]
		} else {
			return out
		}
	}
	for _, m := range reBacktick.FindAllStringSubmatch(before, -1) {
		if !propertyToken(m[1]) {
			continue
		}
		out = append(out, Removal{
			Kind: RemovalKindProperty, Resource: resource, Property: m[1],
			Action: RemovalDeprecated, Major: major, Successor: succ,
			Note: text.TruncateRunes(text.OneLine(bullet), 240), Source: source,
		})
	}
	return out
}

// Inventory is the loaded removals plus what the source tree still registers,
// so matches can be corroborated: a "removed" resource still present in the
// source (re-added, or a guide-parse miss) must not close PRs.
type Inventory struct {
	Removals []Removal
	// Live is every azurerm_* name string-referenced under internal/services —
	// a superset of the registered resources, which errs the safe way: a name
	// that appears nowhere at all is definitely gone.
	Live map[string]bool
	// CurrentMajor is the newest major with a parsed upgrade guide.
	CurrentMajor int
}

// LoadInventory builds the removals inventory from a local provider checkout:
// every parseable major-version upgrade guide (4.0+), the changelogs'
// DEPRECATIONS sections, DeprecationMessage markers in the source, and the
// source's live-name set.
func LoadInventory(srcDir string) (*Inventory, error) {
	inv := &Inventory{Live: map[string]bool{}}

	guides, err := filepath.Glob(filepath.Join(srcDir, "website", "docs", "guides", "*-upgrade-guide*"))
	if err != nil {
		return nil, err
	}
	for _, g := range guides {
		m := reGuideFile.FindStringSubmatch(filepath.Base(g))
		if m == nil {
			continue
		}
		major, _ := strconv.Atoi(m[1])
		if major < 4 { // the 3.0 guide predates the parseable format
			continue
		}
		content, rerr := os.ReadFile(g) //nolint:gosec // G304: path comes from a glob under the user-supplied --src-dir
		if rerr != nil {
			return nil, fmt.Errorf("reading upgrade guide %s: %w", g, rerr)
		}
		inv.Removals = append(inv.Removals, ParseUpgradeGuide(string(content), major)...)
		if major > inv.CurrentMajor {
			inv.CurrentMajor = major
		}
	}
	if len(inv.Removals) == 0 {
		return nil, fmt.Errorf("no removals parsed from upgrade guides under %s/website/docs/guides — is --src-dir a provider checkout?", srcDir)
	}

	logs, err := filepath.Glob(filepath.Join(srcDir, "CHANGELOG*.md"))
	if err != nil {
		return nil, err
	}
	for _, l := range logs {
		content, rerr := os.ReadFile(l) //nolint:gosec // G304: path comes from a glob under the user-supplied --src-dir
		if rerr != nil {
			return nil, fmt.Errorf("reading changelog %s: %w", l, rerr)
		}
		inv.Removals = append(inv.Removals, ParseChangelogDeprecations(string(content))...)
	}

	if err := inv.scanSource(srcDir); err != nil {
		return nil, err
	}
	return inv, nil
}

// scanSource walks internal/services once for both source signals: the live
// azurerm_* name set, and DeprecationMessage markers — deprecations announced
// in code before any guide or changelog bullet mentions them.
func (inv *Inventory) scanSource(srcDir string) error {
	root := filepath.Join(srcDir, "internal", "services")
	if _, err := os.Stat(root); err != nil {
		return fmt.Errorf("no internal/services under %s — is --src-dir a provider checkout?", srcDir)
	}
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		content, rerr := os.ReadFile(path) //nolint:gosec // G304: path comes from walking the user-supplied --src-dir
		if rerr != nil {
			return rerr
		}
		names := reResourceID.FindAllStringSubmatch(string(content), -1)
		for _, m := range names {
			inv.Live[m[1]] = true
		}
		// resource files carrying a deprecation marker: the file's resource is
		// on the way out; its name is the file's first azurerm_* string (the
		// registration/ResourceType constant leads in both typed and untyped
		// resources)
		if strings.HasSuffix(path, "_resource.go") && len(names) > 0 &&
			(strings.Contains(string(content), "DeprecationMessage:") || strings.Contains(string(content), "DeprecatedInFavourOfResource")) {
			inv.Removals = append(inv.Removals, Removal{
				Kind: RemovalKindResource, Resource: names[0][1], Action: RemovalDeprecated,
				Note:   "the resource carries a deprecation notice in the provider source",
				Source: "source DeprecationMessage",
			})
		}
		return nil
	})
}
