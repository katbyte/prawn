package explore

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// The kinds a PR is sorted into: one each, the first that fits, the biggest
// change first — a PR that adds a resource also adds properties, and is a new
// resource. KindOrder is that order, and the order the page lists them in.
const (
	KindResources      = "resources added"
	KindResource       = "1 resource added"
	KindDataSources    = "data sources added"
	KindDataSource     = "data source added"
	KindListResource   = "list resource added"
	KindAPIVersion     = "api version upgrade"
	KindStateMigration = "state migration"
	KindRefactor       = "refactor"
	KindPropRemoved    = "property removed"
	KindDeprecation    = "deprecation"
	KindPropsAddedMany = "5+ properties (added)"
	KindPropsAdded     = "2-4 properties (added)"
	KindPropAdded      = "1 property (added)"
	KindPropsChgMany   = "5+ properties (changed)"
	KindPropsChanged   = "2-4 properties (changed)"
	KindPropChanged    = "1 property (changed)"
	KindTestFix        = "test fix"
	KindDocumentation  = "documentation"
	KindTooling        = "ci / tooling and dependencies"
	KindBugFix         = "bug fix"
	KindOther          = "other"
)

// KindOrder lists the kinds as Kind tries them.
var KindOrder = []string{
	KindResources, KindResource, KindDataSources, KindDataSource, KindListResource,
	KindAPIVersion, KindStateMigration, KindRefactor, KindPropRemoved, KindDeprecation,
	KindPropsAddedMany, KindPropsAdded, KindPropAdded, KindPropsChgMany, KindPropsChanged, KindPropChanged,
	KindTestFix, KindDocumentation, KindTooling, KindBugFix, KindOther,
}

// changeFacts is what a PR's diff says it does, read off the diff alone.
type changeFacts struct {
	added, changed, removed []string // schema properties on resources that already exist
	resources, dataSources  int      // files the PR creates
	listResources           int
	migration               bool // a state migration file is created
	apiVersion              bool // an sdk import moved to another dated api version
	deprecates              bool // a Deprecated: line is added
}

var (
	// an azure sdk import: the service, then the dated api version
	sdkImport = regexp.MustCompile(`go-azure-sdk/resource-manager/([a-z0-9]+)/(\d{4}-\d{2}-\d{2}(?:-preview)?)/`)
	// a title that says the PR reshapes code rather than changes what it does
	refactorTitle = regexp.MustCompile(`(?i)\b(refactor\w*|retype\w*|typed (resource|sdk)|migrat\w+ to)\b`)
	deprecatedSet = regexp.MustCompile(`\bDeprecated:\s`)
)

