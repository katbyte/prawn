package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEnsureSrcDir(t *testing.T) {
	t.Parallel()
	f := &FlagData{}
	if err := f.EnsureSrcDir(); err != nil {
		t.Errorf("no src dir set: %v, want nothing to do", err)
	}

	notGit := t.TempDir()
	if err := os.WriteFile(filepath.Join(notGit, "README"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.Cmd.SrcDir = notGit
	if err := f.EnsureSrcDir(); err == nil {
		t.Error("a full directory that is not a checkout: no error, want one rather than a clone over it")
	}

	checkout := t.TempDir()
	initCmd := exec.CommandContext(t.Context(), "git", "init", "-q", checkout) //nolint:gosec // a temp dir of the test's own
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	f.Cmd.SrcDir = checkout
	if err := f.EnsureSrcDir(); err != nil {
		t.Errorf("an existing checkout: %v, want it used as it is", err)
	}
}

// a clean checkout moves forward to its upstream; one with local changes is fetched and left alone
func TestSyncSrcDir(t *testing.T) {
	t.Parallel()
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...) //nolint:gosec // the test's own temp repos
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
		return string(out)
	}
	upstream, checkout := t.TempDir(), filepath.Join(t.TempDir(), "src")
	run(upstream, "init", "-q", "-b", "main")
	run(upstream, "commit", "-q", "--allow-empty", "-m", "one")
	run(filepath.Dir(checkout), "clone", "-q", upstream, checkout)
	run(upstream, "commit", "-q", "--allow-empty", "-m", "two")
	run(upstream, "tag", "v1.0.0")

	f := &FlagData{}
	f.Cmd.SrcDir = checkout
	if err := f.SyncSrcDir(); err != nil {
		t.Fatal(err)
	}
	if got := run(checkout, "log", "-1", "--format=%s"); got != "two\n" {
		t.Errorf("after the sync the checkout is at %q, want the upstream's newest commit", got)
	}
	if got := run(checkout, "tag"); got != "v1.0.0\n" {
		t.Errorf("tags after the sync = %q, want the upstream's", got)
	}

	run(upstream, "commit", "-q", "--allow-empty", "-m", "three")
	if err := os.WriteFile(filepath.Join(checkout, "work.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(checkout, "add", "work.txt")
	if err := f.SyncSrcDir(); err != nil {
		t.Fatal(err)
	}
	if got := run(checkout, "log", "-1", "--format=%s"); got != "two\n" {
		t.Errorf("a checkout with local changes moved to %q, want it left where it was", got)
	}
}
