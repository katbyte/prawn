## Unreleased

- `PRAWN_SRC_DIR` / `--src-dir`: a path that does not exist yet, or an empty directory, gets a clone of the repository on first use, as tctest does its local repo path; anything else must already be a checkout
- `fetch git`: clones the provider checkout when missing, then fetches it (tags too) and moves it forward when it sits clean on a branch with an upstream; one on another branch, with local changes or a detached head is fetched and left where it is. `fetch` does it too, after github and teamcity, when `PRAWN_SRC_DIR` is set
- `explore --serve`: `PRAWN_ADMINS` (`--admins`) names the logins that may refresh the page and upload a database, by the login the proxy in front sets; everyone else may watch a refresh and download, and sees refresh greyed out and no upload. Unset, anyone may, as before
- settings renamed, the old names no longer read: `GITHUB_REPOS` for `PRAWN_REPO` (one repository for now; more is refused), and in the container `PRAWN_FETCH_CRON` for `EXPLORE_CRON` and `PRAWN_PORT` for `PORT`
- `explore` no longer needs `GITHUB_TOKEN`: without one it builds and serves the page from the database as it is, and refresh only rebuilds it — for serving a copied database
- `make` builds prawn again (it had been building only one lint tool), and the dev tools build on a machine with `GOFLAGS=-mod=vendor` set

## v0.1.1 (2026-10-05)

- release: v0.1.0's binaries never built, because the pure-go sqlite has no solaris or 32-bit openbsd port; those two are dropped, and this is the first release with binaries and a homebrew formula (v0.1.0 published only the docker image)

## v0.1.0 (2026-10-05)

Initial release.

- `fetch`: pull request data for a repository into a local sqlite database (`prs.db`): comments, reviews, files, linked issues, timelines, diffs, ci state and mergeability, and the acceptance test builds teamcity ran on each PR when `TC_SERVER`, `TC_TOKEN` and `TC_PROJECT` are set; `fetch gh` and `fetch tc` do one source alone
- `explore`: one self-contained interactive html page over every PR open since `--since`, with a filter bar and a query language over it, and tabs for the PRs (grouping, sorting, columns, export, a detail per PR), trends, suggested easy wins, services, people and the close checks
- `explore --serve`: serves the page, with a **refresh** button that syncs and rebuilds it, a **db** button to download the database or upload one in its place, and a log line per request naming the viewer when a login proxy sits in front
- `close resolved`, `close duplicate`, `close stale`, `close deprecated`: find PRs the evidence says are done and close them, optionally scored by AI; `close report` writes every candidate to an html report
- `label bug`, `label enhancement`: add the labels a PR's content supports (add-only)
- `reopen`, `stats`, `cache`
- a docker image and compose file that serve the page and refresh it on a schedule