// facts scans a unified diff once for what Kind needs. Properties are told
// apart by their schema key: a key the diff adds is a property added, one it
// drops is removed, and a line changed inside a key the diff leaves in place
// is that property changed. A key both dropped and added (moved, renamed in
// place) counts as changed.
func facts(diff string) changeFacts {
	var f changeFacts
	added, removed, changed := map[string]bool{}, map[string]bool{}, map[string]bool{}
	sdkAdded, sdkRemoved := map[string]map[string]bool{}, map[string]map[string]bool{}
	file, schema, service := "", false, false
	type block struct{ indent, name string }
	var open []block

	for line := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			_, file, _ = strings.Cut(line, " b/")
			base := path.Base(file)
			service = strings.HasPrefix(file, "internal/") && strings.HasSuffix(base, ".go") && !strings.HasSuffix(base, "_test.go")
			schema = service && propFileKind(line) == propSchema
			open = nil
			continue
		case strings.HasPrefix(line, "--- /dev/null"):
			base := path.Base(file)
			if service && strings.HasPrefix(file, "internal/services/") {
				switch {
				case strings.HasSuffix(base, "_resource_list.go"), strings.HasSuffix(base, "_list_resource.go"):
					f.listResources++
				case strings.HasSuffix(base, "_data_source.go"):
					f.dataSources++
				case strings.HasSuffix(base, "_resource.go"):
					f.resources++
				}
				if strings.Contains(file, "/migration/") {
					f.migration = true
				}
			}
			schema = false // a file the PR creates: its keys are the new resource's, not properties added to one
			continue
		case strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "--- "):
			continue
		case strings.HasPrefix(line, "@@"):
			open = nil
			continue
		case line == "" || !service:
			continue
		}
		sign, text := line[0], line[1:]
		edit := sign == '+' || sign == '-'
		if edit {
			for _, m := range sdkImport.FindAllStringSubmatch(text, -1) {
				side := sdkAdded
				if sign == '-' {
					side = sdkRemoved
				}
				if side[m[1]] == nil {
					side[m[1]] = map[string]bool{}
				}
				side[m[1]][m[2]] = true
			}
			if sign == '+' && deprecatedSet.MatchString(text) {
				f.deprecates = true
			}
		}
		if !schema {
			continue
		}
		if indent, name, opens, ok := schemaKey(text); ok {
			if opens {
				open = append(open, block{indent: indent, name: name})
			}
			switch sign {
			case '+':
				added[name] = true
			case '-':
				removed[name] = true
			}
			continue
		}
		if n := len(open); n > 0 {
			if edit && strings.TrimSpace(text) != "" {
				changed[open[n-1].name] = true
			}
			if strings.TrimRight(text, " \t") == open[n-1].indent+"}," {
				open = open[:n-1]
			}
		}
	}

	for name := range added {
		if removed[name] {
			changed[name] = true // dropped and added: moved or reworked, not new
			continue
		}
		f.added = append(f.added, name)
	}
	for name := range removed {
		if !added[name] {
			f.removed = append(f.removed, name)
		}
	}
	for name := range changed {
		if !slices.Contains(f.added, name) && !slices.Contains(f.removed, name) {
			f.changed = append(f.changed, name)
		}
	}
	slices.Sort(f.added)
	slices.Sort(f.removed)
	slices.Sort(f.changed)
	// an api version upgrade: a service imported at a version it was not imported at before, in place of one it was
	for svc, versions := range sdkAdded {
		for v := range versions {
			if len(sdkRemoved[svc]) > 0 && !sdkRemoved[svc][v] {
				f.apiVersion = true
			}
		}
	}
	return f
}

// Kind names what sort of change a PR is, for the page's kind grouping: the
// first of KindOrder that fits. diff is its unified diff ("" when not
// stored, which leaves only what the files, title and labels say), kinds the
// kinds of file it touches, as areas reports them.
func Kind(diff, title string, labels, kinds []string) string {
	f := facts(diff)
	only := func(allowed ...string) bool {
		touched := false
		for _, k := range kinds {
			if k == "changelog" {
				continue // an entry in the changelog rides along with anything
			}
			if !slices.Contains(allowed, k) {
				return false
			}
			touched = true
		}
		return touched
	}
	count := func(n int, one, few, many string) string {
		switch {
		case n == 1:
			return one
		case n <= 4:
			return few
		}
		return many
	}

	switch {
	case f.resources > 1:
		return KindResources
	case f.resources == 1:
		return KindResource
	case f.dataSources > 1:
		return KindDataSources
	case f.dataSources == 1:
		return KindDataSource
	case f.listResources > 0:
		return KindListResource
	case f.apiVersion:
		return KindAPIVersion
	case f.migration:
		return KindStateMigration
	case refactorTitle.MatchString(title):
		return KindRefactor
	case len(f.removed) > 0:
		return KindPropRemoved
	case f.deprecates:
		return KindDeprecation
	case len(f.added) > 0:
		return count(len(f.added), KindPropAdded, KindPropsAdded, KindPropsAddedMany)
	case len(f.changed) > 0:
		return count(len(f.changed), KindPropChanged, KindPropsChanged, KindPropsChgMany)
	case only("tests"):
		return KindTestFix
	case only("docs", "examples", "contributing"):
		return KindDocumentation
	case only("ci", "vendor"):
		return KindTooling
	case slices.Contains(labels, "bug"):
		return KindBugFix
	}
	return KindOther
}
