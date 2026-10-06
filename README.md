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

```bash
brew install katbyte/tap/prawn
# or
go install github.com/katbyte/prawn@latest
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
prawn fetch                       # github, and teamcity's test builds when TC_* is set; fetch gh / fetch tc for one alone
prawn explore                     # -> report/explore.html
prawn explore --serve 8765        # ...and serve it, for other machines on the network; its refresh button re-fetches and rebuilds
```

One self-contained page over every PR open at any point in the period, with a filter bar, and six tabs picked from the dropdown in the header:

- **prs** — the matching PRs as a table; pick, reorder, and sort the columns, **group by** suggested category, kind, documentation type, service, court, status, ci, failing check, properties changed, author group, effort, or author to tackle alike PRs together (a group folds on a click), or **show** only one kind (approved, docs only, ci/test only, a single property, 2–4 properties); **export** the table as a csv with the columns you pick; open a row for the PR's timeline
- **trends** — metrics over time: backlog, flow, review status, times, review load, and quality by author group
- **suggested** — easy wins by category (docs only, approved but unmerged, small and unanswered, …) or by service
- **people** — authors and reviewers
- **services** — services, kinds of change, labels
- **checks** — the close candidates, restricted to the filter

Every control lives in the url, so a view is a link, and views can be saved, downloaded, and pasted.

## Docker

A container that keeps the explore page fresh and serves it: `prawn explore --serve` runs on start, and
a cron job inside refreshes the page on a schedule (`EXPLORE_CRON`, set in `docker-compose.yml`).

```bash
# .prawn holds GITHUB_TOKEN, PRAWN_REPO, PRAWN_SINCE, PRAWN_GROUP_<name>... — the same file the cli reads
make docker            # or `docker compose pull` once a release has published the image
docker compose up -d   # -> http://localhost:8765/ ; the db and the page live in ./data
```

The first start of an empty `./data` walks the whole repo before the page appears. The page is open to
anyone who can reach the port, so put a login in front of it (oauth2-proxy, Cloudflare Access) before
exposing it beyond the local network.

## Configuration

All options can be passed as command-line flags, environment variables, or via a `.prawn` file (env format)
in your home directory or the current one.

| Variable | Flag | Description |
|---|---|---|
| `GITHUB_TOKEN` | `--token-gh` | GitHub token |
| `PRAWN_REPO` | `--repo`, `-r` | Repository to triage (default `hashicorp/terraform-provider-azurerm`) |
| `TC_SERVER` | `--tc-server` | TeamCity host, for each PR's acceptance test results (optional; all three or none) |
| `TC_TOKEN` | `--tc-token` | TeamCity access token; read-only is enough |
| `TC_PROJECT` | `--tc-project` | The TeamCity project id the test builds live under |
| `PRAWN_DB` | `--db` | Path to the SQLite database (default `prs.db`) |
| `PRAWN_SRC_DIR` | `--src-dir` | A local provider checkout, for `close resolved landed`, `close deprecated`, the report, and explore's release markers |
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
