## v0.8.0 (2026-10-08)

- export: "copy project filter" copies a filter that shows just these PRs in a github project view

## v0.7.2 (2026-10-07)

- a sync says what it is doing at every step: how many PRs changed, then progress as it fetches and checks them
- a sync with nothing new finishes in one small request

## v0.7.1 (2026-10-07)

- syncing works with a fine-grained token: github's search answers those nothing, which left PRs missing and out of date
- a PR github refuses in part is still fetched, with less; one it refuses outright is named in the log

## v0.7.0 (2026-10-07)

- a sync now fetches any PR open on github that it does not have — github's search lags, so one opened moments before a sync could be missed for good
- the header says when the data last synced with github, amber after six hours, red when the server cannot sync (no token)
- selecting a range (or right-clicking a point) on a chart shows each series' values at both ends, the change, and its low and high
- a `/test` that started nothing says so, with the workflow's reason (no tests found, merge conflicts, too many services); `slashtest:notrun`
- **resources** and **data sources** columns: what each PR changes, from its code, tests and docs; `resource`, `datasource`, `resources`, `datasources` and `services` query keys
- the scheduled refresh shows on the page as refreshing, as the button's does, and reloads open pages when it ends

## v0.6.0 (2026-10-07)

- right-click a column header for its query key and its commonest values, a click away from the query
- the tests box shows the latest `/test`: who asked, when, and the results the bot posted, linked; a **/test** column and `slashtest`, `slashage` query keys
- the tests box puts its counts beside the state
- a **person** view: pick anyone, then **info** (their numbers and who they work with), **trends** (their work over time, picked like the trends tab) and **data** (every PR they opened, reviewed, approved, commented on, merged or closed)
- names on the people tab open the person view, and its numbers open the matching list
- a **ci passing (except changelog)** filter: passing, or failing only for the missing changelog entry
- the query box suggests keys as you type, then the values the data has for them

## v0.5.0 (2026-10-07)

- the 🦐 prawn title links to prawn on github
- sort the prs table by several columns: shift-click a header to sort by it next
- a **tests passed, outdated** filter: tests passed, but commits have been pushed since
- **ci passing** and **ci (no changelog)** filters: the second is CI failing only for the missing changelog entry
- the merge box says when the branch last caught up with main

## v0.4.0 (2026-10-06)

- how long each PR's branch has been behind main: a **behind for** column, `behindfor` query key, and a **mergeable, behind 30d+** filter
- clearer colours in the terminal: numbers stand out, warnings are orange
- **show** has api upgrade, new resource and new data source; the docs entries read documentation (provider), (examples), (contributing)
- **show** lists how many PRs each entry has, and greys out the empty ones
- the checks tab looks like koi's report: coloured evidence with links, each check's question and classes, and a click on a check folds it
- a **waiting for response** list on the checks tab: PRs where a reviewer asked for something over six months ago and the author has not answered
- **open** and **export** on the checks tab

## v0.3.1 (2026-10-06)

- a failing close check no longer stops the page from rebuilding
- the container trusts a provider checkout owned by another user
- git errors say what git said

## v0.3.0 (2026-10-06)

- a **data** tab: a month, a year or any dates at a glance, and the closed and merged PRs in columns you pick, ready to paste
- a built-in **GC sheet** view with the monthly sheet's columns
- the query box fills the rest of the filter bar
- the effort filter is gone from the top bar
- Copilot's reviews no longer count as a person's

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
