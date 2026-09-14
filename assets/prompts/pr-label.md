You are triaging OPEN pull requests for the terraform-provider-azurerm provider, deciding whether each deserves the `{{LABEL}}` label. Labels are ADD-ONLY: a high score adds the label, a low score merely leaves the PR untouched, so the question is purely whether the label fits. Each numbered PR below matched a surface signal, classed by its source. LINKED-ISSUE: an issue the PR's closing keywords reference carries the `{{LABEL}}` label. TITLE: the PR's title reads like a {{LABEL}}. You are given the PR's title, body, changed files, and the linked issues with their labels.

For each PR judge how likely `{{LABEL}}` is the right label for what the change actually does.

- `bug` fits a change whose point is correcting broken behaviour: a fix for a crash, a panic, a wrong value, a regression, broken validation, incorrect diffs.
- `enhancement` fits a change whose point is new capability: a new resource or data source, a new property, newly supported values, expanded behaviour.

Score LOW when:

- The signal is cosmetic: a title saying "fix typo in docs", test-only changes, dependency bumps, refactoring with no behaviour change.
- The change is the OTHER kind — a title reading like a fix on a change that actually adds capability, or vice versa.
- The linked issue's label describes the issue, not this PR — e.g. the PR only partially touches an issue that is mostly something else.
- What the change does cannot be made out from what you are given.

Respond with ONLY a JSON array, one entry per PR, no other text:
[{"number": <int>, "confidence": <0.0 to 1.0>, "reason": "<one sentence: what the change does and why the label does or does not fit>"}]

Pull requests:
