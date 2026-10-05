package explore

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// The three places a provider property shows up in a diff: its key in a
// resource's schema map, its tag on a typed model, and its bullet in the docs.
var (
	propSchemaKey = regexp.MustCompile(`^(\s*)"([a-z][a-z0-9_]*)":\s*\{\s*$`)
	propModelTag  = regexp.MustCompile(`tfschema:"([a-z][a-z0-9_]*)"`)
	propDocBullet = regexp.MustCompile("^\\s*[*-]\\s+`([a-z][a-z0-9_]*)`\\s+-\\s")
)

// Properties names the schema properties a PR's diff adds or changes on
// resources and data sources that already exist, sorted. A property counts
// when the diff touches its schema key or anything inside its schema block,
// adds or drops its tag on a typed model, or rewrites its docs bullet. Files
// the PR creates are skipped: a new resource is not a property change. So is
// a diff that touches no existing service code: rewording the docs changes
// no property.
//
// It reads the diff, not the code, so it is a count to sort and group by
// rather than a fact: a behaviour fix that touches no schema or docs line
// names no property, and a block changed far from its key (beyond the
// diff's context) goes unnamed.
func Properties(diff string) []string {
	seen := map[string]bool{}
	kind := propSkip
	code := false // an existing resource, data source, or other service file is changed
	// the schema blocks the current line sits inside, innermost last
	type block struct{ indent, name string }
	var open []block

	for line := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			kind, open = propFileKind(line), nil
			continue
		case strings.HasPrefix(line, "--- /dev/null"):
			kind = propSkip // a file the PR creates
			continue
		case strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "--- "):
			continue
		case strings.HasPrefix(line, "@@"):
			open = nil // a new hunk: which block it starts in is unknown
			continue
		case kind == propSkip || line == "":
			continue
		}
		sign, text := line[0], line[1:]
		changed := sign == '+' || sign == '-'

		switch kind {
		case propDocs:
			if m := propDocBullet.FindStringSubmatch(text); m != nil && changed && !propTimeouts[m[1]] {
				seen[m[1]] = true
			}
		case propSchema, propModel:
			if changed {
				code = true
				for _, m := range propModelTag.FindAllStringSubmatch(text, -1) {
					seen[m[1]] = true
				}
			}
			if kind != propSchema {
				continue
			}
			if m := propSchemaKey.FindStringSubmatch(text); m != nil {
				open = append(open, block{indent: m[1], name: m[2]})
				if changed {
					seen[m[2]] = true
				}
				continue
			}
			if n := len(open); n > 0 {
				if changed && strings.TrimSpace(text) != "" {
					seen[open[n-1].name] = true
				}
				// the block's own closing brace, at the key's indent
				if strings.TrimRight(text, " \t") == open[n-1].indent+"}," {
					open = open[:n-1]
				}
			}
		}
	}

	names := make([]string, 0, len(seen))
	if !code {
		return names
	}
	for n := range seen {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// the docs' timeouts bullets read like properties and are not
var propTimeouts = map[string]bool{"create": true, "read": true, "update": true, "delete": true}

// where properties are read from in a file
const (
	propSkip   = iota // tests, vendor, everything else
	propSchema        // a resource or data source: schema keys and model tags
	propModel         // other service code: model tags
	propDocs          // the website docs: property bullets
)

// propFileKind sorts a "diff --git" line's file by where its properties are read from.
func propFileKind(diffLine string) int {
	_, f, ok := strings.Cut(diffLine, " b/")
	if !ok {
		return propSkip
	}
	base := path.Base(f)
	switch {
	case strings.HasPrefix(f, "website/docs/") && (strings.HasSuffix(base, ".markdown") || strings.HasSuffix(base, ".md")):
		return propDocs
	case !strings.HasPrefix(f, "internal/services/") || !strings.HasSuffix(base, ".go") || strings.HasSuffix(base, "_test.go"):
		return propSkip
	case strings.Contains(base, "resource") || strings.Contains(base, "data_source"):
		return propSchema
	}
	return propModel
}
