package explore

import (
	"cmp"
	"path"
	"regexp"
	"slices"
	"strings"
)

// The three places a provider property shows up in a diff: its key in a
// resource's schema map, its tag on a typed model, and its bullet in the docs.
var (
	propSchemaKey = regexp.MustCompile(`^(\s*)"([a-z][a-z0-9_]*)":\s*(.*?)\s*$`)
	// what follows a schema key: a block opening ({ or &pluginsdk.Schema{), or one
	// call that returns the schema whole (commonschema.Location(), tags.Schema())
	propBlockOpen = regexp.MustCompile(`^(?:&?[\w.]*Schema)?\{$`)
	propOneLine   = regexp.MustCompile(`^(?:commonschema\.\w+|[\w.]*[Ss]chema\w*)\(.*\),?$`)
	propModelTag  = regexp.MustCompile(`tfschema:"([a-z][a-z0-9_]*)"`)
	propDocBullet = regexp.MustCompile("^\\s*[*-]\\s+`([a-z][a-z0-9_]*)`\\s+-\\s")
)

// schemaKey reads a line as a property's key in a schema map. block says the
// property's definition opens on this line and runs to its closing brace; a
// property declared in one call has no block to follow.
func schemaKey(text string) (indent, name string, block, ok bool) {
	m := propSchemaKey.FindStringSubmatch(text)
	if m == nil {
		return "", "", false, false
	}
	switch {
	case propBlockOpen.MatchString(m[3]):
		return m[1], m[2], true, true
	case propOneLine.MatchString(m[3]):
		return m[1], m[2], false, true
	}
	return "", "", false, false
}

// Prop is a schema property and what it is on: the resource or data source (azurerm_x, data.azurerm_x), or the shared file
// its schema sits in when the file is not one resource's own.
type Prop struct {
	On   string `json:"o"`
	Name string `json:"n"`
}

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
	return propNames(PropertiesOn(diff))
}

// propNames is each property's name once, sorted, whatever it is on.
func propNames(props []Prop) []string {
	seen := map[string]bool{}
	for _, p := range props {
		seen[p.Name] = true
	}
	return sortedKeys(seen)
}

// PropertiesOn is Properties with what each one is on, so two PRs changing the same property of the same resource can be told
// apart from two that change a property of the same name on different ones. Sorted, a property once for each thing it is on.
func PropertiesOn(diff string) []Prop {
	seen := map[Prop]bool{}
	kind, on := propSkip, ""
	code := false // an existing resource, data source, or other service file is changed
	// the schema blocks the current line sits inside, innermost last
	type block struct{ indent, name string }
	var open []block

	for line := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			kind, on, open = propFileKind(line), propOn(line), nil
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
				seen[Prop{On: on, Name: m[1]}] = true
			}
		case propSchema:
			if changed {
				code = true
				for _, m := range propModelTag.FindAllStringSubmatch(text, -1) {
					seen[Prop{On: on, Name: m[1]}] = true
				}
			}
			if indent, name, opens, ok := schemaKey(text); ok {
				if opens {
					open = append(open, block{indent: indent, name: name})
				}
				if changed {
					seen[Prop{On: on, Name: name}] = true
				}
				continue
			}
			if n := len(open); n > 0 {
				if changed && strings.TrimSpace(text) != "" {
					seen[Prop{On: on, Name: open[n-1].name}] = true
				}
				// the block's own closing brace, at the key's indent
				if strings.TrimRight(text, " \t") == open[n-1].indent+"}," {
					open = open[:n-1]
				}
			}
		}
	}

	props := make([]Prop, 0, len(seen))
	if !code {
		return props
	}
	for p := range seen {
		props = append(props, p)
	}
	slices.SortFunc(props, func(a, b Prop) int { return cmp.Or(cmp.Compare(a.On, b.On), cmp.Compare(a.Name, b.Name)) })
	return props
}

// the docs' timeouts bullets read like properties and are not
var propTimeouts = map[string]bool{"create": true, "read": true, "update": true, "delete": true}

// where properties are read from in a file
const (
	propSkip   = iota // tests, vendor, everything else
	propSchema        // service code: schema keys and model tags — a schema is as often in a shared helper file as in the resource's own
	propDocs          // the website docs: property bullets
)

// propOn names what a "diff --git" line's file holds properties of: the resource or data source its docs page or its own code file
// is named for, or the file itself when it is shared (a schema helper, a guide).
func propOn(diffLine string) string {
	_, f, ok := strings.Cut(diffLine, " b/")
	if !ok {
		return ""
	}
	base := path.Base(f)
	if name, ok := strings.CutSuffix(base, ".html.markdown"); ok {
		switch {
		case strings.HasPrefix(f, "website/docs/r/"):
			return "azurerm_" + name
		case strings.HasPrefix(f, "website/docs/d/"):
			return "data.azurerm_" + name
		}
	}
	if name, ok := strings.CutSuffix(base, "_resource.go"); ok && strings.HasPrefix(f, "internal/services/") {
		return "azurerm_" + name
	}
	if name, ok := strings.CutSuffix(base, "_data_source.go"); ok && strings.HasPrefix(f, "internal/services/") {
		return "data.azurerm_" + name
	}
	return f
}

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
	}
	return propSchema
}
