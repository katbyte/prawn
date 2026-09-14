package pr

import "strings"

// FileDiff is one file's changed lines from a unified diff: what the PR adds
// and removes there, context dropped.
type FileDiff struct {
	Path    string
	IsNew   bool // the PR creates the file
	Added   []string
	Removed []string
}

// ParseDiff splits a unified diff into per-file added and removed lines. The
// path is the new-side path ("b/") so renames land under their new name.
func ParseDiff(s string) []FileDiff {
	var out []FileDiff
	var cur *FileDiff
	for line := range strings.SplitSeq(s, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			out = append(out, FileDiff{Path: diffPath(line)})
			cur = &out[len(out)-1]
		case cur == nil:
			continue
		case strings.HasPrefix(line, "--- /dev/null"):
			cur.IsNew = true
		case strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "--- "):
			continue
		case strings.HasPrefix(line, "+"):
			cur.Added = append(cur.Added, line[1:])
		case strings.HasPrefix(line, "-"):
			cur.Removed = append(cur.Removed, line[1:])
		}
	}
	return out
}

// diffPath pulls the b/ path out of a "diff --git a/x b/y" line.
func diffPath(line string) string {
	if _, after, ok := strings.Cut(line, " b/"); ok {
		return after
	}
	return ""
}
