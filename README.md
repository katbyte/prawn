# 🦐 prawn — PRs: Attention Where Needed

[![GitHub release](https://img.shields.io/github/v/release/katbyte/prawn?color=blueviolet)](https://github.com/katbyte/prawn/releases/latest)
[![Go Version](https://img.shields.io/github/go-mod/go-version/katbyte/prawn?color=00ADD8)](https://github.com/katbyte/prawn/blob/main/go.mod)
[![License](https://img.shields.io/github/license/katbyte/prawn?color=blue)](https://github.com/katbyte/prawn/blob/main/LICENSE)

`prawn` puts attention on the pull requests that need it: assisted bulk
triage of a GitHub repository's open PRs — finding the ones to close, sorting
the rest into clusters for efficient review, surfacing the easy merges, and
drafting basic reviews. Built for `hashicorp/terraform-provider-azurerm` and
its long tail of open PRs, the sibling of [koi](https://github.com/katbyte/koi)
(the keeper of issues) — **PR**s: **A**ttention **W**here **N**eeded.

It fetches every open PR — **including the comments, reviews, changed files,
linked issues, and the diff itself, which is where the story is on an old
PR** — into a local SQLite database, then runs a battery of evidence **checks**: deterministic
sweeps tuned for recall surface the candidates, an AI judge reads each one's
actual substance for precision, and a human approves actions one
evidence-packed card at a time. Nothing touches GitHub without an approved
action, applies are throttled, and every mutation is audited and reversible
(`prawn reopen`).

## The command shape

Every command follows the same three-level grammar (koi's):

```
prawn <action> <check> [class]
```

- **action** — what prawn changes: `close` (close PRs), `label` (add labels).
- **check** — the question asked and the kind of evidence that answers it:
  `close resolved`, `close stale`, `label bug`, …
- **class** — the shape or strength of the evidence, as a subcommand
  (strongest first): `close stale waiting`, `close resolved issue-closed`,
  `close duplicate same-issue`. Bare check = all classes.

## The checks

Each check asks one question about an open PR, from one kind of evidence:

| check | the question | the evidence | classes |
|---|---|---|---|
| `prawn close resolved` | this change has **already been dealt with** — close it? | what the PR's diff adds already being in the provider source (arrived after the PR) + the linked issues' fates + superseded claims in the thread | landed · issue-closed · superseded |
| `prawn close duplicate` | another open PR makes the **same change** — keep the review in one place? | shared closing issues + near-identical titles over overlapping files | same-issue · similar |
| `prawn close stale` | the author **walked away** — close out the thread? | the waiting-response label, unanswered reviews | waiting · changes-requested |
| `prawn close deprecated` | this PR changes something the provider has **removed or deprecated** — moot as proposed? | the upgrade guides, changelog deprecations, and deprecation markers in a local provider checkout, matched against the PR's diff | resource · property |
| `prawn label bug` | this PR **fixes something** its labels don't record — label it? | linked issues wearing the label + fix-shaped titles | linked-issue · title |
| `prawn label enhancement` | this PR **adds something** its labels don't record — label it? | linked issues wearing the label + support-for-shaped titles | linked-issue · title |

Every check works the same way: evidence classes as subcommands (strongest
first), an AI that judges the actual substance, and three apply modes —
`--apply` acts on the evidence with no AI (combine with `--dry-run` to get a
sense of the changes), `--apply-with-ai` shows each card with its score and
asks you, `--apply-with-ai-auto[=t]` acts alone above a confidence threshold.
Bare invocation is always a report; `--dry-run` previews any apply. Labels are
only ever added, never removed.

`close resolved` (alias `fixed`) is deliberately exhaustive: its judge blocks
carry everything prawn holds on each candidate — the full body, every changed
file, every review, and the whole human thread, not a digest — in smaller
batches, because "is this still needed" hinges on details anywhere in the PR.
Its `landed` class is the one that catches the quiet case: nobody linked an
issue or said a word on the thread, but somebody else's later commit made the
change (the 5.0 service refactors, say). It reads the identifiers the PR's
diff introduces and deletes — schema and doc properties, by file — and asks
the provider checkout: is each added one in that file at HEAD though it was
absent when the PR was opened, and is each deleted one gone now though it
was there then? Identifiers the PR merely reused are neutral; ones still
missing, or still present, are the PR's undelivered part. A rename the
provider made under a different name still lands, on the deletions. When
most of either kind has landed, `git log -S` names the commit, and the judge
reads it against the PR's substance.

`close deprecated` matches removed and deprecated properties against the
PR's **diff**, in the resource's own file — a `- "zone_name": {` line is the
PR acting on the property, where the title and body may never name it. The
title/body prose is the fallback for PRs whose diff GitHub will not render.

## Workflow

```sh
export GITHUB_TOKEN=$(gh auth token)
export PRAWN_AI_CMD=claude PRAWN_AI_MODEL=opus   # required by every --apply-with-ai mode (no default)

prawn fetch            # every open PR + comments + reviews + files + linked issues + timeline + diff
                       # -> prs.db (resumable; later runs sync incrementally and
                       # reconcile the open set — the only required setup step)

prawn stats            # what the db holds: open PRs by review state

export PRAWN_SRC_DIR=~/src/azurerm   # a provider checkout: resolved reads its git history for
                                     # changes that already landed, deprecated its upgrade
                                     # guides, changelog, and deprecation markers
prawn close report     # report/close-<yyyymmdd-hhmm>.html: every close candidate each check
                       # sees, with its evidence linked — no AI, seconds to run
prawn close report --with-ai            # the same page with every candidate AI-scored, surest first
prawn close report --with-ai --limit 10 # AI-score a small slice per check first — cheap end-to-end test

# then work one check at a time. Bare is always a report; the apply modes act:
#   --apply              act on the evidence, no AI
#   --apply-with-ai      card + score + (a)ccept (s)kip (p)review (o)pen (q)uit per PR
#   --apply-with-ai-auto[=t]  unattended at or above a confidence
prawn close resolved --apply-with-ai   # a later commit made the change, the linked issues closed,
                                       # or the thread says superseded
prawn close duplicate --apply-with-ai  # another open PR makes the same change
prawn close stale --apply-with-ai      # the author walked away (waiting-response:
                                       # 90 days is enough; changes-requested 180)
prawn close deprecated --apply-with-ai # it changes a resource or property the provider removed

prawn label bug --apply-with-ai         # add the bug label the evidence supports (add-only)
prawn label enhancement --apply-with-ai # add the enhancement label (add-only)

prawn reopen 1234 --comment "reopening, closed in error"   # mistake recovery

# every check takes its evidence classes as subcommands, strongest first:
prawn close resolved landed --apply-with-ai        # only PRs a later merged commit covers
prawn close resolved issue-closed --apply-with-ai  # only PRs whose every linked issue closed
prawn close stale waiting --apply-with-ai           # only waiting-response PRs the author abandoned
prawn close duplicate similar --dry-run             # pairs nobody linked, preview only
prawn label bug linked-issue --apply-with-ai-auto=0.9  # only issue-backed labels, above 0.90

# the explore page: every PR open at any point in the period, one html file, no server
export PRAWN_SINCE=2023-01-01                          # the period's start (fetch backfills to it)
export PRAWN_GROUP_MEMBERS=katbyte,jackofallops         # named groups of logins, any number of them —
export PRAWN_GROUP_PARTNERS=magodo,wodansson            # in the env or .prawn, never in the repo
export PRAWN_MAINTAINERS=members                        # which group counts as maintainers (default)
prawn fetch                      # with PRAWN_SINCE set: also every PR closed or merged since, timelines included
prawn explore                    # -> report/explore.html: filter bar over data · trends · suggested · areas · people · checks
prawn explore --with-ai          # the checks tab scored, as close report --with-ai
prawn explore --checks=false     # skip the checks (no provider checkout needed)

prawn cache                      # list the local db's caches and sizes
prawn cache clear ai             # drop AI verdicts (or prs|all)
```

## What closes, what keeps

Every close comments first, from a per-check template in `assets/comments/` —
citing the closed issues, the superseding claim with a deep link, the
surviving duplicate, the unanswered review — then closes the PR and records an
auditable action row so `prawn reopen` can undo it.

Keep protections run **before** any close check: an **approved** PR is never
listed (that is a merge waiting to happen, not an abandonment), and 👍 ≥
`--keep-reactions` pins a PR open. Each check's judge re-checks every proposed
close in full thread context as a second safety net — a PR doing more than its
linked issues cover, an author waiting on the maintainers, and same-files-but-
different-fixes pairs all score low.

## Explore

`prawn explore` writes one self-contained page over every PR that was open at
any point since `--since` / `PRAWN_SINCE` — the open set plus everything closed
or merged since — with a global filter bar above six tabs, every tab a view
of what the filter matches. The state of every control lives in the url, so a
view is a link.

The filter: period, state, group, author, service, kind of change (schema,
code, tests, docs, vendor, ci, changelog — read off the changed files, and
exclusive: docs means docs and nothing else), label, court, effort, and a query
box — `author:x label:bug age>90 -kind:docs`, keys for everything the page
derives (`court cd idle size rounds fr reviewer responder lastmaint check ai`
and more; the page lists them).

- **data** — every PR the filter matches, sorted by whose court they sit in
  and how cheap they are to review (or by any column), with query presets:
  needs first look, approved but unmerged, pushed after changes requested,
  waiting over 30 days, small and idle, conflicted. The columns are yours:
  drag a header to reorder, × removes it, **columns** adds any field
  collected on a PR — the review roll (reviewed by, approved by, changes
  requested by with `×requests ✎comments`, as [ghp-sync](https://github.com/katbyte/ghp-sync)
  stamps them on a project), status, CI, dates, decision, mergeability,
  milestone, waiting, resolve time, linked issues, checks. The column set
  lives in the url, so saved views (the same save / download / upload /
  paste / ai controls as trends) keep a table layout. A row opens into the
  PR's life as a bar of its daily states, its numbers, and its full timeline.
- **trends** — [tfpp](https://github.com/katbyte/tf-provider-profiler)'s
  model: a tree of metrics over the filtered set (the backlog by state, court,
  group, effort, and service; the flow of opened, merged, and closed; response
  and merge times; review load per reviewer; waiting-response cycles; the
  quality of each author group's PRs as the member work they needed — reviews,
  review comments, and rounds per PR by the month opened — and that work as
  load by the month it was left), presets
  that add bundles, one panel per selection, panels that combine by dragging
  one onto another (a second unit goes on the right axis), stack, split, and
  a `% change` view. Release tags from the provider checkout are the markers.
- **suggested** — the easy wins among the matching open PRs, by category:
  approved but unmerged, docs and changelog only, tests and CI only,
  dependency bumps, small fixes and single-service enhancements in the
  maintainers' court, small re-reviews after the author pushed, small PRs
  never answered, and likely closes from the checks. Each row says why it is
  cheap, and every category is a `suggested:<name>` query on the data tab.
  **by service** regroups the small PRs in the maintainers' court by the
  service they touch, most first: read the service once, review them together.
- **people** — authors matching the filter with merge rate (and its trend
  over the last six months), time to merge, rounds, first response; reviewers
  with their touches, approvals, changes requested, first responses, and
  response time. Click a login to filter by it.
- **areas** — services from the changed files' `internal/services/<name>`,
  kinds of change, and labels, each with open count, share in the maintainers'
  court, unanswered, effort, merge rate, and top authors and reviewers.
- **checks** — every close candidate the checks see, restricted to the
  filter; needs the provider checkout, `--with-ai` scores them.

The stats come from each PR's timeline, replayed: **ball in court** is the
author's under draft, the waiting-response label, or a changes-requested
review nothing has been pushed since, and the maintainers' otherwise; **first
response** is the first review, comment, or close by a maintainer other than
the author; **effort** is a 1–5 estimate from the diff's lines, files, and
services, discounted for docs- and vendor-only changes. Maintainers are the
`PRAWN_MAINTAINERS` group (default `members`), or whoever GitHub marks
member, owner, or collaborator when no group is set. Groups are
`PRAWN_GROUP_<name>=login,login`, any number of them, from the environment or
`.prawn` — never the repo.

## Config

Flags, env vars, or a `.prawn` file (env format) in `$HOME` or `.`:
`GITHUB_TOKEN`, `PRAWN_REPO`, `PRAWN_DB`, `PRAWN_KEEP_REACTIONS`,
`PRAWN_NO_AUTO_FETCH` (never touch the network for freshness — run against the
local db as-is), `PRAWN_SRC_DIR` (a local provider checkout, for `close
resolved landed`, `close deprecated`, the report, and the explore page's
release markers and checks tab), `PRAWN_SINCE` (the explore period's start,
`yyyy-mm-dd`; `fetch` backfills every PR closed or merged since it),
`PRAWN_GROUP_<name>` and `PRAWN_MAINTAINERS` (see Explore), `PRAWN_AI` (`false`
lists without scores), `PRAWN_AI_CMD`, `PRAWN_AI_MODEL`, `PRAWN_AI_TIMEOUT`
(minutes per AI call), `PRAWN_AS`, `PRAWN_GH_THROTTLE` (gap between GraphQL
requests, e.g. `500ms`), `PRAWN_LOG` (debug/trace HTTP dumps). AI calls shell
out to an already-authenticated CLI — no API key management, but
`PRAWN_AI_CMD` and `PRAWN_AI_MODEL` are required (no defaults). `claude`, `gemini`, antigravity's `agy`, and IBM's `bob` are
recognised by binary name; anything speaking one of those dialects works.
Model aliases like `fable` resolve to their canonical id up front, so cached
verdicts always record exactly which model produced them.

## Building

```sh
make        # fmt + build -> ./prawn
make test
make lint
```
