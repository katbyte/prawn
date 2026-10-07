package explore

import (
	"path"
	"slices"
	"strings"
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
