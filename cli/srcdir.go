package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/katbyte/go-kt/cout"
)

// EnsureSrcDir makes --src-dir / PRAWN_SRC_DIR a checkout of the repository,
// as tctest does its --local-repo-path: a path that does not exist yet, or an
// empty directory, gets a clone of it; anything else must already be a git
// checkout. Unlike tctest it never resets or cleans one — prawn only reads it.
// Nothing to do when no path is set.
func (f *FlagData) EnsureSrcDir() error {
	dir := f.Cmd.SrcDir
	if dir == "" {
		return nil
	}
	var empty bool
	switch entries, err := os.ReadDir(dir); {
	case errors.Is(err, os.ErrNotExist):
		empty = true
	case err != nil:
		return fmt.Errorf("reading the provider checkout %s: %w", dir, err)
	default:
		empty = len(entries) == 0
	}

	if empty {
		url := "https://github.com/" + f.GH.Repo + ".git"
		// whole history: the landed class searches it (git log -S) and the release markers read its tags
		cout.Printf("cloning <cyan>%s</> into <cyan>%s</> for its history and tags — a few minutes, once...\n", url, dir)
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil { //nolint:gosec // the user's own --src-dir
			return fmt.Errorf("creating %s: %w", filepath.Dir(dir), err)
		}
		cmd := exec.CommandContext(context.Background(), "git", "clone", "--quiet", url, dir) //nolint:gosec // the repo and path are the user's own settings
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("cloning %s into %s: %s: %w", url, dir, strings.TrimSpace(string(out)), err)
		}
	}

	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return fmt.Errorf("the provider checkout %s is not a git repository (empty it, or point PRAWN_SRC_DIR somewhere new, and prawn clones one): %w", dir, err)
	}
	return nil
}

// SyncSrcDir brings the provider checkout up to date: cloned when missing,
// then fetched, tags included, and moved forward to what it tracks — but only
// when it sits clean on a branch with an upstream. A checkout someone works
// in (another branch, local changes, a detached head) is fetched and left
// where it is, so prawn never moves anyone's work.
func (f *FlagData) SyncSrcDir() error {
	dir := f.Cmd.SrcDir
	if dir == "" {
		return errors.New("no provider checkout to fetch: set PRAWN_SRC_DIR or --src-dir")
	}
	if err := f.EnsureSrcDir(); err != nil {
		return err
	}
	cout.Printf("fetching the provider checkout at <cyan>%s</>...\n", dir)
	if _, err := git(dir, "fetch", "--quiet", "--tags", "--prune", "origin"); err != nil {
		return err
	}

	branch, berr := git(dir, "symbolic-ref", "--short", "-q", "HEAD")
	upstream, uerr := git(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	changes, cerr := git(dir, "status", "--porcelain", "--untracked-files=no")
	switch {
	case berr != nil || branch == "":
		cout.Printf("  <gray>fetched; its head is detached, so it stays where it is</>\n")
		return nil //nolint:nilerr // no branch is an answer: nothing to move
	case uerr != nil:
		cout.Printf("  <gray>fetched; %s tracks no upstream, so it stays where it is</>\n", branch)
		return nil //nolint:nilerr // no upstream is an answer: nothing to move to
	case cerr != nil:
		return cerr
	case changes != "":
		cout.Printf("  <yellow>fetched; %s has local changes, so it stays where it is</>\n", branch)
		return nil
	}

	before, _ := git(dir, "rev-parse", "HEAD")
	if _, err := git(dir, "merge", "--ff-only", "--quiet", upstream); err != nil {
		cout.Printf("  <yellow>fetched; %s has commits %s does not, so it stays where it is</>\n", branch, upstream)
		return nil //nolint:nilerr // a branch of someone's own is theirs to sort out, not a failed fetch
	}
	after, _ := git(dir, "rev-parse", "HEAD")
	moved := "already up to date"
	if before != after {
		n, _ := git(dir, "rev-list", "--count", before+".."+after)
		moved = n + " new commits"
	}
	cout.Printf("  <gray>%s at %.10s: %s</>\n", branch, after, moved)
	return nil
}

// git runs git in dir and returns what it printed, trimmed.
func git(dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...) //nolint:gosec // fixed git subcommands on the user's own checkout
	out, err := cmd.Output()
	if err != nil {
		msg := ""
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			msg = strings.TrimSpace(string(ee.Stderr))
		}
		return "", fmt.Errorf("git %s in %s: %s: %w", strings.Join(args, " "), dir, msg, err)
	}
	return strings.TrimSpace(string(out)), nil
}
