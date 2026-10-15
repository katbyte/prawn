package explore

import (
	"path"
	"slices"
	"strings"

	"github.com/katbyte/prawn/lib/db"
)

// the files that name what they belong to: a resource's or a data source's
// code and its tests, and its page in the docs
var (
	resourceSuffixes   = []string{"_resource.go", "_resource_test.go"}
	dataSourceSuffixes = []string{"_data_source.go", "_data_source_test.go"}
)

// affected names the resources and the data sources a PR's files change —
// code, tests or docs — as azurerm_<name>, sorted. A file's name is the
// resource's in all but a few cases, the docs page's always.
func affected(files []string) (resources, dataSources []string) {
	res, ds := map[string]bool{}, map[string]bool{}
	for _, f := range files {
		base := path.Base(f)
		switch {
		case strings.HasPrefix(f, "website/docs/r/"):
			res[strings.TrimSuffix(base, ".html.markdown")] = true
		case strings.HasPrefix(f, "website/docs/d/"):
			ds[strings.TrimSuffix(base, ".html.markdown")] = true
		case strings.HasPrefix(f, "internal/services/"):
			for _, s := range resourceSuffixes {
				if name, ok := strings.CutSuffix(base, s); ok {
					res[name] = true
				}
			}
			for _, s := range dataSourceSuffixes {
				if name, ok := strings.CutSuffix(base, s); ok {
					ds[name] = true
				}
			}
		}
	}
	return names(res), names(ds)
}

// sharedFiles is, for each PR, the files it changes that another open PR also changes — what the page needs to say who
// else is working on the same code. Files nearly every change touches (vendored code, generated files, the changelog,
// a service's registration) are left out: sharing one says nothing about the work.
func sharedFiles(prs []*db.PR) map[int][]string {
	open := map[string][]int{}
	for _, p := range prs {
		if p.State != db.PROpen {
			continue
		}

		for _, f := range p.Files {
			if !plumbing(f) {
				open[f] = append(open[f], p.Number)
			}
		}
	}

	out := map[int][]string{}
	for _, p := range prs {
		for _, f := range p.Files {
			if others := open[f]; len(others) > 1 || (len(others) == 1 && others[0] != p.Number) {
				out[p.Number] = append(out[p.Number], f)
			}
		}
	}
	return out
}

// plumbing is a file most changes touch whatever they are about: vendored code, the module files, ci and generated
// config, the changelog, and the lists that register a service or a resource.
func plumbing(f string) bool {
	switch {
	case strings.HasPrefix(f, "vendor/"), strings.HasPrefix(f, ".github/"), strings.HasPrefix(f, ".teamcity/"):
		return true
	case f == "go.mod", f == "go.sum", f == "CHANGELOG.md", f == "internal/clients/client.go", f == "internal/provider/services.go":
		return true
	}
	return strings.HasPrefix(f, "internal/services/") && path.Base(f) == "registration.go"
}

func names(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for n := range set {
		if n != "" && !strings.Contains(n, ".") {
			out = append(out, "azurerm_"+n)
		}
	}
	slices.Sort(out)
	return out
}
