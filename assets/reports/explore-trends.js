// prawn explore — the trends tab, in tfpp's shape: a catalogue of metrics
// over the filtered PRs, a tree on the left to pick them, presets that add
// bundles, one panel per selection, and panels that combine by dragging one
// onto another (same unit shares the axis, a second unit goes right), stack,
// split, or drop a metric from the legend. The selection lives in the hash.

// ---- the catalogue: every metric is a series over the bucket grid ----
// unit: prs | events | pct | days | ratio. area groups the tree. compute(ctx) -> values[]
const REVIEW_NAMES = ['never reviewed', 'commented only', 'changes requested', 'approved'];
const REVIEW_DESC = ['no maintainer has reviewed or commented yet', 'a maintainer commented or reviewed without a verdict', 'the last maintainer verdict was changes requested', 'the last maintainer verdict was an approval'];
const REVIEW_COLORS = ['var(--other)', 'var(--s1)', 'var(--s4)', 'var(--s3)'];
// the review-by-whom categories: index 0 is no review; then verdict (1 commented, 2 changes, 3 approved) × who (1 member, 2 partner, 3 both)
const WHO_SIDES = [null, D.maintainerGroup || 'members', D.partnerGroup || 'partners', 'both'];
const WHO_CATS = [{ label: 'no review', desc: 'no member or partner has reviewed or commented', color: 'var(--other)' }];
for (let v = 1; v <= 3; v++) for (let w = 1; w <= 3; w++) {
  const base = REVIEW_COLORS[v], verb = ['', 'commented', 'changes requested', 'approved'][v];
  const color = w === 1 ? base : w === 2 ? `color-mix(in srgb, ${base} 55%, var(--surface))` : `color-mix(in srgb, ${base} 70%, var(--ink))`;
  WHO_CATS.push({ v, w, label: `${verb} by ${WHO_SIDES[w]}`, desc: `the strongest verdict on the PR is "${verb}", given by ${WHO_SIDES[w] === 'both' ? 'both a member and a partner' : 'the ' + WHO_SIDES[w] + ' group'}`, color });
}
// a packed status code (members*4 + partners) → the who category index
function whoCat(code) {
  const m = code >> 2, p = code & 3;
  if (!m && !p) return 0;
  const v = Math.max(m, p), w = (m === v ? 1 : 0) + (p === v ? 2 : 0);
  return 1 + (v - 1) * 3 + (w - 1);
}
const UNIT_FMT = { prs: v => fmtNum(v), events: v => fmtNum(v), pct: v => fmtNum(v, 1) + '%', days: v => fmtDays(v), ratio: v => fmtNum(v, 2) };
function metricCatalogue() {
  const M_ = [];
  const add = (key, label, unit, area, compute, desc, color) => M_.push({ key, label, unit, area, compute, desc, color });
  const per = (ctx, pick, agg) => ctx.bs.map((_, i) => agg(ctx.byBucket[i].filter(pick)));
  const count = pick => ctx => per(ctx, pick, ps => ps.length);
  const med = (pick, f) => ctx => per(ctx, pick, ps => median(ps.map(f)));
  const mean = (pick, f) => ctx => per(ctx, pick, ps => ps.length ? ps.reduce((n, p) => n + f(p), 0) / ps.length : null);
  const share = (pick, test) => ctx => per(ctx, pick, ps => ps.length ? 100 * ps.filter(test).length / ps.length : null);
  const atStart = (arr, ctx) => ctx.bs.map(t => { const i = Math.floor(t / DAY) - ctx.daily.d0; return i >= 0 && i < ctx.daily.n ? arr[i] : null; });
  const open = p => p.s === 'open';

  // backlog: what was open on the first day of each bucket
  add('backlog.open', 'Open PRs', 'prs', 'backlog', ctx => atStart(ctx.dailyTotal, ctx), 'every PR open on the first day of the bucket, from the daily state replay', 'var(--s1)');
  add('backlog.drafts', 'Drafts', 'prs', 'backlog', ctx => atStart(ctx.daily.counts[0], ctx), 'open PRs in draft', 'var(--other)');
  add('backlog.nodraft', 'Open PRs (no drafts)', 'prs', 'backlog', ctx => atStart(ctx.dailyTotal, ctx).map((v, i) => v == null ? null : v - (atStart(ctx.daily.counts[0], ctx)[i] || 0)), 'open PRs that are not drafts', 'var(--s1)');
  // review status: the last verdict the members gave, from their reviews and comments only
  REVIEW_NAMES.forEach((n, i) => add('status.' + i, 'Status · ' + n, 'prs', 'pr status', ctx => atStart(ctx.dailyBy.status[i], ctx), REVIEW_DESC[i] + ' (the ' + D.maintainerGroup + ' group)', REVIEW_COLORS[i]));
  // who reviewed: the strongest verdict either side gave, and which side(s) gave it
  WHO_CATS.forEach((c, i) => add('who.' + i, 'Review · ' + c.label, 'prs', 'review by whom', ctx => atStart(ctx.dailyBy.who[i], ctx), c.desc, c.color));
  add('backlog.blocked_ms', 'Blocked', 'prs', 'backlog', ctx => atStart(ctx.dailyBy.span['ms:blocked'] || new Int32Array(ctx.daily.n), ctx), 'open PRs on the Blocked milestone: waiting on upstream, the API, a decision', 'var(--s8)');
  STATE_NAMES.forEach((n, i) => add('backlog.' + n, 'Open · ' + n, 'prs', 'backlog states', ctx => atStart(ctx.daily.counts[i], ctx), STATE_DESC[i], STATE_COLORS[i]));
  add('backlog.court.maintainer', "Open · maintainers' court", 'prs', 'pr court', ctx => atStart(ctx.daily.court[0], ctx), 'awaiting, approved or blocked: the next move is a maintainer\'s', 'var(--s1)');
  add('backlog.court.author', "Open · author's court", 'prs', 'pr court', ctx => atStart(ctx.daily.court[1], ctx), 'draft or waiting: the next move is the author\'s', 'var(--s4)');
  for (const g of [...GROUP_NAMES, 'community']) add('backlog.group.' + g, 'Open · ' + g, 'prs', 'backlog by group', ctx => atStart(ctx.dailyBy.group[g], ctx), 'open PRs whose author is in ' + g, groupColor(g));
  for (let e = 1; e <= 5; e++) add('backlog.effort.' + e, 'Open · effort ' + e, 'prs', 'backlog by effort', ctx => atStart(ctx.dailyBy.effort[e], ctx), 'open PRs with review effort ' + e + '/5');

  // flow: what happened in each bucket
  add('flow.opened', 'Opened', 'prs', 'flow', count(() => true), 'PRs opened in the bucket', 'var(--s1)');
  add('flow.merged', 'Merged', 'prs', 'flow', ctx => ctx.bs.map((_, i) => ctx.mergedIn[i].length), 'PRs merged in the bucket', 'var(--s7)');
  add('flow.closed', 'Closed unmerged', 'prs', 'flow', ctx => ctx.bs.map((_, i) => ctx.closedIn[i].length), 'PRs closed without merging in the bucket', 'var(--s8)');
  add('flow.resolved', 'Resolved', 'prs', 'flow', ctx => ctx.bs.map((_, i) => ctx.mergedIn[i].length + ctx.closedIn[i].length), 'merged + closed');
  add('flow.net', 'Net change', 'prs', 'flow', ctx => ctx.bs.map((_, i) => ctx.byBucket[i].length - ctx.mergedIn[i].length - ctx.closedIn[i].length), 'opened − resolved: the backlog\'s growth');
  add('flow.merge_rate', 'Merge rate', 'pct', 'flow', ctx => per(ctx, p => p.s !== 'open', ps => ps.length ? 100 * ps.filter(p => p.s === 'merged').length / ps.length : null), 'of the PRs opened in the bucket and since resolved, the share merged');
  add('flow.still_open', 'Still open', 'pct', 'flow', share(() => true, open), 'of the PRs opened in the bucket, the share still open');
  for (const g of [...GROUP_NAMES, 'community']) add('flow.group.' + g, 'Opened · ' + g, 'prs', 'flow by group', count(p => p.g === g), 'PRs opened by ' + g + ' authors', groupColor(g));
  for (const g of [...GROUP_NAMES, 'community']) add('flow.merged.' + g, 'Merged · ' + g, 'prs', 'merged by group', ctx => ctx.bs.map((_, i) => ctx.mergedIn[i].filter(p => p.g === g).length), 'PRs by ' + g + ' authors merged in the bucket', groupColor(g));
  for (const k of KINDS) add('flow.kind.' + k, 'Opened · ' + k, 'prs', 'flow by kind', count(p => p.k.includes(k)), 'PRs opened touching ' + k);
  add('flow.drafts', 'Opened as draft', 'pct', 'flow', share(() => true, p => p.iv.length && p.iv[0].st === 0), 'share of PRs opened in draft');
  const M_NAME = D.maintainerGroup || 'members', P_NAME = D.partnerGroup || 'partners';
  add('flow.reviewed.member', `Reviewed (${M_NAME})`, 'events', 'flow', ctx => ctx.evSide(e => e.k === 'review' && e.x !== 'approved', 'm'), `reviews submitted by the ${M_NAME} group, approvals apart`, 'var(--s3)');
  add('flow.reviewed.partner', `Reviewed (${P_NAME})`, 'events', 'flow', ctx => ctx.evSide(e => e.k === 'review' && e.x !== 'approved', 'p'), `reviews submitted by the ${P_NAME} group, approvals apart`, 'var(--s5)');
  add('flow.approved.member', `Approved (${M_NAME})`, 'events', 'flow', ctx => ctx.evSide(e => e.k === 'review' && e.x === 'approved', 'm'), `approvals by the ${M_NAME} group`, 'var(--s6)');
  add('flow.approved.partner', `Approved (${P_NAME})`, 'events', 'flow', ctx => ctx.evSide(e => e.k === 'review' && e.x === 'approved', 'p'), `approvals by the ${P_NAME} group`, 'var(--s2)');

  // quality by author group: how much member work a group's PRs need. Two placements, since neither is "the" date:
  // by the bucket the PR was opened in (a cohort — what those PRs went on to need; resolved PRs only, so young
  // ones do not drag recent buckets down), and by the bucket the review was left in (the load, as it landed).
  const GROUPS = [...GROUP_NAMES, 'community'];
  const resolvedOf = g => p => p.g === g && p.s !== 'open';
  for (const g of GROUPS) add('quality.reviews.' + g, 'Member reviews per PR · ' + g, 'ratio', 'quality by group (cohort)', mean(resolvedOf(g), p => p.mrv), `mean reviews by the ${M_NAME} group on the ${g} PRs opened in the bucket and since resolved`, groupColor(g));
  for (const g of GROUPS) add('quality.review_comments.' + g, 'Member review comments per PR · ' + g, 'ratio', 'quality by group (cohort)', mean(resolvedOf(g), p => p.mrc), `mean inline comments on ${M_NAME} reviews of the ${g} PRs opened in the bucket and since resolved`, groupColor(g));
  for (const g of GROUPS) add('quality.rounds.' + g, 'Rounds per PR · ' + g, 'ratio', 'quality by group (cohort)', mean(resolvedOf(g), p => p.rr), `mean changes-requested rounds by the ${M_NAME} group on the ${g} PRs opened in the bucket and since resolved`, groupColor(g));
  for (const g of GROUPS) add('quality.clean.' + g, 'Merged clean · ' + g, 'pct', 'quality by group (cohort)', ctx => ctx.bs.map((_, i) => { const ps = ctx.mergedIn[i].filter(p => p.g === g); return ps.length ? 100 * ps.filter(p => p.rr === 0).length / ps.length : null; }), `of the ${g} PRs merged in the bucket, the share that never had changes requested by a member`, groupColor(g));
  for (const g of GROUPS) add('load.reviews.' + g, 'Member reviews on · ' + g, 'events', 'review load by group (activity)', ctx => ctx.evCount(e => e.k === 'review', true, p => p.g === g), `reviews the ${M_NAME} group left on ${g} PRs in the bucket`, groupColor(g));
  for (const g of GROUPS) add('load.review_comments.' + g, 'Member review comments on · ' + g, 'events', 'review load by group (activity)', ctx => ctx.evSum(e => e.k === 'review' ? e.n || 0 : 0, p => p.g === g), `inline comments on ${M_NAME} reviews of ${g} PRs, by the bucket the review was left in`, groupColor(g));
  for (const g of GROUPS) add('load.comments.' + g, 'Member comments on · ' + g, 'events', 'review load by group (activity)', ctx => ctx.evCount(e => e.k === 'comment', true, p => p.g === g), `conversation comments the ${M_NAME} group left on ${g} PRs in the bucket`, groupColor(g));

  // times: how long things took, by the bucket the PR was opened in (response) or resolved in
  add('time.first_response', 'First response', 'days', 'times', med(p => p.fr >= 0, p => p.fr), 'median days from open to the first maintainer review, comment, or close, by bucket opened');
  add('time.first_response_p90', 'First response p90', 'days', 'times', ctx => per(ctx, p => p.fr >= 0, ps => { const a = ps.map(p => p.fr).sort((x, y) => x - y); return a.length ? a[Math.min(a.length - 1, Math.floor(a.length * 0.9))] : null; }), '90th percentile of the first response, by bucket opened');
  add('time.never', 'Never answered', 'pct', 'times', share(() => true, p => p.fr < 0), 'share of PRs opened in the bucket with no maintainer response yet');
  add('time.to_merge', 'Time to merge', 'days', 'times', ctx => ctx.bs.map((_, i) => median(ctx.mergedIn[i].map(p => p.td))), 'median days open, by bucket merged');
  add('time.to_close', 'Time to close', 'days', 'times', ctx => ctx.bs.map((_, i) => median(ctx.closedIn[i].map(p => p.td))), 'median days open, by bucket closed unmerged');
  add('time.age_open', 'Age of the open set', 'days', 'times', ctx => ctx.bs.map(t => median(ctx.M.filter(p => p.c <= t && (!p.x || p.x > t)).map(p => (t - p.c) / DAY))), 'median age of everything open on the first day of the bucket');
  add('time.waiting', 'Waiting-response days', 'days', 'times', ctx => ctx.bs.map((_, i) => median(ctx.mergedIn[i].filter(p => p.wc > 0).map(p => p.wd))), 'median days under waiting-response, of the merged PRs that ever wore it');
  add('time.in_court', "Days in maintainers' court", 'days', 'times', ctx => ctx.bs.map(t => median(ctx.M.filter(p => p.s === 'open' && p.ct === 'maintainer' && p.cs <= t).map(p => (Math.min(NOW, t + ctx.step) - p.cs) / DAY))), 'median days the currently-open PRs in the maintainers\' court had been there');

  // review: the work
  add('review.rounds', 'Rounds to merge', 'ratio', 'review', ctx => ctx.bs.map((_, i) => ctx.mergedIn[i].length ? ctx.mergedIn[i].reduce((n, p) => n + p.rr, 0) / ctx.mergedIn[i].length : null), 'mean changes-requested rounds of the PRs merged in the bucket');
  add('review.reviews', 'Reviews', 'events', 'review', ctx => ctx.evCount(e => e.k === 'review'), 'maintainer reviews submitted in the bucket');
  add('review.approvals', 'Approvals', 'events', 'review', ctx => ctx.evCount(e => e.k === 'review' && e.x === 'approved'), 'approvals in the bucket');
  add('review.changes', 'Changes requested', 'events', 'review', ctx => ctx.evCount(e => e.k === 'review' && e.x === 'changes_requested'), 'changes-requested reviews in the bucket');
  add('review.comments', 'Maintainer comments', 'events', 'review', ctx => ctx.evCount(e => e.k === 'comment'), 'conversation comments by maintainers in the bucket');
  add('review.touches', 'Maintainer touches', 'events', 'review', ctx => ctx.evCount(e => ['review', 'comment', 'merge', 'close'].includes(e.k)), 'reviews + comments + merges + closes by maintainers');
  add('review.prs_touched', 'PRs touched', 'prs', 'review', ctx => ctx.bs.map((_, i) => ctx.touched[i].size), 'distinct PRs a maintainer reviewed or commented on in the bucket');
  add('review.author_pushes', 'Author pushes', 'events', 'review', ctx => ctx.evCount(e => e.k === 'commit' || e.k === 'force-push', false), 'commits and force pushes by non-maintainers');
  add('waiting.labelled', 'Waiting-response labelled', 'pct', 'review', share(() => true, p => p.wc > 0), 'share of PRs opened in the bucket that were ever labelled waiting-response');
  add('waiting.cycles', 'Waiting cycles', 'ratio', 'review', mean(p => p.wc > 0, p => p.wc), 'mean waiting-response cycles, of the PRs opened in the bucket that had any');
  for (const r of D.maintainers) add('reviewer.' + r, 'Touches · ' + r, 'events', 'reviewers', ctx => ctx.evCount(e => (e.w || '').toLowerCase() === r && ['review', 'comment', 'merge', 'close'].includes(e.k)), 'reviews, comments, merges and closes by ' + r);
  for (const r of D.maintainers) add('responder.' + r, 'First responses · ' + r, 'prs', 'first responders', count(p => (p.fw || '').toLowerCase() === r), 'PRs opened in the bucket whose first maintainer response was ' + r + '\'s');

  // labels: the open backlog wearing each label of interest, and every size label seen
  const labelCounts = {}; for (const p of D.prs) if (p.s === 'open') for (const l of p.l) labelCounts[l] = (labelCounts[l] || 0) + 1;
  const wanted = ['bug', 'enhancement', 'breaking-change', 'waiting-response', 'documentation', 'good first issue', 'upstream/microsoft', 'upstream/terraform'].filter(l => labelCounts[l] != null);
  const sizes = Object.keys(labelCounts).filter(l => /^size\//i.test(l)).sort((a, b) => ['XS', 'S', 'M', 'L', 'XL', 'XXL'].indexOf(a.split('/')[1]) - ['XS', 'S', 'M', 'L', 'XL', 'XXL'].indexOf(b.split('/')[1]));
  const others = Object.keys(labelCounts).filter(l => !wanted.includes(l) && !sizes.includes(l)).sort((a, b) => labelCounts[b] - labelCounts[a]).slice(0, 20);
  for (const l of wanted) add('label.' + l, 'Open · ' + l, 'prs', 'labels', ctx => atStart(ctx.dailyBy.span[l] || new Int32Array(ctx.daily.n), ctx), 'open PRs wearing the ' + l + ' label, from the label events');
  for (const l of sizes) add('label.' + l, 'Open · ' + l, 'prs', 'size labels', ctx => atStart(ctx.dailyBy.span[l] || new Int32Array(ctx.daily.n), ctx), 'open PRs wearing ' + l);
  for (const l of others) add('label.' + l, 'Open · ' + l, 'prs', 'other labels', ctx => atStart(ctx.dailyBy.span[l] || new Int32Array(ctx.daily.n), ctx), 'open PRs wearing ' + l);

  // services: the open backlog per service
  const svcCounts = {}; for (const p of D.prs) if (p.s === 'open') for (const sv of p.sv) svcCounts[sv] = (svcCounts[sv] || 0) + 1;
  for (const sv of Object.keys(svcCounts).sort((a, b) => svcCounts[b] - svcCounts[a]).slice(0, 25)) {
    add('svc.open.' + sv, 'Open · ' + sv, 'prs', 'services (open)', ctx => atStart(ctx.dailyBy.svc[sv] || new Int32Array(ctx.daily.n), ctx), 'open PRs touching internal/services/' + sv);
    add('svc.opened.' + sv, 'Opened · ' + sv, 'prs', 'services (opened)', count(p => p.sv.includes(sv)), 'PRs opened touching internal/services/' + sv);
  }
  return M_;
}
const METRICS = metricCatalogue();
const METRIC = Object.fromEntries(METRICS.map(m => [m.key, m]));
const METRICS_SIZE_KEYS = METRICS.filter(m => m.area === 'size labels').map(m => m.key);
const AREAS = [...new Set(METRICS.map(m => m.area))];

const PRESETS = [
  ['open', 'open PRs, drafts apart', ['backlog.nodraft', 'backlog.drafts'], true],
  ['status', 'never reviewed · commented · changes · approved', ['status.0', 'status.1', 'status.2', 'status.3'], true],
  ['who reviewed', 'no review · commented · changes · approved, by member / partner / both', WHO_CATS.map((_, i) => 'who.' + i), true],
  ['court', 'whose move it is', ['backlog.court.maintainer', 'backlog.court.author'], true],
  ['labels', 'bug · enhancement · breaking-change', ['label.bug', 'label.enhancement', 'label.breaking-change'], false],
  ['sizes', 'open PRs by size label', METRICS_SIZE_KEYS, true],
  ['backlog', 'open PRs stacked by state', ['backlog.draft', 'backlog.awaiting', 'backlog.waiting', 'backlog.approved', 'backlog.blocked'], true],
  ['flow', 'opened · merged · closed · reviewed · approved', ['flow.opened', 'flow.merged', 'flow.closed', 'flow.reviewed.member', 'flow.reviewed.partner', 'flow.approved.member', 'flow.approved.partner'], false],
  ['times', 'response · merge · close', ['time.first_response', 'time.to_merge', 'time.to_close'], false],
  ['groups', 'opened by group', [...GROUP_NAMES, 'community'].map(g => 'flow.group.' + g), true],
  ['review load', 'touches per reviewer', D.maintainers.slice(0, 8).map(r => 'reviewer.' + r), true],
  ['quality: reviews', 'member reviews per PR, by author group and month opened', [...GROUP_NAMES, 'community'].map(g => 'quality.reviews.' + g), false],
  ['quality: review comments', 'member review comments per PR, by author group and month opened', [...GROUP_NAMES, 'community'].map(g => 'quality.review_comments.' + g), false],
  ['quality: rounds', 'changes-requested rounds per PR, by author group', [...GROUP_NAMES, 'community'].map(g => 'quality.rounds.' + g), false],
  ['review load by group', 'member review comments left on each group\'s PRs, by month left', [...GROUP_NAMES, 'community'].map(g => 'load.review_comments.' + g), true],
  ['waiting', 'waiting-response', ['waiting.labelled', 'waiting.cycles'], false],
  ['health', 'never answered · still open · merge rate', ['time.never', 'flow.still_open', 'flow.merge_rate'], false],
];
// the partner work panel: member reviews, review comments, and comments on the partner group's PRs, by the month
// they were left — twice, as lines and as stacked bars. A preset with a fifth element lays out several panels
// in the hash's m= grammar instead of one combined panel.
if (D.partnerGroup && GROUP_NAMES.includes(D.partnerGroup)) {
  const P = D.partnerGroup, keys = ['load.reviews.' + P, 'load.review_comments.' + P, 'load.comments.' + P];
  PRESETS.push([P + ': member work', `member reviews · review comments · comments on ${P} PRs, as lines and as stacked bars`, keys, false, `w:${keys.join(',')}|sbw:${keys.join(',')}`]);
}
// the default: the open count, the flow, who reviewed, opened by group, then the quality panels — member reviews and
// review comments per PR by author group, and the member work on the partner group's PRs as lines and as stacked bars
const PARTNER_WORK = PRESETS.find(p => p[4]);
const DEFAULT_M = 's:backlog.nodraft,backlog.drafts|sb:flow.opened,flow.merged,flow.closed,flow.reviewed.member,flow.reviewed.partner,flow.approved.member,flow.approved.partner|sw:' + WHO_CATS.map((_, i) => 'who.' + i).join(',') + '|sb:' + [...GROUP_NAMES, 'community'].map(g => 'flow.group.' + g).join(',')
  + '|' + [...GROUP_NAMES, 'community'].map(g => 'quality.reviews.' + g).join(',') + '|' + [...GROUP_NAMES, 'community'].map(g => 'quality.review_comments.' + g).join(',')
  + (PARTNER_WORK ? '|' + PARTNER_WORK[4] : '');

// ---- selection state: groups (panels) of keys, stacked or not, from the hash's m=; a metric may sit in several panels ----
const T = { groups: [], colors: {} };
const colorOf = (gi, key) => T.colors[gi + ':' + key] || 'var(--ink-2)';
// a group in the hash: an optional flag prefix — s: stacked, b: bars, sb: stacked bars, w: wide — then the keys, ! marking the right axis, ~ a hidden series
function parseGroups(str) {
  return (str || '').split('|').filter(Boolean).map(g => { const m = g.match(/^([swbt]+):/); const flags = m ? m[1] : ''; const body = m ? g.slice(m[0].length) : g; return { stack: flags.includes('s'), bars: flags.includes('b'), wide: flags.includes('w'), table: flags.includes('t'), keys: body.split(',').map(k => ({ key: k.replace(/^[!~]+/, ''), right: k.includes('!'), hidden: k.includes('~') })).filter(k => METRIC[k.key]) }; }).filter(g => g.keys.length);
}
function groupsString() { return T.groups.map(g => { const f = (g.stack ? 's' : '') + (g.bars ? 'b' : '') + (g.table ? 't' : '') + (g.wide ? 'w' : ''); return (f ? f + ':' : '') + g.keys.map(k => (k.right ? '!' : '') + (k.hidden ? '~' : '') + k.key).join(','); }).join('|'); }
const CHART_TYPES = [['line', 'line'], ['stacked', 'stacked area'], ['bars', 'bars'], ['stacked-bars', 'stacked bars'], ['table', 'table']];
const chartType = g => g.table ? 'table' : (g.stack ? 'stacked' : '') + (g.bars ? (g.stack ? '-bars' : 'bars') : '') || 'line';
// m= in the hash: '' is the default set, 'none' an emptied one (so clear sticks), anything else the selection
function syncGroups() { T.groups = T.groups.filter(g => g.keys.length); assignColors(); const gs = groupsString(); S.m = gs === DEFAULT_M ? '' : gs === '' ? 'none' : gs; }
function selectedKeys() { return [...new Set(T.groups.flatMap(g => g.keys.map(k => k.key)))]; }
// colours are per panel: a metric's preferred colour when it has one, else the palette in order, skipping colours taken in
// that panel — unless every metric in the panel prefers the same colour (one group's several series), which would be unreadable
function assignColors() {
  T.colors = {};
  T.groups.forEach((g, gi) => {
    const prefs = g.keys.map(k => METRIC[k.key].color);
    const samePref = g.keys.length > 1 && prefs.every(c => c && c === prefs[0]);
    const used = new Set(samePref ? [] : prefs.filter(Boolean));
    let i = 0;
    for (const k of g.keys) {
      const pref = samePref ? null : METRIC[k.key].color;
      if (pref) { T.colors[gi + ':' + k.key] = pref; continue; }
      while (used.has(PALETTE[i % PALETTE.length]) && i < PALETTE.length) i++;
      T.colors[gi + ':' + k.key] = PALETTE[i % PALETTE.length]; used.add(PALETTE[i % PALETTE.length]); i++;
    }
  });
}
function addKeys(keys, combined, stack) {
  keys = keys.filter(k => METRIC[k]); if (!keys.length) return;
  if (combined) T.groups.push({ stack, keys: keys.map(key => ({ key, right: false })) });
  else for (const key of keys) T.groups.push({ stack: false, keys: [{ key, right: false }] });
  syncGroups();
}
function addKeyTo(gi, key) { const g = T.groups[gi]; if (!g || !METRIC[key] || g.keys.some(k => k.key === key)) return; g.keys.push({ key, right: false }); syncGroups(); }
function removeKeys(keys) { for (const g of T.groups) g.keys = g.keys.filter(k => !keys.includes(k.key)); syncGroups(); }
function removeKeyFrom(gi, key) { const g = T.groups[gi]; if (!g) return; g.keys = g.keys.filter(k => k.key !== key); syncGroups(); }
function mergeGroups(from, to) { if (from === to || !T.groups[from] || !T.groups[to]) return; const f = T.groups[from]; T.groups[to].keys.push(...f.keys); T.groups.splice(from, 1); syncGroups(); }
function moveGroup(from, to, after) { if (from === to || !T.groups[from] || !T.groups[to]) return; const [g] = T.groups.splice(from, 1); let i = T.groups.indexOf(T.groups[to > from ? to - 1 : to]); if (i < 0) i = T.groups.length; T.groups.splice(after ? i + 1 : i, 0, g); syncGroups(); }
function splitGroup(i) { const g = T.groups[i]; if (!g) return; T.groups.splice(i, 1, ...g.keys.map(k => ({ stack: false, keys: [{ ...k, right: false }] }))); syncGroups(); }

// ---- a panel's title, guessed from what its metrics share ----
// a preset's exact key set takes the preset's name; metrics with one "X · " prefix become "X · a, b, c";
// metrics of one area become "area: a, b, c"; anything else is the labels joined
function panelTitle(g) {
  const keys = g.keys.map(k => k.key);
  if (keys.length === 1) return METRIC[keys[0]].label;
  const set = new Set(keys);
  const preset = PRESETS.find(([, , pk]) => pk.length === set.size && pk.every(k => set.has(k)));
  if (preset) return preset[0] + ' · ' + preset[1];
  const ms = keys.map(k => METRIC[k]);
  const join = parts => parts.length > 4 ? parts.slice(0, 3).join(', ') + ` +${parts.length - 3}` : parts.join(', ');
  const split = ms.map(m => m.label.split(' · '));
  if (split.every(x => x.length === 2) && new Set(split.map(x => x[0])).size === 1) return split[0][0] + ' · ' + join(split.map(x => x[1]));
  if (new Set(ms.map(m => m.area)).size === 1) return ms[0].area + ': ' + join(ms.map(m => m.label));
  return join(ms.map(m => m.label));
}

// ---- the context every metric computes from: the bucket grid over the filtered set ----
function metricContext() {
  const from = PERIOD.from, to = PERIOD.to;
  const gran = S.gran, step = GRAN_STEP[gran] || DAY;
  let bs = buckets(from, to, gran);
  // the bucket in progress would read as a collapse in every flow metric; drop it until it is mostly over
  if (bs.length > 2 && to - bs[bs.length - 1] < step * 0.6) bs = bs.slice(0, -1);
  const byBucket = bs.map(() => []), mergedIn = bs.map(() => []), closedIn = bs.map(() => []), touched = bs.map(() => new Set());
  const evBuckets = bs.map(() => []);
  for (const p of M) {
    let i;
    if (p.c >= from && (i = bucketIndex(bs, p.c)) >= 0) byBucket[i].push(p);
    if (p.m && p.m >= from && (i = bucketIndex(bs, p.m)) >= 0) mergedIn[i].push(p);
    if (p.s === 'closed' && p.x >= from && (i = bucketIndex(bs, p.x)) >= 0) closedIn[i].push(p);
    for (const e of p.ev) {
      if (e.t < from || (i = bucketIndex(bs, e.t)) < 0) continue;
      const w = (e.w || '').toLowerCase(), maint = MAINT.has(w) && w !== p.lo, side = maint ? 'm' : PARTNERS.has(w) && w !== p.lo ? 'p' : '';
      evBuckets[i].push({ e, maint, side, p });
      if (maint && (e.k === 'review' || e.k === 'comment')) touched[i].add(p.n);
    }
  }
  const evCount = (test, maintOnly = true, onPR = () => true) => bs.map((_, i) => evBuckets[i].filter(x => (maintOnly ? x.maint : !x.maint) && onPR(x.p) && test(x.e)).length);
  const evSum = (weight, onPR = () => true) => bs.map((_, i) => evBuckets[i].reduce((n, x) => n + (x.maint && onPR(x.p) ? weight(x.e) : 0), 0));
  const evSide = (test, side) => bs.map((_, i) => evBuckets[i].filter(x => x.side === side && test(x.e)).length);
  const daily = dailyStates(M, from, to);
  const dailyTotal = new Int32Array(daily.n); for (let i = 0; i < daily.n; i++) for (const c of daily.counts) dailyTotal[i] += c[i];
  const dailyBy = { group: {}, effort: {}, svc: {}, status: REVIEW_NAMES.map(() => new Int32Array(daily.n)), who: WHO_CATS.map(() => new Int32Array(daily.n)), span: {} };
  const bumpRange = (arr, s0, e0) => { const [a, b] = dayRange(s0, e0, daily.d0, daily.d0 + daily.n - 1); for (let d = a; d <= b; d++) arr[d - daily.d0]++; };
  const bump = (arr, p) => { for (const iv of p.iv) bumpRange(arr, iv.s, iv.e); };
  for (const g of [...GROUP_NAMES, 'community']) dailyBy.group[g] = new Int32Array(daily.n);
  for (let e = 1; e <= 5; e++) dailyBy.effort[e] = new Int32Array(daily.n);
  for (const p of M) {
    if (dailyBy.group[p.g]) bump(dailyBy.group[p.g], p);
    bump(dailyBy.effort[p.ef], p);
    for (const sv of p.sv) { if (!dailyBy.svc[sv]) dailyBy.svc[sv] = new Int32Array(daily.n); bump(dailyBy.svc[sv], p); }
    for (const iv of (p.rs || [])) { bumpRange(dailyBy.status[iv.st >> 2], iv.s, iv.e); bumpRange(dailyBy.who[whoCat(iv.st)], iv.s, iv.e); }
    for (const sp of (p.ls || [])) { const k = sp.n.startsWith('ms:') ? sp.n.toLowerCase() : sp.n; if (!dailyBy.span[k]) dailyBy.span[k] = new Int32Array(daily.n); bumpRange(dailyBy.span[k], sp.s, sp.e); }
  }
  return { M, from, to, gran, bs, step, byBucket, mergedIn, closedIn, touched, evCount, evSum, evSide, daily, dailyTotal, dailyBy, label: bucketLabel(gran) };
}

// ---- rendering: the tree, the controls, the panels ----
const openNodes = new Set(['enabled', 'presets', 'backlog', 'pr status']);
const collapsed = new Set(); // panel indexes folded in the enabled list
let treeFilter = '';
function renderMetrics(view) {
  T.groups = S.m === 'none' ? [] : parseGroups(S.m || DEFAULT_M); assignColors();
  const ctx = metricContext();
  const cache = {}; const values = key => cache[key] || (cache[key] = METRIC[key].compute(ctx));
  tabControls(`<span>grid <select id="t-cols">${['1', '2', '3'].map(c => `<option value="${c}"${S.cols === c ? ' selected' : ''}>${c}</option>`).join('')}</select></span>
    <span class="seg" id="t-gran">${['day', 'week', 'month'].map(g => `<button data-v="${g}" class="${S.gran === g ? 'on' : ''}">${g}</button>`).join('')}</span>
    <span>markers <span class="seg" id="t-marks">${[['major', 'majors'], ['minor', 'minors'], ['none', 'none']].map(([v, l]) => `<button data-v="${v}" class="${S.marks === v ? 'on' : ''}">${l}</button>`).join('')}</span></span>
    <span>y <span class="seg" id="t-zero">${[['1', 'from zero'], ['0', 'fit']].map(([v, l]) => `<button data-v="${v}" class="${S.zero === v ? 'on' : ''}">${l}</button>`).join('')}</span></span>
    <span>dots <span class="seg" id="t-dots">${[['auto', 'auto'], ['1', 'on'], ['0', 'off']].map(([v, l]) => `<button data-v="${v}" class="${S.dots === v ? 'on' : ''}">${l}</button>`).join('')}</span></span>
    <span class="seg" id="t-tv">${[['charts', 'charts'], ['table', 'table']].map(([v, l]) => `<button data-v="${v}" class="${S.tv === v ? 'on' : ''}">${l}</button>`).join('')}</span>`);
  const seg = (id, key) => $(id).addEventListener('click', e => { const b = e.target.closest('button'); if (b) set(key, b.dataset.v); });
  seg('#t-gran', 'gran'); seg('#t-marks', 'marks'); seg('#t-zero', 'zero'); seg('#t-dots', 'dots'); seg('#t-tv', 'tv');
  $('#t-cols').addEventListener('change', e => set('cols', e.target.value));

  view.innerHTML = `<div class="tlayout"><aside id="tree"></aside><div class="splitter" id="splitter"></div><div class="tmain"><div class="panels" id="tpanels" data-cols="${S.cols}"></div><details class="tsection" id="tcompare"><summary>compare two dates · every enabled metric side by side</summary><div class="body"></div></details></div></div>`;
  renderTree(); if (S.tv === 'table') renderTable(ctx, values); else renderPanels(ctx, values); renderCompare(ctx, values);
  // the splitter drags the sidebar's width
  const sp = $('#splitter'), lay = $('.tlayout');
  sp.addEventListener('mousedown', e => { e.preventDefault(); document.body.classList.add('resizing'); const mv = ev => { lay.style.setProperty('--side', Math.max(180, Math.min(520, ev.clientX - lay.getBoundingClientRect().left)) + 'px'); }; const up = () => { document.body.classList.remove('resizing'); removeEventListener('mousemove', mv); removeEventListener('mouseup', up); renderPanels(ctx, values); }; addEventListener('mousemove', mv); addEventListener('mouseup', up); });
}
function refresh() { syncGroups(); writeHash(); update(); }

// one panel as a table: its metrics per bucket, latest first, the stacked ones with a total
function panelTable(el, ctx, g) {
  const cols = g.keys.map(k => ({ m: METRIC[k.key], vals: METRIC[k.key].compute(ctx) }));
  const rows = ctx.bs.map((t, i) => i).reverse().slice(0, 200);
  const total = g.stack && cols.length > 1;
  el.querySelector('.chart').innerHTML = `<div style="overflow:auto; max-height:320px"><table><thead><tr><th>${S.gran}</th>${cols.map(c => `<th class="num" title="${esc(c.m.desc)}">${esc(c.m.label)}</th>`).join('')}${total ? '<th class="num">total</th>' : ''}</tr></thead><tbody>
    ${rows.map(i => `<tr><td>${ctx.label(ctx.bs[i])}</td>${cols.map(c => `<td class="num">${c.vals[i] == null ? '—' : UNIT_FMT[c.m.unit](c.vals[i])}</td>`).join('')}${total ? `<td class="num"><b>${fmtNum(cols.reduce((n, c) => n + (c.vals[i] || 0), 0))}</b></td>` : ''}</tr>`).join('')}</tbody></table></div>`;
  el.querySelector('.legend').innerHTML = `<span class="dim">${rows.length} of ${ctx.bs.length} ${S.gran}s, latest first</span>`;
}

// ---- the table view: every enabled metric per bucket, latest first ----
function renderTable(ctx, values) {
  const host = $('#tpanels'); host.dataset.cols = '1';
  const keys = selectedKeys(); if (!keys.length) { host.innerHTML = '<div class="panel wide"><div class="empty">pick metrics in the tree, or a preset</div></div>'; return; }
  const cols = keys.map(k => ({ k, m: METRIC[k], vals: values(k) }));
  const rows = ctx.bs.map((t, i) => i).reverse().slice(0, 400);
  host.innerHTML = `<div class="panel wide table"><h2>table<span class="desc">${keys.length} metrics over ${ctx.bs.length} ${S.gran}s, latest first${ctx.bs.length > 400 ? ', the last 400' : ''}</span><span class="pbtns"><button class="pbtn" id="t-csv">copy as csv</button></span></h2>
    <div style="overflow:auto; max-height:70vh"><table><thead><tr><th>${S.gran}</th>${cols.map(c => `<th class="num" title="${esc(c.m.desc)}">${esc(c.m.label)}<br><span class="dim">${c.m.unit}</span></th>`).join('')}</tr></thead><tbody>
    ${rows.map(i => `<tr><td>${ctx.label(ctx.bs[i])}</td>${cols.map(c => `<td class="num">${c.vals[i] == null ? '—' : UNIT_FMT[c.m.unit](c.vals[i])}</td>`).join('')}</tr>`).join('')}</tbody></table></div></div>`;
  $('#t-csv').onclick = () => { const csv = [[S.gran, ...cols.map(c => c.m.label)].join(','), ...ctx.bs.map((t, i) => [fmtDate(t), ...cols.map(c => c.vals[i] == null ? '' : c.vals[i])].join(','))].join('\n'); navigator.clipboard?.writeText(csv); $('#t-csv').textContent = 'copied'; };
}

// ---- compare two dates: every enabled metric side by side, with the change ----
function renderCompare(ctx, values) {
  const el = $('#tcompare .body'); if (!el) return;
  const keys = selectedKeys(); const n = ctx.bs.length;
  const at = d => d ? bucketIndex(ctx.bs, Date.parse(d + 'T00:00:00Z') / 1000) : -1;
  const a = at(S.ca) >= 0 ? at(S.ca) : Math.max(0, n - 1 - (S.gran === 'day' ? 30 : S.gran === 'week' ? 8 : 3));
  const b = at(S.cb) >= 0 ? at(S.cb) : n - 1;
  el.innerHTML = `<div class="toolbar"><input type="date" id="c-a" value="${fmtDate(ctx.bs[a])}"> <span class="dim">→</span> <input type="date" id="c-b" value="${fmtDate(ctx.bs[b])}"></div>
    <table><thead><tr><th>metric</th><th class="num">${ctx.label(ctx.bs[a])}</th><th class="num">${ctx.label(ctx.bs[b])}</th><th class="num">change</th></tr></thead><tbody>
    ${keys.map(k => { const m = METRIC[k], v = values(k), f = UNIT_FMT[m.unit]; return `<tr><td title="${esc(m.desc)}">${esc(m.label)} <span class="dim">${m.unit}</span></td><td class="num">${v[a] == null ? '—' : f(v[a])}</td><td class="num">${v[b] == null ? '—' : f(v[b])}</td><td class="num">${fmtDelta(v[b], v[a], f)}</td></tr>`; }).join('')}
    ${!keys.length ? '<tr><td colspan="4" class="empty">no metrics enabled</td></tr>' : ''}</tbody></table>`;
  $('#c-a').onchange = e => { S.ca = e.target.value; renderCompare(ctx, values); };
  $('#c-b').onchange = e => { S.cb = e.target.value; renderCompare(ctx, values); };
}
function renderTree() {
  const tree = $('#tree'); const sel = selectedKeys();
  const row = (m, extra = '', color = 'var(--ink-2)', on = sel.includes(m.key)) => `<label class="opt ${on ? 'on' : ''}" style="--c:${color}" data-key="${esc(m.key)}" title="${esc(m.desc)}" draggable="true"><span class="sw"></span>${esc(m.label)}<span class="unit">${m.unit}</span>${extra}</label>`;
  const node = (id, title, count, body) => `<details ${openNodes.has(id) ? 'open' : ''} data-node="${id}"><summary>${title}<span class="n">${count}</span></summary><div class="sec">${body}</div></details>`;
  let html = `<input type="search" id="tree-filter" placeholder="filter metrics…" value="${esc(treeFilter)}">
    <div class="actions"><button class="plain" id="tree-clear">clear</button><button class="plain" id="tree-defaults">defaults</button></div>`;
  // enabled: one block per panel, its metrics under it — drag a metric from the catalogue onto a panel to add it there too
  const enabled = T.groups.map((g, gi) => `<div class="pgroup ${collapsed.has(gi) ? 'closed' : ''}" data-g="${gi}"><div class="phead" title="click to fold"><span class="pn">${gi + 1}</span>${esc(panelTitle(g))}${g.stack ? ' <span class="unit">stacked</span>' : ''}<span class="rm" data-rmg="${gi}" title="remove the panel">×</span></div>
      <div class="prows">${g.keys.map(k => row(METRIC[k.key], `${g.keys.length > 1 ? `<span class="lr" title="axis"><button data-lr="L" data-key="${esc(k.key)}" data-g="${gi}" class="${k.right ? '' : 'on'}">L</button><button data-lr="R" data-key="${esc(k.key)}" data-g="${gi}" class="${k.right ? 'on' : ''}">R</button></span>` : ''}<span class="rm" data-rm="${esc(k.key)}" data-g="${gi}">×</span>`, colorOf(gi, k.key), true)).join('')}</div></div>`).join('');
  html += node('enabled', 'enabled', T.groups.length + (T.groups.length === 1 ? ' panel' : ' panels'), T.groups.length ? enabled : '<div class="none">nothing selected — pick from below, or drag a metric onto a panel</div>');
  html += node('presets', 'presets', PRESETS.length, PRESETS.map(([name, desc, keys, stack]) => `<label class="opt preset" data-preset="${esc(name)}" title="${esc(keys.join(', '))}"><span class="sw all"></span>${esc(name)}<span class="desc">${esc(desc)}</span></label>`).join(''));
  const f = treeFilter.toLowerCase();
  for (const area of AREAS) {
    const ms = METRICS.filter(m => m.area === area && (!f || m.label.toLowerCase().includes(f) || m.key.includes(f)));
    if (!ms.length) continue;
    html += node(area, area, ms.length, ms.map(m => row(m)).join(''));
  }
  tree.innerHTML = html;
  tree.querySelectorAll('details').forEach(d => d.addEventListener('toggle', () => { if (d.open) openNodes.add(d.dataset.node); else openNodes.delete(d.dataset.node); }));
  $('#tree-filter').addEventListener('input', e => { treeFilter = e.target.value; for (const d of tree.querySelectorAll('details')) if (treeFilter) d.open = true; renderTree(); $('#tree-filter').focus(); const v = $('#tree-filter'); v.setSelectionRange(v.value.length, v.value.length); });
  $('#tree-clear').addEventListener('click', () => { T.groups = []; refresh(); });
  $('#tree-defaults').addEventListener('click', () => { T.groups = parseGroups(DEFAULT_M); refresh(); });
  tree.addEventListener('contextmenu', e => { const lab = e.target.closest('label.opt[data-key]'); if (lab) showInfo(e, lab.dataset.key); });
  tree.addEventListener('click', e => {
    const lr = e.target.closest('button[data-lr]'); if (lr) { e.preventDefault(); const g = T.groups[+lr.dataset.g]; const k = g && g.keys.find(x => x.key === lr.dataset.key); if (k) { k.right = lr.dataset.lr === 'R'; refresh(); } return; }
    const rm = e.target.closest('.rm'); if (rm) { e.preventDefault(); if (rm.dataset.rmg != null) T.groups.splice(+rm.dataset.rmg, 1); else if (rm.dataset.g != null) removeKeyFrom(+rm.dataset.g, rm.dataset.rm); else removeKeys([rm.dataset.rm]); refresh(); return; }
    const ph = e.target.closest('.phead'); if (ph) { const gi = +ph.parentElement.dataset.g; if (collapsed.has(gi)) collapsed.delete(gi); else collapsed.add(gi); ph.parentElement.classList.toggle('closed'); return; }
    const pre = e.target.closest('label.preset'); if (pre) { e.preventDefault(); const p = PRESETS.find(x => x[0] === pre.dataset.preset); removeKeys(p[2]); if (p[4]) { T.groups.push(...parseGroups(p[4])); syncGroups(); } else addKeys(p[2], true, p[3]); refresh(); return; }
    const lab = e.target.closest('label.opt[data-key]'); if (!lab || lab.closest('.pgroup')) return; e.preventDefault();
    const k = lab.dataset.key; if (selectedKeys().includes(k)) removeKeys([k]); else addKeys([k], false, false); refresh();
  });
  // a metric row dragged out of the tree lands on a panel (added there) or on the panel area's empty space (a new panel)
  tree.querySelectorAll('label.opt[draggable]').forEach(l => l.addEventListener('dragstart', e => { e.dataTransfer.setData('text/x-prawn-metric', l.dataset.key); e.dataTransfer.effectAllowed = 'copy'; }));
}
function renderPanels(ctx, values) {
  const host = $('#tpanels'); host.dataset.cols = S.cols;
  const groups = T.groups;
  if (!groups.length) { host.innerHTML = '<div class="panel wide"><div class="empty">pick metrics in the tree, or a preset</div></div>'; return; }
  host.innerHTML = groups.map((g, gi) => {
    const units = [...new Set(g.keys.map(k => METRIC[k.key].unit))];
    const title = panelTitle(g);
    const desc = g.keys.length === 1 ? METRIC[g.keys[0].key].desc : `${g.keys.length} metrics · ${units.join(' × ')}`;
    const sameUnit = new Set(g.keys.map(k => { const u = METRIC[k.key].unit; return u === 'events' ? 'prs' : u; })).size === 1;
    const btns = `<select class="ptype" data-g="${gi}" title="chart type">${CHART_TYPES.filter(([v]) => sameUnit || !v.startsWith('stacked')).map(([v, l]) => `<option value="${v}"${chartType(g) === v ? ' selected' : ''}>${l}</option>`).join('')}</select>${g.keys.length > 1 ? `<button class="pbtn" data-act="split" data-g="${gi}">split</button>` : ''}<button class="pbtn ${g.wide ? 'on' : ''}" data-act="wide" data-g="${gi}">wide</button><button class="pbtn" data-act="rm" data-g="${gi}">×</button>`;
    return `<div class="panel ${g.wide ? 'wide' : ''}" draggable="true" data-g="${gi}"><h2><span class="handle">⠿</span>${esc(title)}<span class="desc">${esc(desc)}</span><span class="pbtns">${btns}</span></h2><div class="chart"></div><div class="legend"></div></div>`;
  }).join('');
  groups.forEach((g, gi) => {
    const el = host.querySelector(`.panel[data-g="${gi}"]`);
    // prs and events are both plain counts and share an axis; days, pct, and ratio each need their own
    const axisOf = u => (u === 'prs' || u === 'events') ? 'count' : u;
    const axes = [...new Set(g.keys.map(k => axisOf(METRIC[k.key].unit)))];
    const title = panelTitle(g);
    const leftAxis = axes[0], rightAxis = axes.find(u => u !== leftAxis);
    const series = g.keys.map((k, si) => { const m = METRIC[k.key]; return { key: k.key, label: m.label, color: colorOf(gi, k.key), values: values(k.key), stack: g.stack, bars: g.bars, barIndex: si, hidden: !!k.hidden, right: k.right || (rightAxis && axisOf(m.unit) === rightAxis) }; });
    const fmtFor = axis => axis === 'count' ? UNIT_FMT.prs : UNIT_FMT[axis];
    const fmt = fmtFor(leftAxis), rfmt = rightAxis ? fmtFor(rightAxis) : fmt;
    const leftUnit = leftAxis;
    if (g.table) { panelTable(el, ctx, g); return; }
    timeChart({ chart: el.querySelector('.chart'), legend: el.querySelector('.legend') }, ctx.bs, series, { step: ctx.step, label: ctx.label, fmt, rightFmt: rfmt, h: g.wide ? 240 : 190, zero: S.zero === '1' && leftUnit !== 'days', title, barGroups: g.bars && !g.stack ? g.keys.length : 1 });
  });
  // one handler, set (not added) so re-renders never stack them
  host.onclick = e => {
    const rm = e.target.closest('.legend .rm'); if (rm) { removeKeyFrom(+rm.closest('.panel').dataset.g, rm.dataset.rm); refresh(); return; }
    const le = e.target.closest('.legend span[data-key]'); if (le && le.dataset.key) { const g = T.groups[+le.closest('.panel').dataset.g]; const k = g && g.keys.find(x => x.key === le.dataset.key); if (k) { k.hidden = !k.hidden; refresh(); } return; }
    const b = e.target.closest('.pbtn'); if (!b) return; const gi = +b.dataset.g, g = T.groups[gi]; if (!g) return;
    if (b.dataset.act === 'split') splitGroup(gi); else if (b.dataset.act === 'rm') T.groups.splice(gi, 1); else if (b.dataset.act === 'wide') g.wide = !g.wide;
    refresh();
  };
  host.oncontextmenu = e => { const le = e.target.closest('.legend span[data-key]'); if (le && le.dataset.key) { const g = T.groups[+le.closest('.panel').dataset.g]; const i = ctx.bs.length - 1; const v = values(le.dataset.key)[i]; showInfo(e, le.dataset.key, `<div class="now">now <b>${v == null ? '—' : UNIT_FMT[METRIC[le.dataset.key].unit](v)}</b> · in panel ${T.groups.indexOf(g) + 1}</div>`); } };
  host.onchange = e => {
    const sel = e.target.closest('select.ptype'); if (!sel) return; const g = T.groups[+sel.dataset.g]; if (!g) return;
    g.table = sel.value === 'table'; if (!g.table) { g.stack = sel.value.startsWith('stacked'); g.bars = sel.value.endsWith('bars'); } refresh();
  };
  // drag a panel: onto the middle of another to combine them, onto its left or right edge to move before or after it
  let dragging = null;
  // the edges move, the middle combines: left and top edges put the dragged panel before, right and bottom after
  const zone = (p, e) => { const r = p.getBoundingClientRect(), x = (e.clientX - r.left) / r.width, y = (e.clientY - r.top) / r.height; return x < 0.22 || y < 0.2 ? 'before' : x > 0.78 || y > 0.8 ? 'after' : 'over'; };
  const isMetricDrag = e => e.dataTransfer.types.includes('text/x-prawn-metric');
  host.querySelectorAll('.panel[draggable="true"]').forEach(p => {
    // a drag starts from the title bar only (the chart keeps the mouse for range selection); dragstart's target is the
    // panel itself, so where the mouse went down is remembered on mousedown
    let fromTitle = false;
    p.addEventListener('mousedown', e => { fromTitle = !!e.target.closest('h2') && !e.target.closest('button, select'); });
    p.addEventListener('dragstart', e => { if (!fromTitle) { e.preventDefault(); return; } dragging = +p.dataset.g; p.classList.add('dragging'); e.dataTransfer.effectAllowed = 'move'; });
    p.addEventListener('dragend', () => { dragging = null; host.querySelectorAll('.panel').forEach(x => x.classList.remove('dragging', 'over', 'before', 'after')); });
    p.addEventListener('dragover', e => {
      if (isMetricDrag(e)) { e.preventDefault(); p.classList.add('over'); return; }
      if (dragging == null || +p.dataset.g === dragging) return; e.preventDefault(); const z = zone(p, e); p.classList.toggle('over', z === 'over'); p.classList.toggle('before', z === 'before'); p.classList.toggle('after', z === 'after');
    });
    p.addEventListener('dragleave', () => p.classList.remove('over', 'before', 'after'));
    p.addEventListener('drop', e => {
      e.preventDefault(); e.stopPropagation();
      if (isMetricDrag(e)) { addKeyTo(+p.dataset.g, e.dataTransfer.getData('text/x-prawn-metric')); refresh(); return; }
      if (dragging == null) return; const z = zone(p, e); if (z === 'over') mergeGroups(dragging, +p.dataset.g); else moveGroup(dragging, +p.dataset.g, z === 'after'); refresh();
    });
  });
  // a metric dropped on the empty space of the panel area becomes its own panel
  host.ondragover = e => { if (isMetricDrag(e)) { e.preventDefault(); host.classList.add('over'); } };
  host.ondragleave = e => { if (e.target === host) host.classList.remove('over'); };
  host.ondrop = e => { host.classList.remove('over'); if (isMetricDrag(e)) { e.preventDefault(); addKeys([e.dataTransfer.getData('text/x-prawn-metric')], false, false); refresh(); } };
}

boot();
