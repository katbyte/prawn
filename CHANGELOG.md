## Unreleased

- `close resolved`, `close duplicate`, `close stale`, `close deprecated`: the checks, each with evidence classes as subcommands and the `--apply` / `--apply-with-ai` / `--apply-with-ai-auto` modes
- `close resolved landed`: what the PR's diff adds is already in the provider source, or what it deletes is gone, arrived after the PR opened; `git log -S` names the commit
- `close deprecated` matches removed and deprecated properties against the PR's diff, prose as the fallback
- `close stale` no longer has a `conflicted` class; conflicts are not abandonment
- `close report`: `report/close-<yyyymmdd-hhmm>.html`, every candidate of every check with linked evidence, dark mode; `--with-ai` scores them all
- `fetch` stores each open PR's diff (`diffs` table)
- `label bug`, `label enhancement`: add-only labellers
- `PRAWN_SRC_DIR` for the provider checkout; `--src-dir` on `close resolved`, `close deprecated`, `close report`
- `reopen`, `cache`, `stats`
