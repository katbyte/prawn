# 🦐 prawn — PRs, Attention Where Needed

[![GitHub release](https://img.shields.io/github/v/release/katbyte/prawn?color=blueviolet)](https://github.com/katbyte/prawn/releases/latest)
[![Go Version](https://img.shields.io/github/go-mod/go-version/katbyte/prawn?label=go&color=00ADD8)](https://github.com/katbyte/prawn/blob/main/go.mod)
[![License](https://img.shields.io/github/license/katbyte/prawn?color=blue)](https://github.com/katbyte/prawn/blob/main/LICENSE)
![build](https://github.com/katbyte/prawn/actions/workflows/build.yaml/badge.svg)
![lint](https://github.com/katbyte/prawn/actions/workflows/pr-golangci-lint.yaml/badge.svg)
![CodeQL](https://github.com/katbyte/prawn/actions/workflows/codeql-analysis.yml/badge.svg)

A command-line utility for triaging a repository's open pull
requests: it finds the ones to close, labels the ones missing labels, and builds an explore page over the
review flow. Built for `hashicorp/terraform-provider-azurerm`; the sibling of [koi](https://github.com/katbyte/koi)
for issues.

Everything works from a local SQLite database filled by `prawn fetch`. The checks list candidates with their
evidence, an AI CLI can score them, and nothing changes on GitHub without an apply flag. Closes comment first
and are recorded so `prawn reopen` can undo them.

## Installation

Download a binary for your platform from the [latest release](https://github.com/katbyte/prawn/releases/latest)
(signed with cosign, with build provenance), or:

```bash
go install github.com/katbyte/prawn@latest
docker pull ghcr.io/katbyte/prawn:latest   # the server, see Docker below
```

## Usage

```bash
export GITHUB_TOKEN=$(gh auth token)
export PRAWN_AI_CMD=claude PRAWN_AI_MODEL=opus   # for the --apply-with-ai modes
export PRAWN_SRC_DIR=~/src/terraform-provider-azurerm   # a provider checkout, for close resolved / deprecated

prawn fetch                        # every open PR into prs.db; later runs sync incrementally
prawn stats                        # what the db holds

prawn close report                 # report/close-<date>.html: every close candidate with its evidence
prawn close report --with-ai       # the same, AI-scored

prawn close resolved --apply-with-ai   # already dealt with: landed, linked issues closed, or superseded
prawn close duplicate --apply-with-ai  # another open PR makes the same change
prawn close stale --apply-with-ai      # the author walked away
prawn close deprecated --apply-with-ai # changes something the provider removed
prawn label bug --apply-with-ai        # add the bug label the evidence supports
prawn label enhancement --apply-with-ai

prawn reopen 1234 --comment "closed in error"
```

Every check is `prawn <action> <check> [class]`. Bare is a report; the apply modes act:

| flag | what it does |
|---|---|
| `--apply` | act on the evidence, no AI (`--dry-run` to preview) |
| `--apply-with-ai` | the AI scores each candidate, you confirm each one |
| `--apply-with-ai-auto[=0.85]` | act alone at or above a confidence |

The classes are subcommands, strongest first, e.g. `prawn close resolved landed` or `prawn close stale waiting`.

| check | closes / labels when | classes |
|---|---|---|
| `close resolved` | the change already landed, every linked issue closed, or the thread says superseded | landed · issue-closed · superseded |
| `close duplicate` | another open PR makes the same change | same-issue · similar |
| `close stale` | the author stopped responding | waiting · changes-requested |
| `close deprecated` | the PR changes a resource or property the provider removed | resource · property |
| `label bug` | it fixes something and the label is missing | linked-issue · title |
| `label enhancement` | it adds something and the label is missing | linked-issue · title |

An approved PR is never proposed for close, and neither is one with `--keep-reactions` or more 👍.

## Explore

```bash
export PRAWN_SINCE=2023-01-01                    # fetch also backfills every PR closed or merged since
export PRAWN_GROUP_MEMBERS=katbyte,jackofallops   # author groups, any number of PRAWN_GROUP_<name>
export PRAWN_GROUP_PARTNERS=magodo,wodansson
prawn fetch                       # github, teamcity's test builds when TC_* is set, the provider checkout when PRAWN_SRC_DIR is; fetch gh / tc / git for one alone
prawn explore                     # -> report/explore.html
prawn explore --serve 8765        # ...and serve it, for other machines on the network
```

One self-contained page over every PR open at any point in the period, with a filter bar, and six tabs picked from the dropdown in the header:

- **prs** — the matching PRs as a table: pick, reorder, and sort the columns; **group by** kind (1 property added, 1 resource added, api version upgrade, test fix…), suggested category, service, court, status, ci, failing check, tests, author group, effort, or author to tackle alike PRs together; **show** only one sort of PR; **open** them all in tabs; **export** them as csv, a copy for a spreadsheet, or markdown for slack or github. Click a row for the PR's detail: what the change is, its life as a bar of states, ci, tests, review, mergeability, what is failing, and its timeline
- **trends** — metrics over time: backlog, flow, review status, times, review load, and quality by author group
- **suggested** — easy wins by category (docs only, approved but unmerged, small and unanswered, …) or by service
- **people** — authors and reviewers
- **services** — services, the kinds of file changed, labels
- **checks** — the close candidates, restricted to the filter

Every control lives in the url, so a view is a link, and views can be saved, downloaded, and pasted.

**CI and tests.** Each `fetch` records every open PR's checks: passing, failing (which checks, and for how
long), running, waiting for a maintainer to approve a fork's workflows, or expired. It also records how
far the PR is behind its base and how stale the result is. With `TC_SERVER`, `TC_TOKEN` and `TC_PROJECT`
set, it also collects the acceptance test builds TeamCity ran for each PR, with the failed tests named.
Read-only: prawn never starts a build.

**Served** with `--serve`, the page gets two buttons. **refresh** has the server sync and rebuild while a
window shows its progress. **db** downloads the server's database or uploads one in its place. Every
request is logged, naming the viewer when a login proxy in front sets `X-Forwarded-User` or similar.

## Docker

A container that keeps the explore page fresh and serves it: `prawn explore --serve` runs on start, and
a cron job inside refreshes the page on a schedule (`PRAWN_FETCH_CRON`, set in `docker-compose.yml`).

```bash
# .prawn holds GITHUB_TOKEN, GITHUB_REPOS, PRAWN_SINCE, PRAWN_GROUP_<name>, TC_*... — the same file the cli reads
docker compose pull    # ghcr.io/katbyte/prawn, published on each release; `make docker` builds it from a checkout
docker compose up -d   # -> http://localhost:8765/ ; the db and the page live in ./data
```

On the first start with an empty `./data`, the page is empty until the first sync has walked the whole
repo, which takes hours. The page is open to
anyone who can reach the port, so put a login in front of it (oauth2-proxy, Cloudflare Access) before
exposing it beyond the local network.

To skip that walk, start from a database fetched elsewhere. Either copy it to `./data/prs.db` before the
first start, or bring the container up and use the page's **db** button (top right, beside refresh).
**upload** puts a database in the server's place and rebuilds the page from it; **download** saves the
server's as `prs.<yyyymmdd>.db`. An upload is checked first, and the one it replaces is kept as
`prs.db.replaced`.

## Configuration

All options can be passed as command-line flags, environment variables, or via a `.prawn` file (env format)
in your home directory or the current one.

| Variable | Flag | Description |
|---|---|---|
| `GITHUB_TOKEN` | `--token-gh` | GitHub token; `explore` runs without one, serving the database as it is |
| `GITHUB_REPOS` | `--repo`, `-r` | Repository to triage (default `hashicorp/terraform-provider-azurerm`); one for now |
| `TC_SERVER` | `--tc-server` | TeamCity host, for each PR's acceptance test results (optional; all three or none) |
| `TC_TOKEN` | `--tc-token` | TeamCity access token; read-only is enough |
| `TC_PROJECT` | `--tc-project` | The TeamCity project id the test builds live under |
| `PRAWN_DB` | `--db` | Path to the SQLite database (default `prs.db`) |
| `PRAWN_SRC_DIR` | `--src-dir` | A local provider checkout, for `close resolved landed`, `close deprecated`, the report, and explore's release markers; cloned there when the path is missing or empty |
| `PRAWN_ADMINS` | `--admins` | With `--serve`: the logins that may refresh the page and upload a database, as the login proxy in front names them (default: anyone). Only meaningful when that proxy is the only way in |
| `PRAWN_SINCE` | `--since` | Start of the explore period, `yyyy-mm-dd`; `fetch` backfills closed and merged PRs to it |
| `PRAWN_GROUP_<name>` | | Author groups for explore, `login,login` |
| `PRAWN_MAINTAINERS` | | The group that counts as maintainers (default `members`) |
| `PRAWN_KEEP_REACTIONS` | `--keep-reactions` | 👍 count at or above which a PR is never proposed for close (default 10) |
| `PRAWN_AI` | `--ai` | `false` lists candidates without scores |
| `PRAWN_AI_CMD` | `--ai-cmd` | The AI CLI to run: `claude`, `gemini`, `agy`, or `bob` |
| `PRAWN_AI_MODEL` | `--ai-model` | The model to pass to it |
| `PRAWN_AI_TIMEOUT` | `--ai-timeout` | Minutes per AI call (default 10) |
| `PRAWN_AS` | `--as` | Who is deciding, recorded on actions (default `$USER`) |
| `PRAWN_NO_AUTO_FETCH` | `--no-auto-fetch` | Never touch the network; run against the local db as-is |
| `PRAWN_GH_THROTTLE` | | Gap between GraphQL requests, e.g. `500ms` |
| `PRAWN_LOG` | | `debug` or `trace` for HTTP dumps |

## Development

```bash
make tools       # pinned dev tools into .tools/bin
make             # fmt + build -> ./prawn
make check-all   # build, test, lint, actionlint, yamllint, shellcheck, typos, depscheck
```
