You are triaging OPEN pull requests for the terraform-provider-azurerm provider. Each numbered PR below changes or references one or more resources, data sources, or properties that have been REMOVED from the provider or formally DEPRECATED, listed with when it happened, the source (a major-version upgrade guide, the changelog, or a deprecation marker in the provider source), and the successor to use instead when one exists. For property-level matches you are also shown where the property matched: a CHANGED LINE from the PR's own diff in that resource's file (prefixed - or + for removed or added: the PR is acting on the property itself, the strongest form) or, failing that, the title/body line that mentions it; for removed resources you are told when the name no longer appears anywhere in the provider source. When the PR also touches resources that are NOT removed or deprecated, those are listed too — weigh them hard: a PR whose substance lives in one of those is not moot, whatever else it touches. You are given the PR's title, body, changed files, dates, review state, and recent comments.

For each PR judge how likely its change is moot where it stands, meaning the PR really targets the removed or deprecated thing itself, so that closing it and pointing at the successor is the right call. Score HIGH when the PR fixes a bug in, adds a feature to, or reworks the removed resource or property specifically — a diff line that adds, validates, or documents a property the provider has since removed is the clearest case: its changed files live in the removed resource's code or docs, or its stated purpose is to change the removed property. The change being reasonable does not matter; if the thing it targets is gone from the provider, the PR as proposed cannot merge and a fresh PR against the successor is the correct path.

Score LOW when:

- The PR's diff REMOVES the property (a - line deleting it) and the provider's own removal came later: the PR was proposing the very change the provider made, so it is delivered rather than moot — it belongs to the resolved check, not this one; score low here.
- The reference is incidental: the resource or property merely appears in the body's example configuration or test output, or the PR touches its file only as part of a wide mechanical sweep, while the actual change concerns something that still exists.
- The PR equally or primarily changes a resource that still exists in the provider; do not close a live change because a dead resource appears alongside it.
- The evidence is a deprecation only, not a removal. Deprecated things still work and still accept fixes; closing is premature unless the PR extends the deprecated thing with new functionality that will die with it.
- The thread shows a maintainer engaging with the change on its merits since the removal happened; that is a live review, not a stale PR.

Be conservative: when it is unclear whether the change targets the dead thing or a live one, score low.

Respond with ONLY a JSON array, one entry per PR, no other text:
[{"number": <int>, "confidence": <0.0 to 1.0>, "reason": "<one sentence naming the removed thing and why the PR is or is not moot>"}]

PRs:
