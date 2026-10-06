## v0.2.0 (2026-10-06)

- only admins (`PRAWN_ADMINS`) can refresh the page or replace its database
- the page can be served without a GitHub token
- the provider checkout is cloned and kept up to date automatically; `fetch git` does it alone
- renamed settings: `GITHUB_REPOS`, `PRAWN_FETCH_CRON`, `PRAWN_PORT` (old names no longer work)
- `make` builds again on machines that force vendored dependencies

## v0.1.1 (2026-10-05)

- release binaries and homebrew formula; v0.1.0 only published the docker image

## v0.1.0 (2026-10-05)

Initial release.

- `fetch`: pull requests, CI and TeamCity test results into a local database
- `explore`: an interactive page over every PR, with filters, trends and suggestions
- `explore --serve`: serves the page, with refresh and database download/upload
- `close` and `label`: find PRs to close or label, optionally AI-scored
- docker image that serves the page and refreshes it on a schedule
