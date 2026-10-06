## v0.1.0 (2026-10-05)

Initial release.

- `fetch`: pull request data for a repository into a local sqlite database (`prs.db`): comments, reviews, files, linked issues, timelines, diffs, ci state and mergeability, and the acceptance test builds teamcity ran on each PR when `TC_SERVER`, `TC_TOKEN` and `TC_PROJECT` are set; `fetch gh` and `fetch tc` do one source alone
- `explore`: one self-contained interactive html page over every PR open since `--since`, with a filter bar and a query language over it, and tabs for the PRs (grouping, sorting, columns, export, a detail per PR), trends, suggested easy wins, services, people and the close checks
- `explore --serve`: serves the page, with a **refresh** button that syncs and rebuilds it, a **db** button to download the database or upload one in its place, and a log line per request naming the viewer when a login proxy sits in front
- `close resolved`, `close duplicate`, `close stale`, `close deprecated`: find PRs the evidence says are done and close them, optionally scored by AI; `close report` writes every candidate to an html report
- `label bug`, `label enhancement`: add the labels a PR's content supports (add-only)
- `reopen`, `stats`, `cache`
- a docker image and compose file that serve the page and refresh it on a schedule
