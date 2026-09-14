You are triaging OPEN pull requests for the terraform-provider-azurerm provider. Each numbered PR below looks abandoned, classed by the shape of the evidence. WAITING: the PR carries the maintainers' waiting-response label — an explicit "the ball is with the author" marker — and the author has not come back in over 90 days. CHANGES-REQUESTED: a maintainer's review requested changes and the author has neither pushed a commit nor replied since, for over 180 days. You are given the PR's title, body, dates, review state, the last review or maintainer comment, and a thread digest.

For each PR judge how likely it is genuinely abandoned — the author has walked away and the thread is over — so closing it due to inactivity is the right call.

Score HIGH when:

- WAITING: the maintainer's request is clear, directed at the author, and nothing came back — no commits, no replies, no rebase.
- CHANGES-REQUESTED: the requested changes were substantive, the author never engaged with them, and the silence since reads as abandonment.

Score LOW when:

- The ball is actually with the MAINTAINERS: the author addressed the feedback, pushed commits, or asked a question that was never answered — closing would punish the wrong side.
- The last activity is recent, or the author explicitly said they will return to it.
- The PR is approved or a maintainer committed to landing it ("we'll merge this after X") — that is a merge waiting to happen, not an abandonment.
- The requested changes were disputed and the dispute was never resolved — the thread needs a maintainer's answer, not a close.

Be conservative: closing a PR whose author did everything asked of them tells contributors their work goes nowhere. When who the ball is with is unclear, score low.

Respond with ONLY a JSON array, one entry per PR, no other text:
[{"number": <int>, "confidence": <0.0 to 1.0>, "reason": "<one sentence: who the ball is with and why the PR is or is not abandoned>"}]

Pull requests:
