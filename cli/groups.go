// Groups: named sets of GitHub logins the explore page cuts its stats by —
// the maintainers, a partner team, a vendor's contributors. They come from
// the environment (or .prawn), never the repo: PRAWN_GROUP_<NAME>=login,login.

package cli

import (
	"os"
	"sort"
	"strings"

	"github.com/spf13/viper"
)

// groupEnvPrefix is what every group variable starts with; the rest of the
// name, lowercased, is the group's name.
const groupEnvPrefix = "PRAWN_GROUP_"

// maintainersEnv names the group whose members count as maintainers for the
// response-time and ball-in-court stats. Defaults to "members"; when that
// group is empty the author association (MEMBER, OWNER, COLLABORATOR)
// decides instead.
const maintainersEnv = "PRAWN_MAINTAINERS"

// Groups is every configured group, name → sorted logins (lowercased).
type Groups map[string][]string

// LoadGroups reads PRAWN_GROUP_* from the environment and the .prawn file
// viper loaded. Logins are comma separated; blanks and duplicates are dropped.
func LoadGroups() Groups {
	raw := map[string]string{}
	// .prawn first, the environment over it
	for _, k := range viper.AllKeys() {
		if name, found := strings.CutPrefix(strings.ToUpper(k), groupEnvPrefix); found {
			raw[strings.ToLower(name)] = viper.GetString(k)
		}
	}
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if name, found := strings.CutPrefix(k, groupEnvPrefix); found {
			raw[strings.ToLower(name)] = v
		}
	}

	g := Groups{}
	for name, v := range raw {
		seen := map[string]bool{}
		var logins []string
		for l := range strings.SplitSeq(v, ",") {
			l = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(l, "@")))
			if l == "" || seen[l] {
				continue
			}
			seen[l] = true
			logins = append(logins, l)
		}
		if len(logins) > 0 {
			sort.Strings(logins)
			g[name] = logins
		}
	}
	return g
}

// MaintainersGroup names the group that counts as maintainers.
func MaintainersGroup() string {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv(maintainersEnv))); v != "" {
		return v
	}
	if v := viper.GetString(strings.ToLower(maintainersEnv)); v != "" {
		return strings.ToLower(strings.TrimSpace(v))
	}
	return "members"
}

// Maintainers returns the maintainer logins, or nil when the maintainers
// group is not configured (the association decides then).
func (g Groups) Maintainers() []string {
	return g[MaintainersGroup()]
}

// partnersEnv names the group whose reviews count as a partner's in the
// review-status split (members vs partners). Defaults to "partners".
const partnersEnv = "PRAWN_PARTNERS"

// PartnersGroup names the group that counts as partners.
func PartnersGroup() string {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv(partnersEnv))); v != "" {
		return v
	}
	if v := viper.GetString(strings.ToLower(partnersEnv)); v != "" {
		return strings.ToLower(strings.TrimSpace(v))
	}
	return "partners"
}

// Partners returns the partner logins, or nil when no such group is set.
func (g Groups) Partners() []string {
	return g[PartnersGroup()]
}

// Names lists the groups, sorted.
func (g Groups) Names() []string {
	names := make([]string, 0, len(g))
	for n := range g {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
