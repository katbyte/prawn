You are triaging OPEN pull requests for the terraform-provider-azurerm provider. Each numbered PR below appears to already be resolved — no longer needed — classed by the evidence. LANDED: the provider source already reflects this PR's change though it did not when the PR was opened — the identifiers its diff introduces (schema properties, doc properties) now exist in the source, or the ones it deletes are now gone — with the commit that did it listed with its subject, its own PR number, and the release it shipped in; identifiers the source still lacks, or still holds, are listed too (deleted names gone while the PR's new names are absent is usually the same rename done under different names) — the question is whether that commit delivered what this PR proposed (the same change made by someone else, or a wider rework that covered it), or merely happens to use the same names for something else. ISSUE-CLOSED: every issue the PR's closing keywords reference ("fixes #N") has since been closed — the problem it set out to solve was dealt with some other way, or turned out not to need solving. SUPERSEDED: a maintainer's comment on the thread says the change has been covered elsewhere — superseded by, replaced by, handled in another PR. You are given each candidate IN FULL: the complete PR body, every changed file, its labels and review decision, the evidence (the linked issues with how each was closed, or the superseding claim), every review, and every human comment in the thread. Read all of it — the detail that decides these often hides in the body's last paragraph or deep in the thread.

For each PR judge how likely the change is genuinely no longer needed — what it set out to do has been delivered or dismissed elsewhere — so closing it with a comment citing the evidence is the right call.

Score HIGH when:

- LANDED: the identifiers now in the source are the substance of this PR (the property it adds, the resource it introduces) and the commit's subject describes the same change — so the PR's purpose has been delivered without it.
- ISSUE-CLOSED: the linked issues were closed as completed (fixed by another change) or clearly resolved, and nothing in the PR — body, files, or thread — goes beyond what those issues covered: the PR's whole purpose has been served.
- SUPERSEDED: the claim is specific (names the covering PR or release), comes from a maintainer, and nobody disputed it since.

Score LOW when:

- LANDED: the identifiers that landed are incidental to this PR's real change, the ones still missing ARE its real change, or the commit used the same names for a different purpose — the PR's actual proposal is undelivered.
- The PR does MORE than the linked issues cover — refactoring, extra properties, additional fixes, files touched beyond the issue's scope — that would be lost by closing it.
- The linked issues were closed as stale, not-planned for inactivity, or as duplicates of an issue that is still open — nothing actually shipped, so the PR may still be the fix.
- SUPERSEDED: the claim is vague, disputed later in the thread, or the supposedly covering change went in a different direction.
- The thread or reviews show recent, active work — the author is responding, pushing commits, or a maintainer committed to reviewing it.
- Anyone in the thread says the problem still exists after the supposedly resolving change.

Be conservative: closing a contributor's still-useful work tells them their effort was wasted. When the evidence is ambiguous, score low.

Respond with ONLY a JSON array, one entry per PR, no other text:
[{"number": <int>, "confidence": <0.0 to 1.0>, "reason": "<one sentence: what the evidence shows and why the PR is or is not still needed>"}]

Pull requests:
