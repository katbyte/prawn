You are triaging OPEN pull requests for the terraform-provider-azurerm provider. Each numbered PR below appears to duplicate another OPEN pull request — the survivor — that would keep the review in one place, classed by how the pair was found. SAME-ISSUE: both PRs' closing keywords reference the same issue ("fixes #N"). SIMILAR: near-identical titles over overlapping changed files, with nobody having linked them. The survivor was picked for carrying more of the review discussion (approvals first, then review and comment weight, then age). You are given both PRs' titles, bodies, dates, changed files, and review states, plus the candidate's thread digest.

For each PR judge how likely it genuinely duplicates the survivor — the same change to the same thing, where closing this one towards the survivor loses nothing.

Score HIGH when:

- Both PRs change the same resource or behaviour to the same end — same fix, same feature — even if the implementations differ in detail.
- SAME-ISSUE: both exist only to close that issue and neither goes beyond it.
- The survivor is clearly the better home: approved or actively reviewed while the candidate sits untouched.

Score LOW when:

- The changes only look alike: same files but different fixes, same title pattern but different resources or properties.
- SAME-ISSUE: the issue has several parts and the two PRs cover different parts.
- The candidate does MORE than the survivor — closing it would lose work the survivor doesn't carry.
- The candidate is the one with the active review and the survivor is the stale one — the pair may be real but the close is aimed at the wrong side.

Be conservative: closing a contributor's distinct work as a duplicate tells them they weren't read. When the overlap is unclear, score low.

Respond with ONLY a JSON array, one entry per PR, no other text:
[{"number": <int>, "confidence": <0.0 to 1.0>, "reason": "<one sentence: what both PRs change and why they are or are not the same change>"}]

Pull requests:
