// prawn explore — the page script. D is the embedded data set (see
// lib/explore). One global filter over every PR; each tab renders a view of
// the matched set. Every control lives in the url hash, so a view is a link.
'use strict';

const DAY = 86400;
const NOW = D.now;
const MAINT = new Set(D.maintainers.map(s => s.toLowerCase()));
const PARTNERS = new Set((D.partners || []).map(s => s.toLowerCase()));
const GROUP_NAMES = Object.keys(D.groups || {}).sort();
const STATE_NAMES = ['draft', 'awaiting', 'waiting', 'approved', 'blocked'];
const STATE_COLORS = ['var(--other)', 'var(--s1)', 'var(--s4)', 'var(--s3)', 'var(--s2)']; // blocked is orange: red is ci's, striped over the bar
const STATE_DESC = ['draft — not up for review yet', 'awaiting — the maintainers\' court', 'waiting — the author\'s court (waiting-response, or changes requested and nothing pushed)', 'approved — waiting on a merge', 'blocked — the Blocked milestone'];
const PALETTE = ['var(--s1)', 'var(--s2)', 'var(--s3)', 'var(--s4)', 'var(--s5)', 'var(--s7)', 'var(--s6)', 'var(--s8)'];
const KINDS = ['schema', 'code', 'tests', 'docs', 'examples', 'contributing', 'vendor', 'ci', 'changelog', 'other'];

const $ = (sel, el = document) => el.querySelector(sel);
const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const fmtDate = u => u ? new Date(u * 1000).toISOString().slice(0, 10) : '';
const fmtDays = d => d == null || d < 0 ? '—' : d < 1 ? '<1d' : d < 60 ? Math.round(d) + 'd' : d < 730 ? (d / 30.4).toFixed(1) + 'mo' : (d / 365).toFixed(1) + 'y';
const fmtNum = (v, dp = 0) => v == null || Number.isNaN(v) ? '—' : Number(v).toLocaleString(undefined, { maximumFractionDigits: dp });
const pct = (a, b) => b ? Math.round(100 * a / b) + '%' : '—';
const median = xs => { const a = xs.filter(x => x != null && !Number.isNaN(x)).sort((x, y) => x - y); if (!a.length) return null; const m = a.length >> 1; return a.length % 2 ? a[m] : (a[m - 1] + a[m]) / 2; };
const daysSince = u => u ? (NOW - u) / DAY : null;
const groupColor = g => { const i = GROUP_NAMES.indexOf(g); return i < 0 ? 'var(--other)' : PALETTE[i % PALETTE.length]; };
const prURL = n => `https://github.com/${D.repo}/pull/${n}`;

// ---- derived per-PR fields the filters and sorts read ----
for (const p of D.prs) {
  for (const k of ['l', 'sv', 'k', 'rw', 'li', 'iv', 'ev', 'rs', 'ls', 'rb', 'ab', 'cb']) if (!Array.isArray(p[k])) p[k] = [];
  p.age = (NOW - p.c) / DAY;
  p.idle = (NOW - p.la) / DAY;
  p.size = p.ad + p.de;
  p.cd = p.cs ? (NOW - p.cs) / DAY : null; // days in the current court
  p.lo = p.a.toLowerCase();
  p.tl = p.t.toLowerCase();
  p.lastMaintBy = ''; p.lastAuthorBy = '';
  // the members' work on the PR: their reviews, the inline comments on those reviews, and their conversation comments
  p.mrv = 0; p.mrc = 0; p.mcm = 0;
  for (const e of p.ev) { const w = (e.w || '').toLowerCase(); if (!MAINT.has(w) || w === p.lo) continue; if (e.k === 'review') { p.mrv++; p.mrc += e.n || 0; } else if (e.k === 'comment') p.mcm++; }
  for (let i = p.ev.length - 1; i >= 0; i--) { const e = p.ev[i]; if (MAINT.has((e.w || '').toLowerCase()) && e.w.toLowerCase() !== p.lo && ['review', 'comment', 'label+', 'label-', 'close', 'merge', 'milestone+'].includes(e.k)) { p.lastMaintBy = e.w; break; } }
  p.aiMax = p.ai ? Math.max(...Object.values(p.ai)) : null;
  // approved by one of us: the replay's current state, which only counts the maintainers group's reviews —
  // github's own reviewDecision (p.rd) flips on any collaborator's approval
  p.approved = p.s === 'open' && p.iv.length > 0 && p.iv[p.iv.length - 1].st === 3;
  // ghp-sync's Status field: merged / closed, or the state an open PR is in today
  p.status = p.s !== 'open' ? p.s : STATE_NAMES[p.iv.length ? p.iv[p.iv.length - 1].st : 1];
  p.cbLogins = p.cb.map(c => c.l);
  p.checks = [];
}
const BY_N = new Map(D.prs.map(p => [p.n, p]));
for (const c of (D.checks || [])) { const p = BY_N.get(c.n); if (p) p.checks.push(c); }
for (const p of D.prs) p.checkNames = p.checks.map(c => c.check).join(',');

// ---- state: everything the url hash carries ----
const DEFAULTS = { tab: 'prs', from: D.viewFrom || D.since, to: '', st: null, g: '', a: '', svc: '', k: '', l: '', ct: '', ef: '', dr: '', q: '', sort: 'u', gb: '', gc: '', sh: '', dir: '', dc: '', sg: '', mo: '', pd: '', yr: '', ddc: '', cf: '', gran: 'day', by: '', psort: '', asort: '', marks: 'major', open_n: '', m: '', cols: '2', zero: '1', dots: 'auto', tv: 'charts', ca: '', cb: '' };
const S = { ...DEFAULTS };
// the state filter's default depends on the tab: open PRs everywhere, every state on trends — until it is set explicitly
const stateFilter = () => S.st ?? (S.tab === 'trends' || S.tab === 'data' ? '' : 'open');
function readHash() {
  const h = new URLSearchParams(location.hash.slice(1));
  for (const k in DEFAULTS) S[k] = h.has(k) ? h.get(k) : DEFAULTS[k];
}
function writeHash() {
  const h = new URLSearchParams();
  for (const k in DEFAULTS) if (S[k] !== DEFAULTS[k] && S[k] !== null) h.set(k, S[k]); // an empty value that differs from the default (state: any) is kept
  const s = '#' + h.toString();
  if (s !== location.hash) history.replaceState(null, '', s === '#' ? location.pathname : s);
}
function set(k, v) { S[k] = v; update(); }

// ---- the query language: key:value, key>n, key<n, -key:value, bare words ----
// keys: author group svc kind label court state effort age idle size files rounds fr reviewer n title draft decision milestone mergeable cd waiting thumbs check ai
const KEYS = {
  author: p => p.lo, a: p => p.lo, group: p => p.g, g: p => p.g, svc: p => p.sv, service: p => p.sv, s: p => p.sv,
  kind: p => p.kd || (p.s === 'open' ? 'other' : null), touches: p => p.k, k: p => p.k, only: p => p.k.length === 1 ? p.k[0] : 'mixed', label: p => p.l, l: p => p.l, court: p => p.ct, c: p => p.ct, state: p => p.s, st: p => p.s,
  effort: p => p.ef, e: p => p.ef, age: p => p.age, idle: p => p.idle, size: p => p.size, files: p => p.f, rounds: p => p.rr,
  tests: p => p.tc ? p.tc.s : 'none', testsfailed: p => p.tc ? p.tc.f : null, testage: p => testAge(p), testsince: p => testsSince(p), failedtest: p => p.tc && p.tc.ft ? p.tc.ft : [], tested: p => p.tc ? p.tc.b.map(b => b.sv) : [],
  ciage: p => ciAge(p), failingfor: p => failingFor(p), failing: p => p.cif || [], behind: p => p.bh ?? null, behindfor: p => behindFor(p), ahead: p => p.ah ?? null, drift: p => p.cdr ?? null,
  props: p => (p.pp || []).length, prop: p => p.pp || [], // how many schema properties the diff touches, and which
  fr: p => p.fr < 0 ? null : p.fr, reviewer: p => p.rw, r: p => p.rw, n: p => p.n, title: p => p.tl, t: p => p.tl,
  draft: p => p.d ? 'yes' : 'no', approved: p => p.approved ? 'yes' : 'no', decision: p => p.rd || 'none', rd: p => p.rd || 'none', milestone: p => p.ms || '', ms: p => p.ms || '',
  mergeable: p => p.mg, mg: p => p.mg, cd: p => p.cd, waiting: p => p.wd, wd: p => p.wd, thumbs: p => p.th, comments: p => p.cm,
  reviews: p => p.rv, approvals: p => p.ap, check: p => p.checkNames, ai: p => p.aiMax, mergedby: p => (p.mb || '').toLowerCase(), mb: p => (p.mb || '').toLowerCase(),
  responder: p => (p.fw || '').toLowerCase(), assoc: p => (p.as || '').toLowerCase(), lastmaint: p => p.lastMaintBy.toLowerCase(), lm: p => p.lastMaintBy.toLowerCase(),
  reviewedby: p => p.rb, rb: p => p.rb, approvedby: p => p.ab, ab: p => p.ab, changesby: p => p.cbLogins, cb: p => p.cbLogins,
  ci: p => p.ci || 'none', status: p => p.status, reviewcomments: p => p.rc, rc: p => p.rc,
  memberreviews: p => p.mrv, mrv: p => p.mrv, memberreviewcomments: p => p.mrc, mrc: p => p.mrc, membercomments: p => p.mcm, mcm: p => p.mcm,
  suggested: p => p.s === 'open' ? CATEGORIES.filter(c => c[2](p)).map(c => c[4]) : [], // the suggested tab's categories the PR falls in
};
function parseQuery(q) {
  const terms = [];
  for (const tok of (q || '').match(/(?:[^\s"]+|"[^"]*")+/g) || []) {
    let neg = false, t = tok;
    if (t.startsWith('-')) { neg = true; t = t.slice(1); }
    const m = t.match(/^([a-z_]+)(:|>=|<=|>|<|=)(.*)$/i);
    if (!m || !KEYS[m[1].toLowerCase()]) { terms.push({ neg, bare: t.replace(/"/g, '').toLowerCase() }); continue; }
    terms.push({ neg, key: m[1].toLowerCase(), op: m[2], vals: m[3].replace(/"/g, '').toLowerCase().split(',').filter(Boolean) });
  }
  return terms;
}
function matchTerm(p, t) {
  if (t.bare != null) return p.tl.includes(t.bare) || p.lo.includes(t.bare) || String(p.n) === t.bare;
  const v = KEYS[t.key](p);
  const num = typeof v === 'number';
  if (t.op === ':' || t.op === '=') {
    if (Array.isArray(v)) return t.vals.some(x => v.some(y => y.toLowerCase() === x || (t.op === ':' && y.toLowerCase().startsWith(x))));
    if (num) return t.vals.some(x => v === Number(x));
    if (v == null) return t.vals.includes('none') || t.vals.includes('');
    const sv = String(v).toLowerCase();
    return t.vals.some(x => sv === x || (t.op === ':' && sv.startsWith(x)));
  }
  if (v == null || !num) return false;
  const x = Number(t.vals[0]);
  if (Number.isNaN(x)) return false;
  return t.op === '>' ? v > x : t.op === '<' ? v < x : t.op === '>=' ? v >= x : v <= x;
}

// ---- the filter ----
let M = []; // the matched PRs
let PERIOD = { from: 0, to: NOW };
function applyFilter() {
  // the period, clamped to the data: a half-typed year in the date field must not become a million buckets
  const earliest = D.prs.reduce((m, p) => Math.min(m, p.c), NOW);
  let from = S.from ? Date.parse(S.from + 'T00:00:00Z') / 1000 : earliest;
  let to = S.to ? Date.parse(S.to + 'T23:59:59Z') / 1000 : NOW;
  if (S.tab === 'data') ({ from, to } = dataRange()); // the data tab's period is the window: a month, a year, or from/to
  if (!Number.isFinite(from) || from < earliest) from = earliest;
  if (!Number.isFinite(to) || to > NOW) to = NOW;
  if (to < from) to = from + DAY;
  PERIOD = { from, to };
  const states = stateFilter() ? stateFilter().split(',') : null;
  const kinds = S.k ? S.k.split(',') : null;
  const authors = S.a ? S.a.toLowerCase().split(',').map(s => s.trim().replace(/^@/, '')).filter(Boolean) : null;
  const svcs = S.svc ? S.svc.toLowerCase().split(',').map(s => s.trim()).filter(Boolean) : null;
  const labels = S.l ? S.l.toLowerCase().split(',').map(s => s.trim()).filter(Boolean) : null;
  const [efLo, efHi] = S.ef ? S.ef.split('-').map(Number) : [1, 5];
  const terms = parseQuery(S.q);
  M = D.prs.filter(p => {
    if (p.c > to) return false;
    if (p.x && p.x < from) return false;
    if (states && !states.includes(p.s)) return false;
    if (S.g && p.g !== S.g) return false;
    if (authors && !authors.some(a => p.lo === a || p.lo.startsWith(a))) return false;
    if (svcs && !svcs.some(s => p.sv.some(x => x === s || x.startsWith(s)))) return false;
    if (kinds && !(p.k.length && p.k.every(k => kinds.includes(k)))) return false; // only those kinds, nothing else
    if (labels && !labels.some(l => p.l.some(x => x.toLowerCase() === l || x.toLowerCase().startsWith(l)))) return false;
    if (S.ct && p.ct !== S.ct) return false;
    if (S.dr === 'yes' && !p.d) return false; // only the drafts
    if (S.dr === 'no' && p.d) return false; // no drafts
    if (p.ef < efLo || p.ef > efHi) return false;
    for (const t of terms) if (matchTerm(p, t) === !!t.neg) return false;
    return true;
  });
}

// ---- the filter bar ----
const STATE_MC = { open: 'var(--s3)', merged: 'var(--s7)', closed: 'var(--s8)' };
function segMulti(id, items, cur, colors) {
  const on = cur ? cur.split(',') : [];
  return `<div class="seg multi" id="${id}">${items.map(v => `<button data-v="${v}" class="${on.includes(v) ? 'on' : ''}" style="--mc:${(colors && colors[v]) || 'var(--ink-2)'}"><i></i>${v}</button>`).join('')}</div>`;
}
function renderFilters() {
  const el = $('#filters');
  const opt = (v, label, cur) => `<option value="${esc(v)}"${v === cur ? ' selected' : ''}>${esc(label)}</option>`;
  el.innerHTML = `
    ${S.tab === 'data' ? `<label>period<select id="f-pd">${opt('', 'month', S.pd)}${opt('year', 'year', S.pd)}${opt('custom', 'custom', S.pd)}</select></label>` : ''}
    ${S.tab === 'data' && S.pd === '' ? `<label>month<input type="month" id="f-mo" value="${dataMonth()}"></label>` : ''}
    ${S.tab === 'data' && S.pd === 'year' ? `<label>year<select id="f-yr">${dataYears().map(y => opt(y, y, dataYear())).join('')}</select></label>` : ''}
    ${S.tab !== 'data' || S.pd === 'custom' ? `<label>from<input type="date" id="f-from" value="${esc(S.from)}"></label>
    <label>to<input type="date" id="f-to" value="${esc(S.to)}"></label>` : ''}
    <label>state<div class="row">${segMulti('f-st', ['open', 'merged', 'closed'], stateFilter(), STATE_MC)}<div class="seg multi apart" id="f-dr"><button style="--mc:var(--ink-2)"><i></i>draft</button></div></div></label>
    <label>group<select id="f-g">${opt('', 'any', S.g)}${GROUP_NAMES.map(g => opt(g, g, S.g)).join('')}${opt('community', 'community', S.g)}</select></label>
    <div class="fl">author${picker('f-a', 'a', countBy(p => [p.a]), 'any author')}</div>
    <div class="fl">service${picker('f-svc', 'svc', countBy(p => p.sv), 'any service')}</div>
    <div class="fl">files${picker('f-k', 'k', KINDS.map(k => [k, D.prs.filter(p => p.k.length && p.k.every(x => x === k)).length]), 'any files', 'PRs made of only the picked kinds of file: schema (resource/data source .go), code (other .go), tests (_test.go), docs (website/, the provider docs), examples (examples/), contributing (contributing/, the contributor guide), vendor (vendor/, go.mod), ci (.github/, scripts/), changelog (.changelog/, CHANGELOG.md); the counts are PRs of exactly that one kind')}</div>
    <div class="fl">label${picker('f-l', 'l', countBy(p => p.l), 'any label')}</div>
    <label>court<select id="f-ct">${opt('', 'any', S.ct)}${opt('maintainer', 'maintainer', S.ct)}${opt('author', 'author', S.ct)}</select></label>
    <label class="grow">query<input class="wide" id="f-q" value="${esc(S.q)}" placeholder="author:x label:bug age>90 -kind:docs …  (enter)"></label>
    <span class="match"><span class="count"><b id="f-n"></b><span>of ${fmtNum(D.prs.length)} PRs</span></span> <button class="plain" id="f-clear">clear</button></span>
    `;
  const bind = (id, key, ev = 'change') => $(id).addEventListener(ev, e => set(key, e.target.value));
  const bindIf = (id, key) => { if ($(id)) bind(id, key); };
  bindIf('#f-from', 'from'); bindIf('#f-to', 'to'); bindIf('#f-mo', 'mo'); bindIf('#f-yr', 'yr'); bind('#f-g', 'g'); bind('#f-ct', 'ct'); bind('#f-q', 'q');
  // a month, a year, or from/to: each has other inputs, so the bar is drawn again
  if ($('#f-pd')) $('#f-pd').addEventListener('change', e => { S.pd = e.target.value; update(true); });
  bindPickers();
  $('#f-q').addEventListener('keydown', e => { if (e.key === 'Enter') set('q', e.target.value); });
  const togglePills = (id, key) => $(id).addEventListener('click', e => {
    const b = e.target.closest('button'); if (!b) return; const v = b.dataset.v;
    const val = key === 'st' ? stateFilter() : S[key];
    const cur = val ? val.split(',') : [];
    set(key, (cur.includes(v) ? cur.filter(x => x !== v) : [...cur, v]).join(','));
  });
  togglePills('#f-st', 'st');
  $('#f-dr').addEventListener('click', () => set('dr', DRAFT_NEXT[S.dr] ?? ''));
  syncDraft();
  $('#f-clear').addEventListener('click', () => { for (const k of ['from', 'to', 'st', 'g', 'a', 'svc', 'k', 'l', 'ct', 'ef', 'dr', 'q', 'sh', 'sort', 'dir']) S[k] = DEFAULTS[k]; update(true); }); // back to the page as it opens: every open PR, the most recently updated first
  $('#qhelp').innerHTML = `<details><summary>query keys</summary> — <code>key:value</code> matches (prefix for text, any of <code>a,b</code>), <code>key&gt;n</code> <code>key&lt;n</code> compare, <code>-key:value</code> excludes, bare words search title and author.
    keys: <code>author group svc kind touches only label court state status effort age idle size files props prop rounds fr cd waiting reviewer reviewedby approvedby changesby responder lastmaint mergedby assoc approved decision mergeable ci ciage failingfor failing behind behindfor drift tests testsfailed testage testsince failedtest tested milestone draft thumbs comments reviews reviewcomments memberreviews memberreviewcomments membercomments approvals check ai suggested n title</code> (<code>kind</code> is the sort of change — <code>kind:"1 property"</code>, <code>kind:resource</code>, <code>kind:"test fix"</code>; <code>touches:docs</code> touches docs files, <code>only:docs</code> is docs and nothing else; <code>approved</code> is a maintainer's, <code>decision</code> is github's; <code>status</code> is merged, closed, or an open PR's state today; <code>ci</code> is passing, failing, running, approval, expired, none; <code>ciage</code> and <code>failingfor</code> are days, <code>failing</code> a check's name, <code>behind</code> and <code>drift</code> commits against the base branch, <code>behindfor</code> the days since the branch last caught up with it; <code>tests</code> is teamcity's failing, running, passing, cancelled, none, <code>testsince</code> the commits pushed since they ran, <code>failedtest</code> a test's name, <code>tested</code> a service).
    e.g. <code>court:maintainer effort&lt;3 idle&gt;30</code> · <code>label:waiting-response cd&gt;60</code> · <code>fr:none state:open</code> · <code>reviewer:katbyte rounds&gt;2</code> · <code>approvedby:katbyte ci:passing</code> · <code>check:stale ai&gt;0.8</code> · <code>suggested:fixes</code> (${CATEGORIES.map(c => c[4]).join(', ')})</details>`;
}

// the draft button has three states, a click going round them: off (no drafts), lit (drafts shown with the
// rest, as the page opens), purple (only the drafts)
const DRAFT_NEXT = { no: '', '': 'yes', yes: 'no' };
const DRAFT_TITLE = { no: 'drafts are hidden — click to show them with the rest', '': 'drafts are shown with the rest — click for only the drafts', yes: 'only the drafts — click to hide drafts' };
function syncDraft() {
  const b = $('#f-dr button'); if (!b) return;
  b.classList.toggle('on', !S.dr); b.classList.toggle('only', S.dr === 'yes');
  b.title = DRAFT_TITLE[S.dr] ?? DRAFT_TITLE[''];
}
// the toggle buttons and pickers reflect the state after every change, without rebuilding the bar (which would drop focus)
function syncFilterButtons() {
  syncDraft();
  for (const [id, key] of [['#f-st', 'st']]) {
    const val = key === 'st' ? stateFilter() : S[key];
    const on = val ? val.split(',') : [];
    document.querySelectorAll(id + ' button').forEach(b => b.classList.toggle('on', on.includes(b.dataset.v)));
  }
  document.querySelectorAll('.picker[data-key]').forEach(syncPicker);
}

// a multi-select picker: a button naming the selection, a popover of searchable checkbox rows with counts
function countBy(items) { const c = {}; for (const p of D.prs) for (const v of items(p)) if (v) c[v] = (c[v] || 0) + 1; return Object.entries(c).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])); }
const selected = key => (S[key] ? S[key].split(',') : []).map(v => v.trim().toLowerCase()).filter(Boolean);
function picker(id, key, options, placeholder, title = '') {
  return `<span class="picker" id="${id}" data-key="${key}" data-placeholder="${esc(placeholder)}" title="${esc(title)}"><button class="plain pick" type="button"></button>
    <div class="pop" hidden><input type="search" placeholder="filter…"><div class="rows">${options.map(([v, n]) => `<div class="opt" data-v="${esc(v.toLowerCase())}"><span class="sw"></span><span class="name">${esc(v)}</span><span class="cnt">${fmtNum(n)}</span></div>`).join('')}</div></div></span>`;
}
function syncPicker(el) {
  const key = el.dataset.key, on = selected(key);
  el.querySelector('button.pick').textContent = on.length ? (on.length <= 2 ? on.join(', ') : `${on[0]} +${on.length - 1}`) : el.dataset.placeholder;
  el.querySelector('button.pick').classList.toggle('on', on.length > 0);
  el.querySelectorAll('.opt').forEach(l => l.classList.toggle('on', on.includes(l.dataset.v)));
}
function bindPickers() {
  document.querySelectorAll('.picker[data-key]').forEach(el => { // the filter bar's own: the tab row's pickers bind themselves
    const key = el.dataset.key, pop = el.querySelector('.pop'), search = pop.querySelector('input');
    syncPicker(el);
    el.querySelector('button.pick').addEventListener('click', () => { const open = pop.hidden; document.querySelectorAll('.picker .pop').forEach(p => { p.hidden = true; }); pop.hidden = !open; if (open) { search.value = ''; search.dispatchEvent(new Event('input')); search.focus(); } });
    search.addEventListener('input', () => { const f = search.value.toLowerCase(); pop.querySelectorAll('.opt').forEach(l => { l.hidden = !!f && !l.dataset.v.includes(f); }); });
    pop.addEventListener('click', e => {
      const l = e.target.closest('.opt'); if (!l) return; e.preventDefault();
      const on = selected(key), v = l.dataset.v;
      set(key, (on.includes(v) ? on.filter(x => x !== v) : [...on, v]).join(','));
    });
  });
  document.addEventListener('click', e => { if (!e.target.closest('.picker')) document.querySelectorAll('.picker .pop').forEach(p => { p.hidden = true; }); });
}

// ---- tabs ----
const TABS = [['prs', 'prs'], ['trends', 'trends'], ['suggested', 'suggested'], ['services', 'services'], ['people', 'people'], ['checks', 'checks'], ['data', 'data']];
function renderTabs() {
  const counts = { prs: queueRows().length, // the prs tab's show, and a grouping that drops PRs, narrow its table: so its count too
    trends: M.length, suggested: suggestions().reduce((n, c) => n + c.prs.length, 0), people: new Set(M.map(p => p.a)).size, services: new Set(M.flatMap(p => p.sv)).size, checks: checkSections(false).reduce((n, sec) => n + sec.prs.length, 0), data: monthRows().length };
  // the tab is the header's dropdown, where the page's name was; the controls row keeps the tab's own controls
  $('#tabSel').innerHTML = TABS.map(([id, label]) => `<option value="${id}"${S.tab === id ? ' selected' : ''}>${label} · ${fmtNum(counts[id])}</option>`).join('');
  $('#controls').innerHTML = `<span id="tabctl" style="display:contents"></span>`;
}
$('#tabSel').addEventListener('change', e => set('tab', e.target.value));
// per-tab controls slot in beside the tabs
const tabControls = html => { $('#tabctl').innerHTML = html; };

// ---- charts: hand-rolled svg, one tooltip ----
const tip = $('#tip');
function showTip(ev, html) { tip.innerHTML = html; tip.style.display = 'block'; const w = tip.offsetWidth, h = tip.offsetHeight; tip.style.left = Math.min(ev.clientX + 14, innerWidth - w - 8) + 'px'; tip.style.top = Math.min(ev.clientY + 14, innerHeight - h - 8) + 'px'; }
function hideTip() { tip.style.display = 'none'; }
function niceTicks(lo, hi, n) {
  if (hi === lo) hi = lo + (Math.abs(lo) || 1);
  const raw = (hi - lo) / n, mag = Math.pow(10, Math.floor(Math.log10(raw))), norm = raw / mag;
  const step = (norm <= 1 ? 1 : norm <= 2 ? 2 : norm <= 2.5 ? 2.5 : norm <= 5 ? 5 : 10) * mag;
  const out = []; for (let v = Math.ceil(lo / step) * step; v <= hi + step * 1e-6; v += step) out.push(+v.toFixed(10)); return out;
}
function monthTicks(t0, t1) {
  const out = []; const d = new Date(t0 * 1000); d.setUTCDate(1); d.setUTCHours(0, 0, 0, 0);
  const months = (t1 - t0) / (30 * DAY), step = months > 30 ? 6 : months > 18 ? 3 : months > 9 ? 2 : 1;
  // ticks land on months divisible by the step, so January (the year label) is always one of them
  d.setUTCMonth(d.getUTCMonth() + 1);
  while (d.getUTCMonth() % step) d.setUTCMonth(d.getUTCMonth() + 1);
  while (d.getTime() / 1000 <= t1) { out.push(d.getTime() / 1000); d.setUTCMonth(d.getUTCMonth() + step); }
  return out;
}
const monthLbl = t => { const d = new Date(t * 1000); return d.toLocaleString('en', { month: 'short', timeZone: 'UTC' }) + ' ' + String(d.getUTCFullYear()).slice(2); };
function markers(xs, pad, h) {
  if (S.marks === 'none' || !D.releases || !D.releases.length) return '';
  let g = '', rowEnd = [];
  for (const r of D.releases) {
    if (r.date < PERIOD.from || r.date > PERIOD.to) continue;
    const major = /^v?\d+\.0\.0$/.test(r.tag), minor = /^v?\d+\.\d+\.0$/.test(r.tag);
    if (S.marks === 'major' && !major) continue;
    if (S.marks === 'minor' && !major && !minor) continue;
    const x = xs(r.date);
    if (major) {
      g += `<line class="mark" stroke="var(--s8)" x1="${x}" x2="${x}" y1="${pad.t}" y2="${h - pad.b}"><title>${esc(r.tag)} ${fmtDate(r.date)}</title></line>`;
      const lx = x + 3; let row = rowEnd.findIndex(end => lx > end); if (row < 0) row = rowEnd.length; rowEnd[row] = lx + r.tag.length * 6 + 8;
      g += `<text class="marklbl" fill="var(--s8)" x="${lx}" y="${pad.t + 9 + row * 11}">${esc(r.tag)}</text>`;
    } else {
      g += `<line class="mark" stroke="var(--muted)" x1="${x}" x2="${x}" y1="${h - pad.b - 6}" y2="${h - pad.b}"><title>${esc(r.tag)} ${fmtDate(r.date)}</title></line>`;
    }
  }
  return g;
}
// series: [{label, color, values[], stack?, bars?, dash?}] over times[] (unix); stacked series pile up in order.
// Draws into el.chart and writes the legend (latest value and its change) into el.legend when given.
const fmtDelta = (a, b, fmt) => {
  if (a == null || b == null) return '';
  const d = a - b; if (Math.abs(d) < 1e-9) return '<span class="delta dim">±0</span>';
  const p = b ? ` (${d > 0 ? '+' : ''}${(100 * d / Math.abs(b)).toFixed(1)}%)` : '';
  return `<span class="delta ${d > 0 ? 'up' : 'down'}">${d > 0 ? '+' : '−'}${fmt(Math.abs(d))}${p}</span>`;
};
function legendHTML(series, i, fmt, rfmt) {
  return series.map(s => { const f = s.right && rfmt ? rfmt : fmt; const v = s.values[i], pv = i > 0 ? s.values[i - 1] : null; return `<span class="${s.hidden ? 'off' : ''}" data-key="${esc(s.key || '')}" title="click to hide or show · right-click for what it is"><i class="${s.stack || s.bars ? 'box' : ''}" style="--c:${s.color}"></i><span class="lbl">${esc(s.label)}</span>${s.right ? ' <span class="dim">(right)</span>' : ''} <b>${v == null ? '—' : f(v)}</b> ${fmtDelta(v, pv, f)}${s.key ? ` <span class="rm" data-rm="${esc(s.key)}" title="remove">×</span>` : ''}</span>`; }).join('');
}
function timeChart(els, times, series, opts = {}) {
  const el = els.chart || els, legend = els.legend;
  const fmt = opts.fmt || (v => fmtNum(v, 1));
  const w = Math.max(320, Math.round(el.getBoundingClientRect().width) || 700), h = opts.h || 190, pad = { l: 52, r: 14, t: 14, b: 24 };
  const t0 = times[0], t1 = times[times.length - 1] + (opts.step || 0);
  const xs = t => pad.l + (t - t0) / Math.max(t1 - t0, 1) * (w - pad.l - pad.r);
  const n = times.length;
  let base = new Array(n).fill(0);
  const drawn = series.map(s => {
    if (s.hidden) return { ...s, top: [], base: null };
    if (!s.stack) return { ...s, top: s.values, base: null };
    const top = s.values.map((v, i) => base[i] + (v || 0)); const b = base; base = top; return { ...s, top, base: b };
  });
  const hasRight = drawn.some(s => s.right);
  if (hasRight) pad.r = 58;
  const dom = ss => {
    let lo = Infinity, hi = -Infinity;
    for (const s of ss) for (const v of s.top) if (v != null) { if (v < lo) lo = v; if (v > hi) hi = v; }
    if (lo === Infinity) { lo = 0; hi = 1; }
    if (opts.zero === false) { const p = (hi - lo) * 0.08 || 1; lo -= p; hi += p; } else { lo = Math.min(0, lo); hi = Math.max(1e-9, hi) * 1.06; }
    return [lo, hi];
  };
  const [lo, hi] = dom(drawn.filter(s => !s.right)), [rlo, rhi] = dom(drawn.filter(s => s.right));
  const ysL = v => pad.t + (1 - (v - lo) / (hi - lo)) * (h - pad.t - pad.b);
  const ysR = v => pad.t + (1 - (v - rlo) / (rhi - rlo)) * (h - pad.t - pad.b);
  const ys = ysL, scaleOf = s => s.right ? ysR : ysL;
  const rfmt = opts.rightFmt || fmt;
  let g = '';
  for (const t of niceTicks(lo, hi, 4).filter(t => t >= lo && t <= hi)) g += `<line class="grid" x1="${pad.l}" x2="${w - pad.r}" y1="${ys(t)}" y2="${ys(t)}"/><text x="${pad.l - 6}" y="${ys(t) + 4}" text-anchor="end">${fmt(t)}</text>`;
  if (hasRight) for (const t of niceTicks(rlo, rhi, 4).filter(t => t >= rlo && t <= rhi)) g += `<text x="${w - pad.r + 6}" y="${ysR(t) + 4}">${rfmt(t)}</text>`;
  g += `<line class="axis" x1="${pad.l}" x2="${w - pad.r}" y1="${h - pad.b}" y2="${h - pad.b}"/>`;
  if (hasRight) g += `<line class="axis" x1="${w - pad.r}" x2="${w - pad.r}" y1="${pad.t}" y2="${h - pad.b}"/>`;
  for (const t of monthTicks(t0, t1)) g += `<text x="${xs(t)}" y="${h - 6}" text-anchor="middle">${monthLbl(t)}</text>`;
  const bw = Math.max(1, (w - pad.l - pad.r) / n * 0.8 / (opts.barGroups || 1));
  drawn.forEach((s, si) => {
    const sy = scaleOf(s);
    const pts = []; s.top.forEach((v, i) => { if (v != null) pts.push([xs(times[i]), sy(v), i]); });
    if (!pts.length) return;
    const path = pts.map((p, i) => (i ? 'L' : 'M') + p[0].toFixed(1) + ' ' + p[1].toFixed(1)).join(' ');
    if (s.bars) {
      // grouped bars side by side, or stacked bars from each series' base
      const off = (s.barIndex || 0) * bw - ((opts.barGroups || 1) - 1) * bw / 2;
      const x0 = s.stack ? -bw / 2 : -bw / 2 + off;
      for (const p of pts) { const y0 = s.stack ? sy(s.base[p[2]]) : sy(0); g += `<rect class="bar" fill="${s.color}" x="${(p[0] + x0).toFixed(1)}" y="${Math.min(p[1], y0).toFixed(1)}" width="${bw.toFixed(1)}" height="${Math.abs(y0 - p[1]).toFixed(1)}" rx="${bw > 3 ? 2 : 0}"/>`; }
    } else if (s.stack) {
      const back = pts.slice().reverse().map(p => 'L' + p[0].toFixed(1) + ' ' + sy(s.base[p[2]]).toFixed(1)).join(' ');
      g += `<path fill="${s.color}" opacity=".8" stroke="var(--surface)" stroke-width="1.5" d="${path} ${back} Z"/>`;
    } else {
      g += `<path class="line" stroke="${s.color}" ${s.dash ? 'stroke-dasharray="4 3"' : ''} d="${path}"/>`;
      const dotsOn = S.dots === '1' || (S.dots === 'auto' && pts.length < 40 && pts.length < n);
      if (dotsOn && pts.length <= 400) for (const p of pts) g += `<circle class="dot" cx="${p[0]}" cy="${p[1]}" r="3" fill="${s.color}"/>`;
    }
    g += `<circle class="dot hov" data-s="${si}" cx="0" cy="0" r="4" fill="${s.color}" opacity="0"/>`;
  });
  g += markers(xs, pad, h);
  g += `<line class="cross" x1="0" x2="0" y1="${pad.t}" y2="${h - pad.b}"/><rect class="hit" x="${pad.l}" y="0" width="${w - pad.l - pad.r}" height="${h}"/>`;
  el.innerHTML = `<svg viewBox="0 0 ${w} ${h}" width="${w}" height="${h}">${g}</svg>`;
  const svg = el.firstChild, cross = svg.querySelector('.cross'), dots = [...svg.querySelectorAll('.dot.hov')];
  const last = n - 1;
  if (legend) legend.innerHTML = legendHTML(series, last, fmt, rfmt);
  const label = opts.label || fmtDate;
  svg.addEventListener('mousemove', ev => {
    const r = svg.getBoundingClientRect(), x = (ev.clientX - r.left) / r.width * w;
    let best = 0, bd = Infinity; for (let i = 0; i < n; i++) { const d = Math.abs(xs(times[i]) - x); if (d < bd) { bd = d; best = i; } }
    cross.setAttribute('x1', xs(times[best])); cross.setAttribute('x2', xs(times[best])); cross.style.opacity = 1;
    for (const d of dots) { const s = drawn[+d.dataset.s], v = s.top[best]; if (v == null || s.bars || s.hidden) { d.style.opacity = 0; continue; } d.setAttribute('cx', xs(times[best])); d.setAttribute('cy', scaleOf(s)(v)); d.style.opacity = 1; }
    let rows = series.map(s => { const f = s.right ? rfmt : fmt; const v = s.values[best], pv = best > 0 ? s.values[best - 1] : null; return `<div class="row"><span style="--c:${s.color}">${esc(s.label)}</span><span>${v == null ? '—' : f(v)} ${fmtDelta(v, pv, f)}</span></div>`; }).join('');
    // stacked series add up: a total row, with its own change
    const stacked = series.filter(s => s.stack && !s.hidden);
    if (stacked.length > 1) {
      const sum = i => stacked.reduce((n, s) => n + (s.values[i] || 0), 0);
      rows += `<div class="row total"><span>total</span><span>${fmt(sum(best))} ${fmtDelta(sum(best), best > 0 ? sum(best - 1) : null, fmt)}</span></div>`;
    }
    showTip(ev, `<b>${label(times[best])}</b>${rows}`);
    if (legend) legend.innerHTML = legendHTML(series, best, fmt, rfmt);
  });
  svg.addEventListener('mouseleave', () => { cross.style.opacity = 0; for (const d of dots) d.style.opacity = 0; hideTip(); if (legend) legend.innerHTML = legendHTML(series, last, fmt, rfmt); });
  // drag to select a range, right-click a point: both open the menu that asks an AI about it or narrows the page to it
  const nearest = ev => { const r = svg.getBoundingClientRect(), x = (ev.clientX - r.left) / r.width * w; let best = 0, bd = Infinity; for (let i = 0; i < n; i++) { const d = Math.abs(xs(times[i]) - x); if (d < bd) { bd = d; best = i; } } return best; };
  const chart = { times, series, fmt, rfmt, label, title: opts.title || '' };
  let selStart = null, selRect = null;
  svg.addEventListener('mousedown', ev => { if (ev.button !== 0) return; selStart = nearest(ev); selRect?.remove(); selRect = null; });
  svg.addEventListener('mousemove', ev => {
    if (selStart == null) return;
    const i = nearest(ev); if (i === selStart) return;
    if (!selRect) { selRect = document.createElementNS('http://www.w3.org/2000/svg', 'rect'); selRect.setAttribute('class', 'sel'); selRect.setAttribute('y', pad.t); selRect.setAttribute('height', h - pad.t - pad.b); svg.insertBefore(selRect, svg.querySelector('.hit')); }
    const a = Math.min(selStart, i), b = Math.max(selStart, i);
    selRect.setAttribute('x', xs(times[a])); selRect.setAttribute('width', Math.max(1, xs(times[b]) - xs(times[a])));
  });
  svg.addEventListener('mouseup', ev => { if (selStart == null) return; const i = nearest(ev); const a = Math.min(selStart, i), b = Math.max(selStart, i); selStart = null; if (a !== b) { showCtx(ev, chart, a, b); } else { selRect?.remove(); selRect = null; } });
  svg.addEventListener('contextmenu', ev => { ev.preventDefault(); const i = nearest(ev); showCtx(ev, chart, Math.max(0, i - 1), i, true); });
  svg.addEventListener('mouseleave', () => { selStart = null; });
}

// ---- the info popover: what a metric is, on a right-click of a legend entry or a tree row ----
const infoEl = (() => { const d = document.createElement('div'); d.id = 'info'; d.hidden = true; document.body.appendChild(d); return d; })();
function showInfo(ev, key, extra = '') {
  const m = typeof METRIC !== 'undefined' && METRIC[key]; if (!m) return;
  ev.preventDefault();
  infoEl.innerHTML = `<div class="hd">${esc(m.label)}<span class="dim"> · ${esc(m.unit)}</span></div><div class="desc">${esc(m.desc)}</div>${extra}<div class="key"><code>${esc(m.key)}</code> · ${esc(m.area)}</div>`;
  infoEl.hidden = false;
  const w = infoEl.offsetWidth, h = infoEl.offsetHeight;
  infoEl.style.left = Math.min(ev.clientX + 8, innerWidth - w - 8) + 'px'; infoEl.style.top = Math.min(ev.clientY + 8, innerHeight - h - 8) + 'px';
}
document.addEventListener('mousedown', ev => { if (!ev.target.closest('#info')) infoEl.hidden = true; });
document.addEventListener('keydown', ev => { if (ev.key === 'Escape') infoEl.hidden = true; });

// ---- the chart menu: ask an AI what happened over a range (or at a point), or narrow the page to it ----
const ctxEl = (() => { const d = document.createElement('div'); d.id = 'ctx'; d.hidden = true; document.body.appendChild(d); return d; })();
function rangePrompt(chart, a, b, point, ask) {
  writeHash();
  const t = chart.times, fmtV = s => (s.right ? chart.rfmt : chart.fmt);
  const lines = chart.series.map(s => { const va = s.values[a], vb = s.values[b], f = fmtV(s); const vals = s.values.slice(a, b + 1).filter(v => v != null); const lo = vals.length ? Math.min(...vals) : null, hi = vals.length ? Math.max(...vals) : null; return `- ${s.label}: ${va == null ? '—' : f(va)} → ${vb == null ? '—' : f(vb)}${va != null && vb != null ? ` (${vb - va > 0 ? '+' : ''}${f(vb - va).replace(/^\+/, '')}${va ? `, ${(100 * (vb - va) / Math.abs(va)).toFixed(1)}%` : ''})` : ''}${!point && vals.length > 2 ? `; low ${f(lo)}, high ${f(hi)} over the range` : ''}`; });
  const filter = location.hash.slice(1) || 'no filter (open PRs)';
  return `${ask || ASK_PLACEHOLDER}

##########
Context: prawn explore, a page over the pull requests of ${D.repo} (${fmtNum(D.prs.length)} PRs open at some point since ${D.since}). The chart "${chart.title || chart.series.map(s => s.label).join(', ')}" is bucketed by ${S.gran}; the page's filter is: ${filter}.
${point ? `The point ${chart.label(t[b])} against the previous one, ${chart.label(t[a])}:` : `The selected range, ${chart.label(t[a])} to ${chart.label(t[b])} (${Math.round((t[b] - t[a]) / DAY)} days):`}
${lines.join('\n')}
${point ? 'What explains the change at this point?' : 'What explains how these series moved over this range, and what should the maintainers do about it?'} Groups: ${GROUP_NAMES.join(', ')}; maintainers are the ${D.maintainerGroup} group. A PR is in the maintainers' court when it awaits a first look, a re-look after the author pushed, or a merge; in the author's when it is a draft, labelled waiting-response, or has changes requested and nothing pushed since.`;
}
function showCtx(ev, chart, a, b, point) {
  const t = chart.times;
  ctxEl.innerHTML = `<div class="hd">${point ? chart.label(t[b]) : `${chart.label(t[a])} → ${chart.label(t[b])}`}<span class="dim"> · ${point ? 'this point vs the previous' : Math.round((t[b] - t[a]) / DAY) + ' days'}</span></div>
    <input id="ctxAsk" placeholder="your question (optional)" autocomplete="off">
    <div class="row">ask ${AI_TARGETS.map(x => `<button class="plain" data-ai="${x.id}">${x.label}</button>`).join('')}<button class="plain" data-ai="copy">copy prompt</button></div>
    <div class="row">${point ? '' : `<button class="plain" data-act="zoom">narrow the page to this range</button>`}<button class="plain" data-act="close">close</button></div>`;
  ctxEl.hidden = false;
  const cw = ctxEl.offsetWidth, ch = ctxEl.offsetHeight;
  ctxEl.style.left = Math.min(ev.clientX + 8, innerWidth - cw - 8) + 'px'; ctxEl.style.top = Math.min(ev.clientY + 8, innerHeight - ch - 8) + 'px';
  hideTip();
  $('#ctxAsk').focus();
  ctxEl.onclick = e => {
    const btn = e.target.closest('button'); if (!btn) return;
    const ask = $('#ctxAsk').value.trim();
    if (btn.dataset.act === 'close') { ctxEl.hidden = true; return; }
    if (btn.dataset.act === 'zoom') { S.from = fmtDate(t[a]); S.to = fmtDate(t[b]); ctxEl.hidden = true; update(true); return; }
    if (btn.dataset.ai === 'copy') { navigator.clipboard?.writeText(rangePrompt(chart, a, b, point, ask)); btn.textContent = 'copied'; return; }
    const x = AI_TARGETS.find(y => y.id === btn.dataset.ai); if (x) window.open(x.url(rangePrompt(chart, a, b, point, ask).slice(0, x.limit)), '_blank', 'noopener');
  };
}
document.addEventListener('mousedown', ev => { if (!ev.target.closest('#ctx')) { ctxEl.hidden = true; document.querySelectorAll('svg rect.sel').forEach(r => r.remove()); } });
document.addEventListener('keydown', ev => { if (ev.key === 'Escape') { ctxEl.hidden = true; document.querySelectorAll('svg rect.sel').forEach(r => r.remove()); } });
// categorical bars: items [{label, value, color?, title?}], rounded at the top like tfpp's per-release bars
function barChart(el, items, opts = {}) {
  const w = Math.max(320, Math.round(el.getBoundingClientRect().width) || 700), h = opts.h || 180, pad = { l: 52, r: 14, t: 10, b: 28 };
  const hi = items.reduce((m, i) => Math.max(m, i.value), 1e-9) * 1.06, ys = v => pad.t + (1 - v / hi) * (h - pad.t - pad.b);
  const bw = (w - pad.l - pad.r) / items.length;
  let g = '';
  for (const t of niceTicks(0, hi, 4).filter(t => t <= hi)) g += `<line class="grid" x1="${pad.l}" x2="${w - pad.r}" y1="${ys(t)}" y2="${ys(t)}"/><text x="${pad.l - 6}" y="${ys(t) + 4}" text-anchor="end">${fmtNum(t)}</text>`;
  items.forEach((it, i) => {
    const x = pad.l + i * bw + bw * 0.12, width = bw * 0.76, y0 = ys(0), y1 = ys(it.value), hh = Math.max(0, y0 - y1), r = Math.min(4, width / 2, hh);
    const path = hh ? `M${x} ${y0} V${y1 + r} a${r} ${r} 0 0 1 ${r} -${r} h${width - 2 * r} a${r} ${r} 0 0 1 ${r} ${r} V${y0} Z` : '';
    g += `<path class="bar" d="${path}" fill="${it.color || 'var(--s1)'}"><title>${esc(it.title || it.label + ': ' + it.value)}</title></path>`;
    g += `<text x="${x + width / 2}" y="${h - 8}" text-anchor="middle">${esc(it.label)}</text>`;
  });
  g += `<line class="axis" x1="${pad.l}" x2="${w - pad.r}" y1="${ys(0)}" y2="${ys(0)}"/>`;
  el.innerHTML = `<svg viewBox="0 0 ${w} ${h}" width="${w}" height="${h}">${g}</svg>`;
}
function sparkline(values, color = 'var(--accent)', w = 90, h = 18) {
  const hi = values.reduce((m, v) => Math.max(m, v), 1); const n = values.length; if (n < 2) return '';
  const pts = values.map((v, i) => `${(i / (n - 1) * w).toFixed(1)},${(h - 2 - (v / hi) * (h - 4)).toFixed(1)}`).join(' ');
  return `<svg class="spark" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}"><polyline fill="none" stroke="${color}" stroke-width="1.5" points="${pts}"/></svg>`;
}

// ---- time bucketing ----
function buckets(from, to, gran) {
  const out = []; const d = new Date(from * 1000); d.setUTCHours(0, 0, 0, 0);
  if (gran === 'month') d.setUTCDate(1); else if (gran === 'week') d.setUTCDate(d.getUTCDate() - ((d.getUTCDay() + 6) % 7)); // monday
  while (d.getTime() / 1000 <= to) { out.push(d.getTime() / 1000); if (gran === 'month') d.setUTCMonth(d.getUTCMonth() + 1); else d.setUTCDate(d.getUTCDate() + (gran === 'week' ? 7 : 1)); }
  return out;
}
const GRAN_STEP = { day: DAY, week: 7 * DAY, month: 30 * DAY };
function bucketIndex(times, t) { let lo = 0, hi = times.length - 1; if (t < times[0]) return -1; while (lo < hi) { const m = (lo + hi + 1) >> 1; if (times[m] <= t) lo = m; else hi = m - 1; } return lo; }
const bucketLabel = gran => t => { const d = new Date(t * 1000); return gran === 'month' ? d.toLocaleString('en', { month: 'short', year: 'numeric', timeZone: 'UTC' }) : gran === 'week' ? 'week of ' + fmtDate(t) : fmtDate(t); };

// ---- the prs tab's columns: every field collected on a PR, each one renderable, sortable, and pickable ----
// key → { l: header, d: what it is, f: render, v: sort value (text a→z, numbers low first unless hi), cls: cell class }
const COL = {};
const col = (k, l, d, f, v, o = {}) => { COL[k] = { k, l, d, f, v, cls: o.cls || '', hi: !!o.hi, s: o.s }; };
const logins = (xs, key, n = 3) => xs.slice(0, n).map(x => `<span class="clk" data-q="${key}:${esc(x)}">${esc(x)}</span>`).join(', ') + (xs.length > n ? ` <span class="dim">+${xs.length - n}</span>` : '');
const LAST = '￿'; // empty text sorts last
col('n', '#', 'the PR number', p => `<a href="${prURL(p.n)}" target="_blank" rel="noopener" onclick="event.stopPropagation()">${p.n}</a>`, p => p.n, { cls: 'num' });
col('t', 'title', 'the title, with a draft badge', p => `${p.d ? '<span class="badge">draft</span>' : ''}${esc(p.t)}`, p => p.tl, { cls: 'title' });
col('a', 'author', 'the author and their group', p => `<span class="clk" data-author="${esc(p.a)}">${esc(p.a)}</span> <span class="badge g" style="--gc:${groupColor(p.g)}">${esc(p.g)}</span>`, p => p.lo);
col('g', 'group', 'the configured group the author is in, or community', p => `<span class="badge g clk" data-group="${esc(p.g)}" style="--gc:${groupColor(p.g)}">${esc(p.g)}</span>`, p => p.g);
col('as', 'assoc', "github's author association", p => esc((p.as || '').toLowerCase().replace(/_/g, ' ')), p => (p.as || LAST).toLowerCase());
col('s', 'state', 'open, merged, or closed', p => `<span class="st-${p.s}">${p.s}</span>`, p => p.s);
col('status', 'status', "ghp-sync's Status: merged, closed, or an open PR's state today (draft, awaiting, waiting, approved, blocked)", p => p.s !== 'open' ? `<span class="st-${p.s}">${p.s}</span>` : `<span style="color:${STATE_COLORS[STATE_NAMES.indexOf(p.status)]}">${p.status}</span>`, p => p.status);
col('d', 'draft', 'a draft', p => p.d ? 'draft' : '', p => p.d ? 1 : 0, { hi: true });
col('ct', 'court', 'whose court an open PR is in, and for how long', p => p.ct ? `<span class="court-${p.ct}">${p.ct}</span> <span class="dim">${fmtDays(p.cd)}</span>` : '—', p => p.ct || LAST, { s: (a, b) => (a.ct || 'z').localeCompare(b.ct || 'z') || (b.cd || 0) - (a.cd || 0) });
col('cd', 'in court', 'days in the current court', p => fmtDays(p.cd), p => p.cd, { cls: 'num', hi: true });
col('age', 'age', 'days since the PR opened', p => fmtDays(p.age), p => p.age, { cls: 'num', hi: true });
col('idle', 'idle', 'days since the last human activity', p => fmtDays(p.idle), p => p.idle, { cls: 'num', hi: true });
col('ef', 'effort', 'review effort 1..5 from the diff shape', p => `<span class="eff" title="review effort ${p.ef}/5 · +${p.ad}/−${p.de} over ${p.f} files">${'<b>●</b>'.repeat(p.ef)}${'○'.repeat(5 - p.ef)}</span>`, p => p.ef, { s: (a, b) => a.ef - b.ef || b.age - a.age });
col('size', 'size', 'lines added and deleted', p => `+${fmtNum(p.ad)}/−${fmtNum(p.de)}`, p => p.size, { cls: 'num', hi: true });
col('f', 'files', 'files changed', p => fmtNum(p.f), p => p.f, { cls: 'num', hi: true });
col('props', 'props', 'schema properties the diff adds or changes on existing resources — read from the diff (schema keys, model tags, docs bullets), open PRs only', p => (p.pp || []).length ? `<span title="${esc(p.pp.join(', '))}">${p.pp.length}</span>` : '', p => (p.pp || []).length, { cls: 'num', hi: true });
col('kd', 'kind', 'what sort of change it is, read from the diff: the first that fits of resources added, data sources added, api version upgrade, properties added or changed, test fix, documentation… — open PRs only', p => p.kd ? `<span class="clk" data-q="kind:&quot;${esc(p.kd)}&quot;">${esc(p.kd)}</span>` : '', p => p.kd ? KIND_ORDER.indexOf(p.kd) : 999);
col('k', 'file types', 'the kinds of file it changes', p => p.k.map(k => `<span class="badge clk" data-kind="${k}">${k}</span>`).join(''), p => p.k[0] || LAST);
col('sv', 'services', 'the services touched (internal/services/<name>)', p => p.sv.slice(0, 3).map(s => `<span class="badge clk" data-svc="${esc(s)}">${esc(s)}</span>`).join('') + (p.sv.length > 3 ? `<span class="badge">+${p.sv.length - 3}</span>` : ''), p => p.sv[0] || LAST);
col('l', 'labels', 'the labels', p => p.l.slice(0, 4).map(l => `<span class="badge clk" data-label="${esc(l)}">${esc(l)}</span>`).join('') + (p.l.length > 4 ? `<span class="badge">+${p.l.length - 4}</span>` : ''), p => p.l[0] || LAST);
col('ms', 'milestone', 'the milestone', p => esc(p.ms || ''), p => p.ms || LAST);
col('rr', 'rounds', 'changes-requested reviews by maintainers', p => p.rr || '—', p => p.rr, { cls: 'num', hi: true });
col('rv', 'reviews', 'reviews by anyone but the author', p => p.rv || '—', p => p.rv, { cls: 'num', hi: true });
col('ap', 'approvals', 'approving reviews by maintainers', p => p.ap || '—', p => p.ap, { cls: 'num', hi: true });
col('rc', 'review ✎', 'inline comments across every review', p => p.rc || '—', p => p.rc, { cls: 'num', hi: true });
col('mrv', 'member reviews', "reviews by the maintainers group — the members' work on the PR", p => p.mrv || '—', p => p.mrv, { cls: 'num', hi: true });
col('mrc', 'member ✎', "inline comments on the members' reviews", p => p.mrc || '—', p => p.mrc, { cls: 'num', hi: true });
col('cm', '💬', 'conversation comments', p => p.cm || '—', p => p.cm, { cls: 'num', hi: true });
col('th', '👍', 'thumbs-up reactions', p => p.th || '—', p => p.th, { cls: 'num', hi: true });
col('rw', 'reviewers', 'maintainers who reviewed or commented, most active first', p => p.rw.length ? p.rw.slice(0, 3).map(r => `<span class="clk" data-reviewer="${esc(r)}">${esc(r)}</span>`).join(', ') + (p.rw.length > 3 ? ` <span class="dim">+${p.rw.length - 3}</span>` : '') : '—', p => p.rw[0] || LAST);
col('rb', 'reviewed by', 'everyone but the author who left a review, first review first', p => p.rb.length ? logins(p.rb, 'reviewedby') : '—', p => p.rb[0] || LAST);
col('ab', 'approved by', 'who left an approving review, first approval first', p => p.ab.length ? logins(p.ab, 'approvedby') : '—', p => p.ab[0] || LAST);
col('cb', 'changes by', 'who requested changes: how many times (×) and how many review comments (✎) over those requests', p => p.cb.length ? p.cb.slice(0, 3).map(c => `<span class="clk" data-q="changesby:${esc(c.l)}">${esc(c.l)}</span><span class="dim">(×${c.n} ✎${c.c})</span>`).join(', ') + (p.cb.length > 3 ? ` <span class="dim">+${p.cb.length - 3}</span>` : '') : '—', p => p.cb[0] ? p.cb[0].l : LAST);
col('fr', 'first resp', 'days to the first maintainer response', p => p.fr < 0 ? '<span class="bad">none</span>' : fmtDays(p.fr), p => p.fr < 0 ? Infinity : p.fr, { cls: 'num', hi: true });
col('fw', 'responder', 'the maintainer who responded first', p => p.fw ? `<span class="clk" data-q="responder:${esc(p.fw)}">${esc(p.fw)}</span>` : '—', p => p.fw || LAST);
col('lm', 'last maint', 'the last maintainer touch: who and when', p => p.lm ? `${esc(p.lastMaintBy)} <span class="dim">${fmtDate(p.lm)}</span>` : '—', p => p.lm || 0, { hi: true });
col('lu', 'last author', 'when the author last acted', p => p.lu ? fmtDate(p.lu) : '—', p => p.lu || 0, { cls: 'num', hi: true });
col('la', 'last activity', 'the last human activity', p => fmtDate(p.la), p => p.la, { cls: 'num', hi: true });
col('wd', 'waiting', 'total days under the waiting-response label', p => p.wd ? fmtDays(p.wd) : '—', p => p.wd, { cls: 'num', hi: true });
col('wc', 'cycles', 'waiting-response cycles', p => p.wc || '—', p => p.wc, { cls: 'num', hi: true });
col('td', 'resolved in', 'days open until merged or closed', p => p.td < 0 ? '—' : fmtDays(p.td), p => p.td < 0 ? null : p.td, { cls: 'num', hi: true });
col('rd', 'decision', "github's review decision", p => esc((p.rd || '').replace(/_/g, ' ')), p => p.rd || LAST);
col('mg', 'mergeable', "github's mergeability", p => p.mg === 'conflicting' ? '<span class="bad">conflicting</span>' : esc(p.mg || ''), p => p.mg || LAST);
col('ci', 'ci', "the head commit's checks: passing, failing, running; needs approval when github is holding a fork's workflows for a maintainer to approve; expired when github has dropped them (it keeps 400 days); not run when a recent head commit has none", p => p.ci ? `<span class="${CI_CLASS[p.ci]}">${ciName(p)}</span>` : p.s === 'open' ? '<span class="dim">not run</span>' : '', p => p.ci || (p.s === 'open' ? 'not run' : LAST));
// ci's states: how each is coloured, and what the two that are not their own word are called
const CI_CLASS = { passing: 'ok', failing: 'bad', running: 'mid', approval: 'mid', expired: 'dim' };
const ciName = p => ({ approval: 'needs approval' }[p.ci] || p.ci || 'not run');
// the detail behind ci, as of the last fetch: when the checks ran, what fails and since when, and how far main has moved
const ciAge = p => p.cir && p.ci !== 'approval' ? (NOW - p.cir) / DAY : null; // days since the head commit's checks last finished
const failingFor = p => p.cis ? (NOW - p.cis) / DAY : null;
const behindFor = p => p.bhs ? (NOW - p.bhs) / DAY : null; // days since the branch last caught up with its base // days since the PR was first seen failing — pushes that fail again do not restart it
const commits = n => `${fmtNum(n)} commit${n === 1 ? '' : 's'}`;
col('ciage', 'ci age', "how long ago the head commit's checks last ran — a re-run counts", p => ciAge(p) == null ? '' : fmtDays(ciAge(p)), p => ciAge(p) ?? -1, { cls: 'num', hi: true });
col('cifor', 'failing for', 'how long ci has been failing: since it was first seen red, through any pushes that failed again', p => p.cis ? `<span class="bad">${fmtDays(failingFor(p))}</span>` : '', p => failingFor(p) ?? -1, { cls: 'num', hi: true });
col('cifail', 'failing checks', 'the checks failing on the head commit', p => (p.cif || []).map(c => `<span class="badge clk" data-q="failing:${esc(c)}">${esc(c)}</span>`).join(''), p => (p.cif || [])[0] || LAST);
col('behind', 'behind', "commits on the base branch the PR's branch does not have", p => p.bh == null ? '' : fmtNum(p.bh), p => p.bh ?? -1, { cls: 'num', hi: true });
col('behindfor', 'behind for', 'how long since the branch last caught up with its base (its merge base) — old and mergeable is a branch to merge main into', p => behindFor(p) == null ? '' : fmtDays(behindFor(p)), p => behindFor(p) ?? -1, { cls: 'num', hi: true });
col('drift', 'ci drift', 'commits the base branch has had since the checks last ran: how stale the result is', p => p.cdr == null ? '' : fmtNum(p.cdr), p => p.cdr ?? -1, { cls: 'num', hi: true });
// the acceptance tests teamcity ran: p.tc is the latest build of each service the PR was tested for, summed up
const HAS_TESTS = D.prs.some(p => p.tc); // teamcity is configured and has answered: without it the page says nothing of tests
const TC_CLASS = { passing: 'ok', failing: 'bad', running: 'mid', cancelled: 'dim' };
const testAge = p => p.tc && p.tc.at ? (NOW - p.tc.at) / DAY : null; // days since the most recent of them finished
const testsSince = p => p.tc && p.tc.cs != null ? p.tc.cs : null; // commits pushed to the PR since they began
col('tests', 'tests', 'the acceptance tests teamcity last ran for it: the worst of the latest build of each service — failing, running, passing, cancelled', p => p.tc ? `<span class="${TC_CLASS[p.tc.s]}">${p.tc.s}</span>` : p.s === 'open' ? '<span class="dim">never run</span>' : '', p => p.tc ? p.tc.s : LAST);
col('tfail', 'tests failed', 'how many tests failed in those builds', p => p.tc && p.tc.f ? `<span class="bad">${fmtNum(p.tc.f)}</span>` : '', p => p.tc ? p.tc.f : -1, { cls: 'num', hi: true });
col('tpass', 'tests passed', 'how many tests passed in those builds', p => p.tc ? fmtNum(p.tc.p) : '', p => p.tc ? p.tc.p : -1, { cls: 'num', hi: true });
col('testage', 'test age', 'how long ago the tests last ran', p => testAge(p) == null ? '' : fmtDays(testAge(p)), p => testAge(p) ?? -1, { cls: 'num', hi: true });
col('tsince', 'since tests', 'commits pushed to the PR since its tests began: what they have not seen', p => testsSince(p) == null ? '' : fmtNum(testsSince(p)), p => testsSince(p) ?? -1, { cls: 'num', hi: true });
col('mb', 'merged by', 'who merged it', p => p.mb ? `<span class="clk" data-q="mergedby:${esc(p.mb)}">${esc(p.mb)}</span>` : '', p => p.mb || LAST);
col('c', 'created', 'when it opened', p => fmtDate(p.c), p => p.c, { cls: 'num', hi: true });
col('u', 'updated', 'when github last saw it change: a push, a comment, a review, a label', p => fmtDate(p.u), p => p.u, { cls: 'num', hi: true });
col('x', 'closed', 'when it closed', p => p.x ? fmtDate(p.x) : '', p => p.x || 0, { cls: 'num', hi: true });
col('m', 'merged', 'when it merged', p => p.m ? fmtDate(p.m) : '', p => p.m || 0, { cls: 'num', hi: true });
col('li', 'closes', 'the issues its closing keywords reference', p => p.li.map(n => `<a href="https://github.com/${D.repo}/issues/${n}" target="_blank" rel="noopener" onclick="event.stopPropagation()">#${n}</a>`).join(' '), p => p.li.length, { hi: true });
col('aiMax', 'ai', 'the highest AI close score', p => p.aiMax == null ? '' : `<span title="${esc(Object.entries(p.ai).map(([k, v]) => k + ' ' + v.toFixed(2)).join(', '))}">${p.aiMax.toFixed(2)}</span>`, p => p.aiMax, { cls: 'num', hi: true });
col('checks', 'checks', 'the close checks that flagged it', p => p.checks.map(c => `<span class="badge">${esc(c.check)}${c.score ? ' ' + c.score : ''}</span>`).join(''), p => p.checks.length, { hi: true });
const DEFAULT_COLS = ['n', 't', 'a', 's', 'ct', 'age', 'idle', 'u', 'ef', 'size', 'f', 'sv', 'l', 'rr', 'fr', 'aiMax'];
// the columns shown, in order: the dc hash key, or the default; unknown keys (an older view) are dropped. The data
// tab keeps a list of its own (ddc), the prs tab's until it is changed there
const colsKey = () => S.tab === 'data' ? 'ddc' : 'dc';
const prsColKeys = () => S.dc ? S.dc.split(',') : DEFAULT_COLS;
const visibleCols = () => { const ks = (S.tab === 'data' && S.ddc ? S.ddc.split(',') : prsColKeys()).filter(k => COL[k]); return (ks.length ? ks : DEFAULT_COLS).map(k => COL[k]); };
const setCols = keys => { const base = S.tab === 'data' ? prsColKeys() : DEFAULT_COLS; set(colsKey(), keys.join(',') === base.join(',') ? '' : keys.join(',')); };
// ties in any sort break this way: the maintainers' court first, then the cheapest, then the longest waiting
const SORT_PRIORITY = (a, b) => (a.ct === 'maintainer' ? 0 : 1) - (b.ct === 'maintainer' ? 0 : 1) || a.ef - b.ef || (b.cd || 0) - (a.cd || 0);
// a column's comparator: its own when it has one, else by its sort value with empties last, ties broken by priority
function colSorter(k) {
  const c = COL[k] || COL[DEFAULTS.sort]; // a column that is gone: the default sort
  if (c.s) return c.s;
  const cmp = (x, y) => {
    if (x == null && y == null) return 0; if (x == null) return 1; if (y == null) return -1;
    return typeof x === 'number' ? x - y : String(x).localeCompare(String(y));
  };
  return (a, b) => { const x = c.v(a), y = c.v(b); const r = x == null || y == null ? cmp(x, y) : c.hi ? cmp(y, x) : cmp(x, y); return r || SORT_PRIORITY(a, b); };
}
const KIND_ORDER = D.kindOrder || [];
// show: one kind of PR at a time, on the prs tab — the suggested categories worth a pass of their own, and the property counts
const propCount = p => (p.pp || []).length;
const SHOWS = [
  ['approved', 'approved', p => p.approved],
  ['docs', 'documentation (provider)', p => only(p, 'docs', 'changelog')],
  ['examples', 'documentation (examples)', p => only(p, 'examples', 'changelog')],
  ['contributing', 'documentation (contributing)', p => only(p, 'contributing', 'changelog')],
  ['tests', 'ci/test only', p => only(p, 'tests', 'ci')],
  // by kind: what the change is, read from its diff
  ['api', 'api upgrade', p => p.kd === 'api version upgrade'],
  ['resource', 'new resource', p => p.kd === 'resources added' || p.kd === '1 resource added'],
  ['datasource', 'new data source', p => p.kd === 'data sources added' || p.kd === 'data source added'],
  ['prop1', 'single property', p => propCount(p) === 1],
  ['prop2-4', '2–4 properties', p => propCount(p) >= 2 && propCount(p) <= 4],
];
const VIEWS = [
  ['first look', 'state:open fr:none -draft:yes'],
  ['approved', 'state:open approved:yes'],
  ['pushed after changes', 'state:open court:maintainer rounds>0'],
  ['>30d', 'state:open label:waiting-response cd>30'],
  ['small/idle', 'state:open effort<3 idle>60'],
  ['conflicted', 'state:open mergeable:conflicting'],
  ['mergeable', 'state:open mergeable:mergeable'],
  ['mergeable, behind 30d+', 'state:open mergeable:mergeable behindfor>30'],
  ['examples', 'state:open touches:examples'],
  ['contributing docs', 'state:open touches:contributing'],
  ['ci failing', 'state:open ci:failing'],
  ['ci needs approval', 'state:open ci:approval'],
  ['ci not run', 'state:open ci:none'],
  ['ci expired', 'state:open ci:expired'],
  ['tests failing', 'state:open tests:failing'],
  ['tests passed, nothing since', 'state:open tests:passing testsince:0'],
  ['never tested', 'state:open tests:none'],
  ['big & old', 'state:open effort>3 age>180'],
  ['maintainer court > 14d', 'state:open court:maintainer cd>14'],
];
// the ready-made filters combine: each is a query, and the query box holds them merged. The same key across two
// filters means either (ci:failing + ci:none is ci:failing,none), different keys mean both. A filter is on when the
// query holds all of it; whatever else was typed into the query stays through every toggle.
const qTokens = q => (q || '').match(/(?:[^\s"]+|"[^"]*")+/g) || [];
const qKV = t => { const m = /^([a-z]+):(.+)$/i.exec(t); return m ? [m[1], m[2].split(',')] : null; }; // a plain key:a,b term (a -key: one is not)
function filtersOn(q) {
  const toks = qTokens(q);
  const has = t => { const a = qKV(t); return a ? toks.some(x => { const b = qKV(x); return b && b[0] === a[0] && a[1].every(v => b[1].includes(v)); }) : toks.includes(t); };
  return VIEWS.filter(([, fq]) => qTokens(fq).every(has));
}
function toggleFilter(q, name) {
  const was = filtersOn(q), now = was.some(f => f[0] === name) ? was.filter(f => f[0] !== name) : [...was, VIEWS.find(f => f[0] === name)];
  // what the query holds beyond the filters that were on: typed by hand, kept as it is
  const covered = was.flatMap(([, fq]) => qTokens(fq)), coveredKV = {};
  for (const a of covered.map(qKV)) if (a) (coveredKV[a[0]] ||= new Set()), a[1].forEach(v => coveredKV[a[0]].add(v));
  const extras = qTokens(q).flatMap(t => { const a = qKV(t); if (!a) return covered.includes(t) ? [] : [t]; const rest = a[1].filter(v => !(coveredKV[a[0]] && coveredKV[a[0]].has(v))); return rest.length ? [`${a[0]}:${rest.join(',')}`] : []; });
  const out = [], at = {};
  for (const t of [...now.flatMap(([, fq]) => qTokens(fq)), ...extras]) {
    const a = qKV(t);
    if (!a) { if (!out.includes(t)) out.push(t); continue; }
    if (at[a[0]] == null) { at[a[0]] = out.length; out.push([a[0], [...a[1]]]); } else for (const v of a[1]) if (!out[at[a[0]]][1].includes(v)) out[at[a[0]]][1].push(v);
  }
  return out.map(t => Array.isArray(t) ? `${t[0]}:${t[1].join(',')}` : t).join(' ');
}
// the tab row's dropdowns list their choices a to z, whatever order they are declared in
const byLabel = label => (a, b) => label(a).localeCompare(label(b), undefined, { numeric: true, sensitivity: 'base' });
let filterPopOpen = false; // the filter list stays open over the re-render each tick causes, until a click elsewhere
document.addEventListener('click', e => { if (!e.target.closest('#viewpick')) filterPopOpen = false; });
let queueLimit = 200;
const DOC_TYPES = [['docs', 'provider docs'], ['examples', 'examples'], ['contributing', 'contributing docs']]; // the kinds that are documentation, and their names
// group by: the table in sections. Each grouping names a PR's section (null for none);
// sections keep the table's sort inside them and run in the grouping's order, else largest first.
// Built on demand: the suggested categories are declared further down
const groupings = () => ({
  suggested: { label: 'suggested category', of: p => { if (p.s !== 'open') return 'not open'; const c = CATEGORIES.find(c => c[2](p)); return c ? c[0] : 'everything else'; }, order: [...CATEGORIES.map(c => c[0]), 'everything else', 'not open'], desc: Object.fromEntries(CATEGORIES.map(c => [c[0], c[1]])) },
  // kind: what sort of change the PR is, one each, the first that fits — worked out when the page is built, from the diff
  kind: { label: 'kind', of: p => p.s !== 'open' ? 'not open' : p.kd || 'other', order: [...KIND_ORDER, 'not open'] },
  // documentation: the PRs that are nothing but docs, by which of the three kinds. It drops the rest: a grouping with
  // drop set leaves out the PRs it has no group for, from the table and everything counted or exported from it
  docs: { label: 'documentation', of: p => { const types = DOC_TYPES.filter(([k]) => p.k.includes(k)); return types.length && only(p, ...DOC_TYPES.map(t => t[0]), 'changelog') ? types.map(t => t[1]).join(' + ') : null; }, drop: true,
    order: DOC_TYPES.map(t => t[1]), desc: { 'provider docs': 'the website docs under website/ and nothing else', examples: 'example configurations under examples/ and nothing else', 'contributing docs': 'the contributor guide under contributing/ and nothing else' } },
  service: { label: 'service', of: p => p.sv.length === 0 ? 'no service' : p.sv.length === 1 ? p.sv[0] : 'several services' },
  court: { label: 'court', of: p => p.s !== 'open' ? p.s : p.ct + "'s court" },
  status: { label: 'status', of: p => p.s !== 'open' ? p.s : p.status || 'open', order: [...STATE_NAMES, 'open', 'merged', 'closed'] },
  ci: { label: 'ci', of: p => p.s !== 'open' ? 'not open' : 'ci ' + ciName(p), order: ['ci failing', 'ci needs approval', 'ci not run', 'ci running', 'ci passing', 'ci expired', 'not open'], desc: { 'ci needs approval': 'github is holding the workflows of a fork until a maintainer clicks approve and run — one click each', 'ci not run': 'no checks on a recent head commit — often a first-time contributor\'s workflows waiting to be approved', 'ci expired': 'the head commit is over 400 days old and github has dropped its check results — a new commit runs them again' } },
  cifail: { label: 'failing check', of: p => p.ci !== 'failing' ? 'not failing' : (p.cif || []).length === 0 ? 'failing, check unknown' : p.cif.length === 1 ? p.cif[0] + ' only' : p.cif.join(' + '), last: ['failing, check unknown', 'not failing'] },
  tests: { label: 'tests', of: p => p.s !== 'open' ? 'not open' : p.tc ? 'tests ' + p.tc.s : 'never tested', order: ['tests failing', 'tests running', 'tests passing', 'tests cancelled', 'never tested', 'not open'], desc: { 'tests failing': 'the latest teamcity build of at least one service failed tests', 'tests passing': 'the latest build of every service it was tested for passed', 'never tested': 'teamcity has no build for it' } },
  group: { label: 'author group', of: p => p.g, order: [...GROUP_NAMES, 'community'] },
  effort: { label: 'effort', of: p => 'effort ' + p.ef, order: ['effort 1', 'effort 2', 'effort 3', 'effort 4', 'effort 5'] },
  author: { label: 'author', of: p => p.a },
});
function grouped(rows) {
  const g = groupings()[S.gb]; if (!g) return null;
  const by = new Map();
  for (const p of rows) { const k = g.of(p) ?? 'other'; if (!by.has(k)) by.set(k, []); by.get(k).push(p); }
  const names = [...by.keys()].sort((a, b) => { const o = g.order || []; const ia = o.indexOf(a), ib = o.indexOf(b); if (ia !== -1 || ib !== -1) return (ia === -1 ? 1e9 : ia) - (ib === -1 ? 1e9 : ib); const la = (g.last || []).includes(a), lb = (g.last || []).includes(b); if (la !== lb) return la ? 1 : -1; return by.get(b).length - by.get(a).length || a.localeCompare(b); });
  return names.map(name => ({ name, desc: (g.desc || {})[name] || '', prs: by.get(name) }));
}
// the prs table's rows: the filter, narrowed by show, in the table's sort — what the table draws and the export writes
function queueRows() {
  const show = SHOWS.find(sh => sh[0] === S.sh);
  let rows = show ? M.filter(show[2]) : M.slice();
  const g = groupings()[S.gb]; if (g && g.drop) rows = rows.filter(p => g.of(p) != null); // a grouping that only covers some PRs shows only those
  rows.sort(colSorter(S.sort)); if (S.dir === 'asc') rows.reverse();
  return rows;
}
function renderQueue(view) {
  const rows = queueRows();
  const cols = visibleCols();
  // the rows to draw, group headers interleaved when grouping; the limit counts PRs
  const sections = grouped(rows);
  // a section folds shut on a click of its header; the folded ones are in the url (gc), split by | as names hold commas
  const shut = new Set(S.gc ? S.gc.split('|') : []);
  const drawn = sections ? sections.flatMap(sec => [{ hdr: sec }, ...(shut.has(sec.name) ? [] : sec.prs)]) : rows;
  const showing = drawn.filter(r => !r.hdr).length;
  let budget = queueLimit;
  const body = drawn.filter(r => r.hdr || budget-- > 0).map(r => r.hdr
    ? `<tr class="group${shut.has(r.hdr.name) ? ' shut' : ''}" data-g="${esc(r.hdr.name)}" title="click to ${shut.has(r.hdr.name) ? 'open' : 'fold'} this group"><td colspan="${cols.length}"><span class="caret">${shut.has(r.hdr.name) ? '▸' : '▾'}</span>${esc(r.hdr.name)}<span class="n">${fmtNum(r.hdr.prs.length)}</span>${r.hdr.desc ? `<span class="desc">${esc(r.hdr.desc)}</span>` : ''}</td></tr>`
    : `<tr class="pr" data-n="${r.n}">${cols.map(c => `<td class="${c.cls}">${c.f(r)}</td>`).join('')}</tr>${S.open_n === String(r.n) ? `<tr class="detail"><td colspan="${cols.length}">${detail(r)}</td></tr>` : ''}`).join('');
  const inCourt = rows.filter(p => p.ct === 'maintainer').length, inAuthor = rows.filter(p => p.ct === 'author').length;
  view.innerHTML = `
    <div class="tiles">
      <div class="tile"><div class="v">${fmtNum(rows.length)}</div><div class="k">matching</div><div class="d">${fmtNum(rows.filter(p => p.s === 'open').length)} open · ${fmtNum(rows.filter(p => p.s === 'merged').length)} merged · ${fmtNum(rows.filter(p => p.s === 'closed').length)} closed</div></div>
      <div class="tile"><div class="v court-maintainer">${fmtNum(inCourt)}</div><div class="k">maintainers' court</div><div class="d">median ${fmtDays(median(rows.filter(p => p.ct === 'maintainer').map(p => p.cd)))} there</div></div>
      <div class="tile"><div class="v court-author">${fmtNum(inAuthor)}</div><div class="k">author's court</div><div class="d">median ${fmtDays(median(rows.filter(p => p.ct === 'author').map(p => p.cd)))} there</div></div>
      <div class="tile"><div class="v">${fmtNum(rows.filter(p => p.fr < 0 && p.s === 'open').length)}</div><div class="k">never answered</div><div class="d">no maintainer response yet</div></div>
      <div class="tile"><div class="v">${fmtNum(rows.reduce((n, p) => n + p.ef, 0))}</div><div class="k">effort points</div><div class="d">Σ effort 1–5, ~${fmtNum(rows.reduce((n, p) => n + p.ef, 0) / 12, 0)} review days</div></div>
      <div class="tile"><div class="v">${fmtDays(median(rows.map(p => p.age)))}</div><div class="k">median age</div><div class="d">oldest ${fmtDays(Math.max(0, ...rows.map(p => p.age)))}</div></div>
    </div>
    <div class="panel wide table"><h2>PRs<span class="desc">every PR matching the filter · click a row for its life and timeline · click an author, service, or label to filter by it · drag a header to reorder, × removes it, <b>columns</b> adds</span></h2><table id="q-table"><thead><tr>${cols.map(c => `<th class="${c.cls} ${S.sort === c.k ? 'on' + (S.dir === 'asc' ? ' asc' : '') : ''}" data-sort="${c.k}" draggable="true" title="${esc(c.d)}">${c.l}<span class="rm" title="remove the column">×</span></th>`).join('')}</tr></thead><tbody>
      ${body}
      ${showing > queueLimit ? `<tr><td colspan="${cols.length}" class="more">showing ${queueLimit} of ${showing} — <a id="q-more">show 200 more</a></td></tr>` : ''}
      ${!rows.length ? `<tr><td colspan="${cols.length}" class="empty">nothing matches</td></tr>` : ''}
    </tbody></table></div>`;
  const sortOpts = cols.map(c => [c.k, c.l]);
  const on = filtersOn(S.q); // the ready-made filters the query holds
  if (!sortOpts.some(([k]) => k === S.sort) && COL[S.sort]) sortOpts.push([S.sort, COL[S.sort].l]); // sorted by a hidden column
  tabControls(`<span>sort <select id="q-sort">${sortOpts.map(([v, l]) => `<option value="${v}"${S.sort === v ? ' selected' : ''}>${l}</option>`).join('')}</select><button class="plain dirbtn" id="q-dir" title="reverse the order — ${S.dir === 'asc' ? 'reversed now' : 'the column\'s own order now: newest, largest, or a to z first'}">${S.dir === 'asc' ? '▴' : '▾'}</button></span>
    <span>group by <select id="q-group" title="the table in sections, to tackle alike PRs together"><option value="">none</option>${Object.entries(groupings()).sort(byLabel(([, g]) => g.label)).map(([k, g]) => `<option value="${k}"${S.gb === k ? ' selected' : ''}>${g.label}</option>`).join('')}</select></span>${sections ? `<button class="plain" id="q-fold" title="fold every group shut, or unfold them all">${sections.every(sec => shut.has(sec.name)) ? 'unfold all' : 'fold all'}</button>` : ''}
    <span>show <select id="q-show" title="one kind of PR at a time"><option value="">all · ${fmtNum(M.length)}</option>${SHOWS.slice().sort(byLabel(sh => sh[1])).map(([k, l, test]) => { const n = M.filter(test).length; return `<option value="${k}"${S.sh === k ? ' selected' : ''}${n ? '' : ' class="none" title="no PR matching the filter is this kind"'}>${l} · ${n ? fmtNum(n) : 'none'}</option>`; }).join('')}</select></span>
    <span class="picker" id="viewpick" title="ready-made queries; tick as many as apply — the query box shows the result"><button class="plain pick${on.length ? ' on' : ''}" type="button">filter <span class="dim">${on.length ? (on.length <= 2 ? on.map(f => f[0]).join(', ') : `${on[0][0]} +${on.length - 1}`) : S.q ? 'custom query' : 'none'}</span></button>
      <div class="pop"${filterPopOpen ? '' : ' hidden'}><div class="opt reset" data-v=""><span class="sw all"></span><span class="name">none</span></div>${VIEWS.slice().sort(byLabel(f => f[0])).map(([l, fq]) => `<div class="opt${on.some(f => f[0] === l) ? ' on' : ''}" data-v="${esc(l)}"><span class="sw"></span><span class="name">${esc(l)}</span><span class="desc">${esc(fq.replace(/^state:open /, ''))}</span></div>`).join('')}</div></span>
    ${colPicker(cols)}
    <button class="plain" id="q-open" title="open every PR in the table in a new tab — the ones in folded groups left out">open</button>
    <button class="plain" id="q-export" title="the table as a csv, or copied for a spreadsheet: pick the columns">export</button>`);
  $('#q-sort').addEventListener('change', e => { S.dir = ''; set('sort', e.target.value); });
  $('#q-dir').addEventListener('click', () => set('dir', S.dir === 'asc' ? '' : 'asc')); // the same flip a second click on the sorted header makes
  $('#q-group').addEventListener('change', e => { S.gc = ''; set('gb', e.target.value); }); // another grouping: other sections, nothing folded
  const fold = $('#q-fold'); if (fold) fold.addEventListener('click', () => set('gc', sections.every(sec => shut.has(sec.name)) ? '' : sections.map(sec => sec.name).join('|')));
  view.querySelectorAll('tr.group').forEach(tr => tr.addEventListener('click', () => { const g = tr.dataset.g; if (shut.has(g)) shut.delete(g); else shut.add(g); set('gc', [...shut].join('|')); }));
  $('#q-show').addEventListener('change', e => set('sh', e.target.value));
  const vp = $('#viewpick'), vpop = vp.querySelector('.pop');
  vp.querySelector('button.pick').addEventListener('click', () => { const open = vpop.hidden; document.querySelectorAll('.picker .pop').forEach(p => { p.hidden = true; }); vpop.hidden = !open; filterPopOpen = open; });
  vpop.addEventListener('click', e => {
    const l = e.target.closest('.opt'); if (!l) return; e.preventDefault();
    filterPopOpen = true;
    // none drops every filter that is on, one at a time, so what was typed by hand stays
    set('q', l.dataset.v ? toggleFilter(S.q, l.dataset.v) : filtersOn(S.q).reduce((q, f) => toggleFilter(q, f[0]), S.q));
  });
  bindColumnPicker(cols);
  $('#q-export').addEventListener('click', openExport);
  $('#q-open').addEventListener('click', openAll);
  view.querySelectorAll('th[data-sort]').forEach(th => th.addEventListener('click', e => { if (e.target.closest('.rm')) return; const k = th.dataset.sort; if (S.sort === k) S.dir = S.dir === 'asc' ? '' : 'asc'; else S.dir = ''; set('sort', k); }));
  view.querySelectorAll('th .rm').forEach(x => x.addEventListener('click', e => { e.stopPropagation(); setCols(cols.map(c => c.k).filter(k => k !== x.parentElement.dataset.sort)); }));
  bindColumnDrag(view, cols);
  const more = $('#q-more'); if (more) more.addEventListener('click', () => { queueLimit += 200; renderQueue(view); });
  bindRowClicks(view);
}
// ---- open: every PR in the table in a tab of its own; past a couple of dozen it asks first ----
const OPEN_ASK = 25;
// the table's rows less the folded groups: what is on show, not only the rows drawn so far
function unfoldedRows() {
  const rows = queueRows(), sections = grouped(rows), shut = new Set(S.gc ? S.gc.split('|') : []);
  return sections ? sections.filter(sec => !shut.has(sec.name)).flatMap(sec => sec.prs) : rows;
}
function openTabs(rows) {
  // a browser lets one click open one tab unless pop-ups are allowed for the page: count what it refused
  let blocked = 0;
  for (const p of rows) { const w = window.open(prURL(p.n), '_blank'); if (w) w.opener = null; else blocked++; }
  const box = $('#openbox');
  if (!blocked) { if (box.open) box.close(); return; }
  $('#op-msg').textContent = `the browser blocked ${fmtNum(blocked)} of ${fmtNum(rows.length)} tabs`;
  $('#op-desc').textContent = 'it allows one new tab a click unless pop-ups are allowed for this page — allow them (the icon in the address bar), then open again';
  $('#op-ok').textContent = 'open again'; $('#op-ok').onclick = () => openTabs(rows);
  if (!box.open) box.showModal();
}
function openAll() {
  // on the checks tab, the candidates of the checks not folded, each PR once
  const rows = S.tab === 'checks' ? [...new Set(checkSections(true).flatMap(sec => sec.prs))] : unfoldedRows(); if (!rows.length) return;
  if (rows.length <= OPEN_ASK) { openTabs(rows); return; }
  $('#op-msg').textContent = `open ${fmtNum(rows.length)} PRs in new tabs?`;
  $('#op-desc').textContent = S.tab === 'checks' ? 'every candidate of the checks that are not folded — fold checks, or narrow the filter, to open fewer' : 'every PR in the table, the folded groups left out — narrow the filter, or fold groups, to open fewer';
  $('#op-ok').textContent = `open ${fmtNum(rows.length)} tabs`; $('#op-ok').onclick = () => openTabs(rows);
  $('#openbox').showModal();
}
$('#op-cancel').addEventListener('click', () => $('#openbox').close());
// ---- export: the table as it stands — the filter, show, the sort, the grouping — with the columns picked in a dialog ----
// a cell's text is what the table shows, tags stripped; the columns the table abbreviates or decorates export in full
const plainText = html => { const t = document.createElement('template'); t.innerHTML = html; return t.content.textContent.replace(/\s+/g, ' ').trim(); };
const EXPORT_VALUE = {
  n: p => p.n, t: p => p.t, a: p => p.a, ef: p => p.ef, d: p => p.d ? 'yes' : '', props: p => propCount(p) || '',
  tests: p => p.tc ? p.tc.s : '', tfail: p => p.tc ? p.tc.f : '', tpass: p => p.tc ? p.tc.p : '', tsince: p => testsSince(p) ?? '',
  kd: p => p.kd || '',
  cifail: p => (p.cif || []).join(', '), behind: p => p.bh ?? '', behindfor: p => behindFor(p) == null ? '' : Math.round(behindFor(p)), drift: p => p.cdr ?? '',
  k: p => p.k.join(', '), sv: p => p.sv.join(', '), l: p => p.l.join(', '), rw: p => p.rw.join(', '), rb: p => p.rb.join(', '), ab: p => p.ab.join(', '),
  cb: p => p.cb.map(c => c.l).join(', '), li: p => (p.li || []).join(', '), checks: p => p.checks.map(c => c.check).join(', '),
};
// columns only an export has: nothing a table cell would show
const EXPORT_ONLY = [
  { k: 'url', l: 'url', d: 'the link to the PR', x: p => prURL(p.n) },
  { k: 'failedtests', l: 'failed tests', d: 'the names of the tests that failed in the latest builds', x: p => p.tc && p.tc.ft ? p.tc.ft.join(', ') : '' },
  { k: 'propnames', l: 'property names', d: 'the properties the diff adds or changes', x: p => (p.pp || []).join(', ') },
];
const exportCell = (c, p) => { const v = c.x ? c.x(p) : EXPORT_VALUE[c.k] ? EXPORT_VALUE[c.k](p) : plainText(c.f(p)); return v == null ? '' : String(v); };
// the dialog's fields: ticked ones are written, in the order they sit — drag one to move it. It starts as the table
// is (its columns first and in its order, ticked; then every other column; then the export-only ones) and keeps
// what is changed while the page is open, until "as shown" puts it back
let exportFields = null; // [[key, ticked], ...] once the ticks or the order are touched
const exportByKey = () => Object.fromEntries([...Object.values(COL), ...EXPORT_ONLY].map(c => [c.k, c]));
function renderExportFields() {
  const shown = visibleCols().map(c => c.k), by = exportByKey();
  const fields = exportFields || [...shown, ...Object.keys(COL).filter(k => !shown.includes(k)), ...EXPORT_ONLY.map(c => c.k)].map(k => [k, shown.includes(k)]);
  // each field is written from its column, looked up by the remembered key: nothing read back from the page reaches the html
  $('#ex-cols').innerHTML = fields.map(([k, on]) => [by[k], on]).filter(([c]) => c).map(([c, on]) => `<label draggable="true" title="${esc(c.d || '')} — drag to reorder"><input type="checkbox" value="${esc(c.k)}"${on ? ' checked' : ''}>${esc(c.l)}</label>`).join('');
}
const keepExportFields = () => { exportFields = [...$('#ex-cols').querySelectorAll('input')].map(i => [i.value, i.checked]); };
function openExport() {
  // with groups folded, what is on show is what is meant: the box starts ticked, and has nothing to do otherwise
  const folded = S.tab === 'checks' ? checkSections(true).length < checkSections(false).length : unfoldedRows().length < queueRows().length;
  $('#ex-shownonly').checked = folded; $('#ex-shownonly').disabled = !folded;
  $('#ex-shownonly').parentElement.title = folded ? 'leave out the folded groups: export only the groups that are open' : 'no groups are folded, so everything is on show';
  syncExportCount();
  renderExportFields();
  $('#ex-note').textContent = '';
  $('#exportbox').showModal();
}
// drag a field onto another: dropped on its left half it lands before, on its right half after (the grid reads across)
(() => {
  const box = $('#ex-cols'); let dragging = null;
  const clear = () => box.querySelectorAll('.before, .after').forEach(l => l.classList.remove('before', 'after'));
  box.addEventListener('change', keepExportFields);
  box.addEventListener('dragstart', e => { dragging = e.target.closest('label'); if (dragging) { dragging.classList.add('dragging'); e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/plain', dragging.textContent); } });
  box.addEventListener('dragend', () => { if (dragging) dragging.classList.remove('dragging'); dragging = null; clear(); });
  box.addEventListener('dragover', e => {
    const over = e.target.closest('label'); if (!dragging || !over || over === dragging) return;
    e.preventDefault(); clear();
    const r = over.getBoundingClientRect(); over.classList.add(e.clientX < r.left + r.width / 2 ? 'before' : 'after');
  });
  box.addEventListener('drop', e => {
    const over = e.target.closest('label'); if (!dragging || !over || over === dragging) return;
    e.preventDefault();
    const r = over.getBoundingClientRect(); over.insertAdjacentElement(e.clientX < r.left + r.width / 2 ? 'beforebegin' : 'afterend', dragging);
    clear(); keepExportFields();
  });
})();
// links: the PR number as a link — a HYPERLINK formula in the csv (a csv has no links of its own; spreadsheets
// run the formula), a real anchor in the copied table
// the dialog's choices: the ticked fields in the order they sit, the rows, their sections when grouped
function exportPick() {
  const by = exportByKey(), cols = [...$('#ex-cols').querySelectorAll('input:checked')].map(i => by[i.value]).filter(Boolean);
  const rows = exportRows();
  return { links: $('#ex-links').checked, cols, rows, sections: S.tab === 'checks' ? checkSections($('#ex-shownonly').checked) : grouped(rows) };
}
// only shown: the folded groups are left out, as open leaves them out — every row of the groups that are open, drawn yet or not
const exportRows = () => S.tab === 'checks' ? checkSections($('#ex-shownonly').checked).flatMap(sec => sec.prs) : ($('#ex-shownonly').checked ? unfoldedRows() : queueRows());
const syncExportCount = () => { $('#ex-n').textContent = `${fmtNum(exportRows().length)} PRs`; };
$('#ex-shownonly').addEventListener('change', syncExportCount);
// markdown: a list, a PR a line — the number (linked), the title, then the other picked columns as label: value.
// A list and not a table because slack draws no tables; the html beside it is the same list for a rich paste,
// which is what slack takes: links, bullets, and the titles' code spans, all live
function exportMarkdown() {
  const { links, cols, rows, sections } = exportPick();
  const rest = cols.filter(c => c.k !== 'n' && c.k !== 't'), blank = new Set(['', '—', 'none']);
  const extras = p => rest.map(c => [c.l, exportCell(c, p)]).filter(([, v]) => !blank.has(v));
  const code = t => esc(t).replace(/`([^`]+)`/g, '<code>$1</code>');
  const md = [], html = [];
  for (const sec of sections || [{ prs: rows }]) {
    if (sections) { md.push(`${md.length ? '\n' : ''}**${sec.name}** (${sec.prs.length})`); html.push(`<p><b>${esc(sec.name)}</b> (${sec.prs.length})</p>`); }
    html.push('<ul>');
    for (const p of sec.prs) {
      const ex = extras(p);
      md.push(`- ${links ? `[#${p.n}](${prURL(p.n)})` : '#' + p.n} ${p.t}${ex.length ? ' — ' + ex.map(([l, v]) => `${l}: ${v}`).join(' · ') : ''}`);
      html.push(`<li>${links ? `<a href="${prURL(p.n)}">#${p.n}</a>` : '#' + p.n} ${code(p.t)}${ex.length ? ' — ' + ex.map(([l, v]) => `${esc(l)}: ${esc(v)}`).join(' · ') : ''}</li>`);
    }
    html.push('</ul>');
  }
  return { text: md.join('\n') + '\n', html: html.join(''), n: rows.length };
}
function exportTable(sep) {
  const { links, cols, rows, sections } = exportPick(); // grouped: the sections' order, the group in a column of its own
  const cell = v => sep === ',' ? (/[",\n]/.test(v) ? '"' + v.replace(/"/g, '""') + '"' : v) : v.replace(/[\t\n]+/g, ' ');
  const lines = [[...(sections ? ['group'] : []), ...cols.map(c => c.l)].map(cell).join(sep)];
  const value = (c, p) => links && sep === ',' && c.k === 'n' ? `=HYPERLINK("${prURL(p.n)}",${p.n})` : exportCell(c, p);
  // the same table as html, for the clipboard: what a spreadsheet or a doc pastes with the links live
  const html = ['<table><tr>' + [...(sections ? ['group'] : []), ...cols.map(c => c.l)].map(h => `<th>${esc(h)}</th>`).join('') + '</tr>'];
  for (const sec of sections || [{ prs: rows }]) for (const p of sec.prs) {
    lines.push([...(sections ? [sec.name] : []), ...cols.map(c => value(c, p))].map(cell).join(sep));
    html.push('<tr>' + (sections ? `<td>${esc(sec.name)}</td>` : '') + cols.map(c => `<td>${links && c.k === 'n' ? `<a href="${prURL(p.n)}">${p.n}</a>` : esc(exportCell(c, p))}</td>`).join('') + '</tr>');
  }
  return { text: lines.join('\n') + '\n', html: html.join('') + '</table>', n: rows.length, cols: cols.length };
}
$('#ex-all').addEventListener('click', () => { $('#ex-cols').querySelectorAll('input').forEach(i => { i.checked = true; }); keepExportFields(); });
$('#ex-none').addEventListener('click', () => { $('#ex-cols').querySelectorAll('input').forEach(i => { i.checked = false; }); keepExportFields(); });
$('#ex-shown').addEventListener('click', () => { exportFields = null; renderExportFields(); }); // the table's columns and order again
$('#ex-cancel').addEventListener('click', () => $('#exportbox').close());
$('#ex-csv').addEventListener('click', () => {
  const out = exportTable(','); if (!out.cols) { $('#ex-note').textContent = 'pick at least one column'; return; }
  const a = document.createElement('a');
  a.href = URL.createObjectURL(new Blob(['\ufeff' + out.text], { type: 'text/csv;charset=utf-8' })); // the mark has excel read it as utf-8
  a.download = `prawn-prs-${new Date().toISOString().slice(0, 10)}.csv`;
  document.body.appendChild(a); a.click(); a.remove(); setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  $('#exportbox').close();
});
// copied as tab-separated text, which a spreadsheet pastes into cells
// with html beside the text when there is some, so the paste keeps the links
const exportCopy = (text, what, html) => (!navigator.clipboard ? Promise.reject(new Error('no clipboard'))
  : html && window.ClipboardItem ? navigator.clipboard.write([new ClipboardItem({ 'text/plain': new Blob([text], { type: 'text/plain' }), 'text/html': new Blob([html], { type: 'text/html' }) })])
  : navigator.clipboard.writeText(text))
  .then(() => { $('#ex-note').textContent = `copied ${what}`; }, () => { $('#ex-note').textContent = 'the browser would not copy'; });
$('#ex-copy').addEventListener('click', () => { const out = exportTable('\t'); if (!out.cols) { $('#ex-note').textContent = 'pick at least one column'; return; } exportCopy(out.text, `${fmtNum(out.n)} rows as a table`, out.html); });
$('#ex-md').addEventListener('click', () => { const out = exportMarkdown(); exportCopy(out.text, `${fmtNum(out.n)} PRs as markdown`, out.html); });
$('#ex-nums').addEventListener('click', () => { const rows = exportRows(); exportCopy(rows.map(p => p.n).join(' '), `${fmtNum(rows.length)} PR numbers`); });
// the columns picker: a popover of every column, checked when shown; a click adds it at the end or removes it
const colPicker = cols => { const shown = new Set(cols.map(c => c.k)); return `<span class="picker" id="colpick"><button class="plain pick on" type="button">columns <span class="dim">${cols.length}</span></button>
      <div class="pop" hidden><input type="search" placeholder="filter…"><div class="opt reset"><span class="sw all"></span><span class="name">reset to the ${S.tab === 'data' ? "prs tab's" : 'default'} columns</span></div><div class="rows">${Object.values(COL).map(c => `<div class="opt${shown.has(c.k) ? ' on' : ''}" data-v="${c.k}" data-name="${esc((c.l || c.d).toLowerCase())}" title="${esc(c.d)}"><span class="sw"></span><span class="name">${esc(c.l || '(blank)')}</span><span class="desc">${esc(c.d)}</span></div>`).join('')}</div></div></span>`; };
function bindColumnPicker(cols) {
  const el = $('#colpick'), pop = el.querySelector('.pop'), search = pop.querySelector('input');
  el.querySelector('button.pick').addEventListener('click', () => { const open = pop.hidden; document.querySelectorAll('.picker .pop').forEach(p => { p.hidden = true; }); pop.hidden = !open; if (open) { search.value = ''; search.dispatchEvent(new Event('input')); search.focus(); } });
  search.addEventListener('input', () => { const f = search.value.toLowerCase(); pop.querySelectorAll('.rows .opt').forEach(l => { l.hidden = !!f && !l.dataset.name.includes(f) && !l.dataset.v.toLowerCase().includes(f); }); });
  pop.addEventListener('click', e => {
    const l = e.target.closest('.opt'); if (!l) return; e.preventDefault();
    if (l.classList.contains('reset')) { set(colsKey(), ''); return; }
    const keys = cols.map(c => c.k), v = l.dataset.v;
    if (keys.includes(v)) { if (keys.length > 1) setCols(keys.filter(k => k !== v)); } else setCols([...keys, v]);
  });
}
// drag a header onto another to move it before (left half) or after (right half)
function bindColumnDrag(view, cols) {
  const heads = [...view.querySelectorAll('th[draggable]')];
  let dragging = null;
  const clear = () => heads.forEach(t => t.classList.remove('dragging', 'before', 'after'));
  for (const th of heads) {
    th.addEventListener('dragstart', e => { dragging = th.dataset.sort; th.classList.add('dragging'); e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/x-prawn-col', dragging); });
    th.addEventListener('dragend', () => { dragging = null; clear(); });
    th.addEventListener('dragover', e => {
      if (!dragging || th.dataset.sort === dragging) return; e.preventDefault();
      const r = th.getBoundingClientRect(), after = e.clientX > r.left + r.width / 2;
      th.classList.toggle('before', !after); th.classList.toggle('after', after);
    });
    th.addEventListener('dragleave', () => th.classList.remove('before', 'after'));
    th.addEventListener('drop', e => {
      e.preventDefault(); if (!dragging || th.dataset.sort === dragging) return;
      const after = th.classList.contains('after'); clear();
      const keys = cols.map(c => c.k).filter(k => k !== dragging);
      keys.splice(keys.indexOf(th.dataset.sort) + (after ? 1 : 0), 0, dragging);
      dragging = null; setCols(keys);
    });
  }
}
function bindRowClicks(view) {
  // the view element outlives its content: bind once, or every re-render stacks another toggle on each row click
  if (view.rowsBound) return; view.rowsBound = true;
  view.addEventListener('click', e => {
    const tl = e.target.closest('[data-timeline]'); if (tl) { e.stopPropagation(); openTimeline(tl.dataset.timeline); return; }
    const clk = e.target.closest('.clk');
    if (clk) { e.stopPropagation(); if (clk.dataset.author) set('a', clk.dataset.author); else if (clk.dataset.svc) set('svc', clk.dataset.svc); else if (clk.dataset.label) set('l', clk.dataset.label); else if (clk.dataset.reviewer) set('q', 'reviewer:' + clk.dataset.reviewer); else if (clk.dataset.group) set('g', clk.dataset.group); else if (clk.dataset.kind) set('k', clk.dataset.kind); else if (clk.dataset.q != null) { S.q = clk.dataset.q; set('tab', 'prs'); } return; }
    const tr = e.target.closest('tr.pr'); if (!tr) return;
    set('open_n', S.open_n === tr.dataset.n ? '' : tr.dataset.n);
  });
}
// a PR's detail: what the change is, its life as a bar of states, the facts two to a line, then a card each for ci, tests, review,
// merge and court and a red box naming what fails. The timeline opens in a window of its own.
const card = (label, value, cls, ...sub) => `<div class="card"><div class="k">${label}</div><div class="v ${cls || ''}">${value}</div>${sub.filter(Boolean).map(x => `<div class="d">${x}</div>`).join('')}</div>`;
function ciCard(p) {
  const ran = p.ci !== 'approval' && p.cir ? `ran ${fmtDays(ciAge(p))} ago` : '';
  const drift = p.ci !== 'approval' && p.cdr ? `base has moved ${commits(p.cdr)} since` : '';
  const why = { approval: 'waiting for a maintainer to approve the run', expired: 'github dropped the results (400 days)' }[p.ci] || (p.ci ? '' : 'no checks on the head commit');
  const v = p.ci === 'failing' && p.cis ? `failing ${fmtDays(failingFor(p))}` : ciName(p);
  return card('ci', v, CI_CLASS[p.ci] || 'dim', why || ran, drift);
}
function testsCard(p) {
  const t = p.tc;
  if (!t) return card('tests', 'never run', 'dim', 'no teamcity build');
  const counts = t.f ? `<span class="bad">${fmtNum(t.f)} failed</span> · ${fmtNum(t.p)} passed` : `${fmtNum(t.p)} passed`;
  const since = t.cs ? `<span class="mid">${commits(t.cs)} pushed since</span>` : t.cs === 0 ? 'nothing pushed since' : '';
  return card('tests', t.s, TC_CLASS[t.s], counts, [t.at ? `ran ${fmtDays(testAge(p))} ago` : '', since].filter(Boolean).join(' · '));
}
function reviewCard(p) {
  const rd = (p.rd || '').replace(/_/g, ' ').toLowerCase();
  const [v, cls] = p.approved ? ['approved', 'ok'] : rd === 'changes requested' ? ['changes requested', 'mid'] : p.rv ? ['reviewed', ''] : ['not reviewed', 'dim'];
  const first = p.fr < 0 ? '<span class="bad">no maintainer response</span>' : `first response ${fmtDays(p.fr)}`;
  return card('review', v, cls, `${p.ap} approval${p.ap === 1 ? '' : 's'} · ${p.rr} round${p.rr === 1 ? '' : 's'} of changes`, first);
}
function mergeCard(p) {
  const v = p.mg === 'conflicting' ? 'conflicting' : p.mg === 'mergeable' ? 'mergeable' : p.mg || 'unknown';
  return card('merge', v, { conflicting: 'bad', mergeable: 'ok' }[v] || 'dim', p.bh == null ? '' : p.bh ? `${commits(p.bh)} behind its base${p.bhs ? `, for ${fmtDays(behindFor(p))}` : ''}` : 'up to date with its base');
}
function detail(p) {
  const start = p.c, end = p.x || NOW, span = Math.max(end - start, 1);
  const life = p.iv.map(iv => `<span style="width:${(((iv.e || NOW) - iv.s) / span * 100).toFixed(2)}%;background:${STATE_COLORS[iv.st]}" title="${STATE_NAMES[iv.st]} ${fmtDate(iv.s)} → ${iv.e ? fmtDate(iv.e) : 'now'} (${fmtDays(((iv.e || NOW) - iv.s) / DAY)})"></span>`).join('')
    // ci failing: red stripes over the bar from when it was first seen red to now, whatever state the PR was in under them
    + (p.s === 'open' && p.ci === 'failing' && p.cis ? (() => { const left = Math.min(Math.max((p.cis - start) / span * 100, 0), 99); return `<span class="cifail" style="left:${left.toFixed(2)}%;width:${(100 - left).toFixed(2)}%" title="ci failing since ${fmtDate(p.cis)} (${fmtDays(failingFor(p))})"></span>`; })() : '');
  const open = p.s === 'open', t = p.tc;

  const cards = open
    ? [ciCard(p), HAS_TESTS ? testsCard(p) : '', reviewCard(p), mergeCard(p), p.ct ? card('court', `${p.ct}'s`, `court-${p.ct}`, `for ${fmtDays(p.cd)}`, p.wc ? `waiting-response ${fmtDays(p.wd)} over ${p.wc} cycle${p.wc === 1 ? '' : 's'}` : '') : '']
    : [card('state', p.s, `st-${p.s}`, p.x ? fmtDate(p.x) : '', p.mb ? `by ${esc(p.mb)}` : ''), reviewCard(p)];

  // what is failing, named, where it is easy to see: the checks, the tests, the builds they failed in
  const failing = [];
  if (open && p.ci === 'failing') failing.push(`<div><b>failing checks</b>${(p.cif || []).length ? p.cif.map(c => `<span class="badge clk" data-q="failing:${esc(c)}">${esc(c)}</span>`).join('') : '<span class="dim">which check is not known</span>'}</div>`);
  if (open && t && t.s === 'failing') {
    const builds = t.b.filter(b => b.st === 'failing').map(b => `<a href="${esc(b.u)}" target="_blank" rel="noopener" onclick="event.stopPropagation()">${esc(b.sv)}${b.f ? ' · ' + fmtNum(b.f) : ''} ↗</a>`).join(', ');
    failing.push(`<div><b>failing builds</b>${builds}</div>`);
    if (t.ft && t.ft.length) failing.push(`<div><b>failed tests</b>${t.ft.slice(0, 12).map(n => `<span class="clk" data-q="failedtest:${esc(n)}">${esc(n)}</span>`).join(', ')}${t.ft.length > 12 ? ` <span class="dim">+${t.ft.length - 12}</span>` : ''}</div>`);
  }

  // the facts, label and value, two to a line
  const fact = (k, v) => v ? `<div class="f"><b>${k}</b><span>${v}</span></div>` : '';
  const facts = [
    fact('author', `<span class="clk" data-author="${esc(p.a)}">${esc(p.a)}</span> <span class="dim">${esc(p.as.toLowerCase().replace('_', ' '))}, ${esc(p.g)}</span>`),
    fact('services', p.sv.map(sv => `<span class="badge clk" data-svc="${esc(sv)}">${esc(sv)}</span>`).join('')),
    fact('reviewers', p.rw.length ? p.rw.map(r => `<span class="clk" data-reviewer="${esc(r)}">${esc(r)}</span>`).join(', ') : '<span class="dim">none</span>'),
    fact('file types', p.k.map(k => `<span class="badge clk" data-kind="${k}">${k}</span>`).join('')),
    fact('changes by', p.cb.length ? p.cb.map(c => `<span class="clk" data-q="changesby:${esc(c.l)}">${esc(c.l)}</span><span class="dim">(×${c.n} ✎${c.c})</span>`).join(', ') : ''),
    fact('approved by', p.ab.length ? logins(p.ab, 'approvedby', 6) : ''),
    fact('labels', p.l.map(l => `<span class="badge clk" data-label="${esc(l)}">${esc(l)}</span>`).join('')),
    fact('activity', `${p.rv} review${p.rv === 1 ? '' : 's'} · ${p.rc} review comment${p.rc === 1 ? '' : 's'} · 💬 ${p.cm} · 👍 ${p.th}${p.lastMaintBy ? ` · last maintainer ${esc(p.lastMaintBy)}` : ''}`),
    open && t ? fact('test builds', t.b.map(b => `<a href="${esc(b.u)}" target="_blank" rel="noopener" class="${TC_CLASS[b.st]}" onclick="event.stopPropagation()">${esc(b.sv)}</a>`).join(', ') + (t.n > t.b.length ? ` <span class="dim">· ${fmtNum(t.n)} runs in all</span>` : '')) : '',
    fact('closes', p.li && p.li.length ? p.li.map(n => `<a href="https://github.com/${D.repo}/issues/${n}" target="_blank" rel="noopener">#${n}</a>`).join(' ') : ''),
    fact('milestone', p.ms ? esc(p.ms) : ''),
    fact('checks', p.checks.map(c => `<span class="badge">${esc(c.check)}${c.score ? ' ' + c.score : ''}</span> ${c.ev.map(line => esc(line.map(b => b.t).join(' '))).join(' · ')}`).join('<br>')),
  ].filter(Boolean).join('');

  // what the change is, first and large: its kind, its size, and how much reviewing it takes
  const headline = `<div class="headline">${p.kd ? `<span class="kd clk" data-q="kind:&quot;${esc(p.kd)}&quot;">${esc(p.kd)}</span>` : ''}${propCount(p) ? `<span class="pp">${p.pp.map(n => `<span class="clk" data-q="prop:${esc(n)}">${esc(n)}</span>`).join(', ')}</span>` : ''}<span class="sz"><span class="ok">+${fmtNum(p.ad)}</span> <span class="bad">−${fmtNum(p.de)}</span></span><span>${p.f} file${p.f === 1 ? '' : 's'}</span><span class="eff" title="review effort ${p.ef}/5">${'<b>●</b>'.repeat(p.ef)}${'○'.repeat(5 - p.ef)}</span><span class="dim">effort ${p.ef}/5</span></div>`;
  return `<div class="detail">
    ${headline}
    <div class="lifehead"><b>life</b>${fmtDate(p.c)} → ${p.x ? fmtDate(p.x) : 'now'} · ${fmtDays((end - start) / DAY)} ${p.s}<button class="plain" data-timeline="${p.n}" title="every event in its life, in a window">timeline · ${p.ev.length} events</button></div>
    <div class="life">${life}</div>
    <div class="legend">${STATE_NAMES.map((s, i) => `<span title="${STATE_DESC[i]}"><i class="box" style="--c:${STATE_COLORS[i]}"></i>${s}</span>`).join('')}<span title="ci has been failing since then: the stripes lie over whatever state the PR was in"><i class="box stripes"></i>ci failing</span></div>
    <div class="facts">${facts}</div>
    <div class="cards">${cards.join('')}</div>
    ${failing.length ? `<div class="failing">${failing.join('')}</div>` : ''}
  </div>`;
}
// the timeline window: a PR's events, newest first, maintainers' in their colour
function openTimeline(n) {
  const p = D.prs.find(x => String(x.n) === String(n)); if (!p) return;
  const evLine = e => { const m = MAINT.has((e.w || '').toLowerCase()) && e.w.toLowerCase() !== p.lo; return `<div class="${m ? 'maint' : ''}"><span class="t">${fmtDate(e.t)}</span><span class="w">${esc(e.w || '')}</span> ${esc(e.k)}${e.x ? ' <span class="dim">' + esc(e.x.length > 90 ? e.x.slice(0, 89) + '…' : e.x) + '</span>' : ''}</div>`; };
  $('#tl-head').innerHTML = `<a href="https://github.com/${D.repo}/pull/${p.n}" target="_blank" rel="noopener">#${p.n}</a> ${esc(p.t)}`;
  $('#tl-desc').textContent = `${p.ev.length} events · ${fmtDate(p.c)} → ${p.x ? fmtDate(p.x) : 'now'}`;
  $('#tl-events').innerHTML = p.ev.slice().reverse().map(evLine).join('') || '<div class="dim">no events fetched</div>';
  $('#tlbox').showModal(); $('#tl-events').scrollTop = 0;
}

// ---- the trends tab ---- (the metric tree, presets, and combinable panels live in explore-trends.js)
// a day counts an interval when the interval covers the day's start: a PR is in exactly one state per day,
// so stacked states add up to the open count (an interval still running covers today)
function dayRange(s, e, d0, d1) { return [Math.max(Math.ceil(s / DAY), d0), Math.min(e ? Math.floor((e - 1) / DAY) : d1, d1)]; }
function dailyStates(prs, from, to) {
  const d0 = Math.floor(from / DAY), d1 = Math.floor(to / DAY), n = d1 - d0 + 1;
  const counts = STATE_NAMES.map(() => new Int32Array(n));
  const court = [new Int32Array(n), new Int32Array(n)]; // maintainer, author
  for (const p of prs) for (const iv of p.iv) {
    const [a, b] = dayRange(iv.s, iv.e, d0, d1);
    const arr = counts[iv.st], c = court[iv.st === 0 || iv.st === 2 ? 1 : 0];
    for (let d = a; d <= b; d++) { arr[d - d0]++; c[d - d0]++; }
  }
  return { d0, n, counts, court };
}
function renderTrends(view) { renderMetrics(view); }

// ---- the suggested tab: the easy wins, by category ----
// Each category is a test over the open PRs the filter matches, with a line
// saying why the PR is cheap. A PR lands in every category it fits; the
// tally at the top counts it once.
const only = (p, ...ks) => p.k.length && p.k.every(k => ks.includes(k));
const fixShaped = p => p.l.includes('bug') || /\b(fix|fixes|fixed|correct|typo|crash|panic|nil)\b/i.test(p.t);
// each category: name, description, the test, the why-line, and a slug the prs tab's query filters by (suggested:<slug>)
const CATEGORIES = [
  ['approved, unmerged', `a maintainer's approval (the ${D.maintainerGroup} group's, not any collaborator's) is on it and nothing has been requested since — merge, or say what is missing`, p => p.approved, p => `approved by ${p.rw.filter(r => MAINT.has(r)).slice(0, 2).join(', ') || 'a maintainer'} · idle ${fmtDays(p.idle)}`, 'approved'],
  ['provider docs only', 'the website docs and nothing else: a read-through', p => only(p, 'docs', 'changelog'), p => `${p.k.join(' + ')} only · +${p.ad}/−${p.de}`, 'docs'],
  ['examples only', 'example configurations under examples/, no provider code', p => only(p, 'examples', 'changelog'), p => `${p.k.join(' + ')} only · +${p.ad}/−${p.de} · ${p.f} files`, 'examples'],
  ['contributing docs only', 'the contributor guide under contributing/, nothing users see', p => only(p, 'contributing', 'changelog'), p => `${p.k.join(' + ')} only · +${p.ad}/−${p.de}`, 'contributing'],
  ['tests and ci only', 'test and workflow changes, nothing user-facing', p => only(p, 'tests', 'ci'), p => `${p.k.join(' + ')} only · +${p.ad}/−${p.de}`, 'tests'],
  ['dependency bumps', 'vendor and go.mod changes only', p => only(p, 'vendor'), p => `vendor only · +${p.ad}/−${p.de} · ${p.f} files`, 'deps'],
  ['small fixes waiting on us', 'effort 1–2, fix-shaped, in the maintainers\' court', p => p.ef <= 2 && p.ct === 'maintainer' && fixShaped(p) && !p.approved && !only(p, 'docs', 'examples', 'contributing', 'changelog', 'tests', 'ci', 'vendor'), p => `effort ${p.ef} · ${p.sv.join(', ') || 'no service'} · ${p.fr < 0 ? 'never answered' : 'last maintainer touch ' + fmtDays(daysSince(p.lm)) + ' ago'}`, 'fixes'],
  ['small enhancements, one service', 'effort 1–2, a single service, in the maintainers\' court', p => p.ef <= 2 && p.ct === 'maintainer' && p.sv.length === 1 && !fixShaped(p) && !p.approved && !only(p, 'docs', 'examples', 'contributing', 'changelog', 'tests', 'ci', 'vendor'), p => `effort ${p.ef} · ${p.sv[0]} · ${p.li && p.li.length ? 'closes #' + p.li.join(', #') : 'no linked issue'}`, 'enhancements'],
  ['author pushed after changes requested', 'a re-review of something already read once; small ones first', p => p.ct === 'maintainer' && p.rr > 0 && !p.approved && p.ef <= 3, p => `${p.rr} round${p.rr === 1 ? '' : 's'} · pushed ${fmtDays(daysSince(p.lu))} ago · effort ${p.ef}`, 'rereview'],
  ['small and never answered', 'effort 1–2 with no maintainer response yet — a first look is cheap and unblocks the author', p => p.ef <= 2 && p.fr < 0 && !p.d, p => `effort ${p.ef} · opened ${fmtDays(p.age)} ago · ${p.sv.join(', ') || 'no service'}`, 'unanswered'],
  ['likely closes', 'the checks\' candidates the AI scored 0.8 or higher — a close, not a review', p => p.checks.some(c => +c.score >= 0.8), p => p.checks.filter(c => +c.score >= 0.8).map(c => `close ${c.check} ${c.score}`).join(' · '), 'closes'],
];
function suggestions() {
  const open = M.filter(p => p.s === 'open');
  return CATEGORIES.map(([name, desc, test, why, slug]) => ({ name, desc, why, slug, prs: open.filter(test).sort((a, b) => a.ef - b.ef || b.age - a.age) })).filter(c => c.prs.length);
}
// the by-service view: every service with small PRs in the maintainers' court, most first — read the service
// once, review them together. A one-service PR counts for its service; a PR touching several counts for each.
function serviceClusters() {
  const bySvc = {};
  for (const p of M) if (p.s === 'open' && p.ef <= 2 && p.ct === 'maintainer') for (const sv of p.sv) (bySvc[sv] ||= []).push(p);
  return Object.entries(bySvc).sort((a, b) => b[1].length - a[1].length || a[0].localeCompare(b[0])).map(([sv, ps]) => ({
    name: `${sv}: ${ps.length} small PR${ps.length === 1 ? '' : 's'}`, desc: 'effort 1–2, in the maintainers\' court — one context load for all of them', q: `svc:${sv} effort<3 court:maintainer`,
    why: p => `effort ${p.ef} · ${fixShaped(p) ? 'fix' : 'enhancement'} · ${p.fr < 0 ? 'never answered' : 'idle ' + fmtDays(p.idle)}${p.sv.length > 1 ? ' · also ' + p.sv.filter(x => x !== sv).join(', ') : ''}`,
    prs: ps.sort((a, b) => a.ef - b.ef || b.age - a.age) }));
}
const SUGGEST_COLS = ['n', 't', 'a', 'ef', 'age', 'idle', 'sv'].map(k => COL[k]);
function renderSuggested(view) {
  const bySvc = S.sg === 'svc';
  tabControls(`<span class="seg" id="sg-mode">${[['', 'by category'], ['svc', 'by service']].map(([v, l]) => `<button data-v="${v}" class="${S.sg === v ? 'on' : ''}">${l}</button>`).join('')}</span>`);
  $('#sg-mode').addEventListener('click', e => { const b = e.target.closest('button'); if (b) set('sg', b.dataset.v); });
  const cats = bySvc ? serviceClusters() : suggestions();
  const all = new Map(); for (const c of cats) for (const p of c.prs) all.set(p.n, p);
  const effort = [...all.values()].reduce((n, p) => n + p.ef, 0);
  view.innerHTML = `
    <div class="tiles">
      <div class="tile"><div class="v">${fmtNum(all.size)}</div><div class="k">easy wins</div><div class="d">of ${fmtNum(M.filter(p => p.s === 'open').length)} open PRs matching · ${cats.length} ${bySvc ? 'services' : 'categories'}</div></div>
      <div class="tile"><div class="v">${fmtNum(effort)}</div><div class="k">effort points</div><div class="d">~${fmtNum(effort / 12, 0)} review days for all of them</div></div>
      ${cats.slice(0, 5).map(c => `<div class="tile"><div class="v">${fmtNum(c.prs.length)}</div><div class="k">${esc(c.name)}</div><div class="d">Σ effort ${c.prs.reduce((n, p) => n + p.ef, 0)}</div></div>`).join('')}
    </div>
    <div class="panels">${cats.map((c, ci) => `<div class="panel wide table"><h2>${esc(c.name)}<span class="desc">${esc(c.desc)}</span><span class="val">${c.prs.length}</span></h2>
      <table><thead><tr>${SUGGEST_COLS.map(c => `<th class="${c.cls}">${c.l}</th>`).join('')}<th>why</th></tr></thead><tbody>
      ${c.prs.slice(0, 15).map(p => `<tr class="pr" data-n="${p.n}">${SUGGEST_COLS.map(c => `<td class="${c.cls}">${c.f(p)}</td>`).join('')}<td class="dim">${esc(c.why(p))}</td></tr>${S.open_n === String(p.n) ? `<tr class="detail"><td colspan="${SUGGEST_COLS.length + 1}">${detail(p)}</td></tr>` : ''}`).join('')}
      ${c.prs.length > 15 ? `<tr><td colspan="${SUGGEST_COLS.length + 1}" class="more">${c.prs.length - 15} more — <a class="clk" data-q="${esc(c.q || 'suggested:' + c.slug)}">see them all in data</a></td></tr>` : ''}
      </tbody></table></div>`).join('')}
      ${!cats.length ? `<div class="panel wide"><div class="empty">${bySvc ? 'no small PRs in the maintainers\' court among the matching open PRs' : 'no easy wins among the matching open PRs'} — widen the filter</div></div>` : ''}
    </div>`;
  view.querySelectorAll('a.clk[data-q]').forEach(a => a.addEventListener('click', e => { e.stopPropagation(); if (a.dataset.q) S.q = a.dataset.q; set('tab', 'prs'); }));
  bindRowClicks(view);
}

// ---- the people tab ----
function trendArrow(recent, prior) {
  if (recent == null || prior == null) return '<span class="trend-flat">·</span>';
  const d = recent - prior; if (Math.abs(d) < 0.05) return '<span class="trend-flat" title="steady">→</span>';
  return d > 0 ? `<span class="trend-up" title="${Math.round(prior * 100)}% → ${Math.round(recent * 100)}%">▲</span>` : `<span class="trend-down" title="${Math.round(prior * 100)}% → ${Math.round(recent * 100)}%">▼</span>`;
}
function sortable(rows, key, defKey, cols) {
  const [k, dir] = (key || defKey).split(':'); const col = cols.find(c => c[0] === k) || cols.find(c => c[0] === defKey.split(':')[0]);
  rows.sort((a, b) => { const x = col[3](a), y = col[3](b); const r = typeof x === 'number' ? (y ?? -1e9) - (x ?? -1e9) : String(x).localeCompare(String(y)); return dir === 'asc' ? -r : r; });
  return k;
}
function table(cols, rows, sortKey, stateKey, limit = 100) {
  return `<table><thead><tr>${cols.map(c => `<th class="${c[2]} ${sortKey === c[0] ? 'on' + ((S[stateKey] || '').endsWith(':asc') ? ' asc' : '') : ''}" data-tsort="${c[0]}" data-tkey="${stateKey}">${c[1]}</th>`).join('')}</tr></thead><tbody>
    ${rows.slice(0, limit).map(r => `<tr>${cols.map(c => `<td class="${c[2]}">${c[4](r)}</td>`).join('')}</tr>`).join('')}
    ${rows.length > limit ? `<tr><td colspan="${cols.length}" class="more">${rows.length - limit} more — narrow the filter</td></tr>` : ''}
    ${!rows.length ? `<tr><td colspan="${cols.length}" class="empty">nobody matches</td></tr>` : ''}</tbody></table>`;
}
function bindTableSorts(view) {
  view.querySelectorAll('th[data-tsort]').forEach(th => th.addEventListener('click', () => {
    const key = th.dataset.tkey, k = th.dataset.tsort, cur = S[key] || '';
    set(key, cur === k ? k + ':asc' : k);
  }));
}
function monthlyCounts(ps, months) { const out = new Array(months.length).fill(0); for (const p of ps) { const i = bucketIndex(months, p.c); if (i >= 0) out[i]++; } return out; }
function renderPeople(view) {
  const from = PERIOD.from || Math.min(...M.map(p => p.c)), months = buckets(from, PERIOD.to, 'month');
  const cut = NOW - 182 * DAY, cut2 = NOW - 365 * DAY;
  const rate = ps => { const r = ps.filter(p => p.s !== 'open'); return r.length >= 3 ? r.filter(p => p.s === 'merged').length / r.length : null; };
  // authors
  const byA = new Map(); for (const p of M) { if (!byA.has(p.lo)) byA.set(p.lo, { login: p.a, g: p.g, prs: [] }); byA.get(p.lo).prs.push(p); }
  const authors = [...byA.values()].map(a => {
    const ps = a.prs, res = ps.filter(p => p.s !== 'open'), merged = ps.filter(p => p.s === 'merged');
    return { ...a, n: ps.length, open: ps.filter(p => p.s === 'open').length, merged: merged.length, closed: ps.filter(p => p.s === 'closed').length,
      rate: rate(ps), ttm: median(merged.map(p => p.td)), rounds: median(merged.map(p => p.rr)), fr: median(ps.filter(p => p.fr >= 0).map(p => p.fr)),
      recent: rate(ps.filter(p => p.c >= cut)), prior: rate(ps.filter(p => p.c >= cut2 && p.c < cut)), effort: median(ps.map(p => p.ef)),
      inCourt: ps.filter(p => p.s === 'open' && p.ct === 'author').length, spark: monthlyCounts(ps, months), last: Math.max(...ps.map(p => p.c)) };
  });
  const ACOLS = [
    ['login', 'author', '', a => a.login, a => `<span class="clk" data-author="${esc(a.login)}">${esc(a.login)}</span> <span class="badge g clk" data-group="${esc(a.g)}" style="--gc:${groupColor(a.g)}">${esc(a.g)}</span>`],
    ['n', 'PRs', 'num', a => a.n, a => fmtNum(a.n)],
    ['open', 'open', 'num', a => a.open, a => fmtNum(a.open)],
    ['merged', 'merged', 'num', a => a.merged, a => fmtNum(a.merged)],
    ['closed', 'closed', 'num', a => a.closed, a => fmtNum(a.closed)],
    ['rate', 'merge rate', 'num', a => a.rate, a => a.rate == null ? '—' : Math.round(a.rate * 100) + '% ' + trendArrow(a.recent, a.prior)],
    ['ttm', 'to merge', 'num', a => a.ttm, a => fmtDays(a.ttm)],
    ['rounds', 'rounds', 'num', a => a.rounds, a => a.rounds == null ? '—' : fmtNum(a.rounds, 1)],
    ['fr', 'first resp', 'num', a => a.fr, a => fmtDays(a.fr)],
    ['effort', 'effort', 'num', a => a.effort, a => a.effort == null ? '—' : fmtNum(a.effort, 1)],
    ['inCourt', 'in their court', 'num', a => a.inCourt, a => a.inCourt || '—'],
    ['spark', 'opened / month', '', a => a.n, a => sparkline(a.spark, groupColor(a.g))],
    ['last', 'last PR', 'num', a => a.last, a => fmtDate(a.last)],
  ];
  const ak = sortable(authors, S.psort, 'n', ACOLS);
  // reviewers: every maintainer touch on the matched PRs
  const byR = new Map();
  for (const p of M) {
    const seen = new Set();
    for (const e of p.ev) {
      const w = (e.w || '').toLowerCase(); if (!MAINT.has(w) || w === p.lo) continue;
      if (!['review', 'comment', 'merge', 'close', 'label+'].includes(e.k)) continue;
      if (!byR.has(w)) byR.set(w, { login: e.w, prs: new Set(), reviews: 0, approvals: 0, changes: 0, comments: 0, merges: 0, closes: 0, first: 0, frDays: [], months: new Array(months.length).fill(0), recent: 0 });
      const r = byR.get(w); r.prs.add(p.n);
      if (e.k === 'review') { r.reviews++; if (e.x === 'approved') r.approvals++; if (e.x === 'changes_requested') r.changes++; }
      else if (e.k === 'comment') r.comments++; else if (e.k === 'merge') r.merges++; else if (e.k === 'close') r.closes++;
      if (e.t >= cut && (e.k === 'review' || e.k === 'comment')) r.recent++;
      if (!seen.has(w) && (e.k === 'review' || e.k === 'comment')) { seen.add(w); const i = bucketIndex(months, e.t); if (i >= 0) r.months[i]++; }
    }
    if (p.fw && byR.has(p.fw.toLowerCase())) { const r = byR.get(p.fw.toLowerCase()); r.first++; r.frDays.push(p.fr); }
  }
  const reviewers = [...byR.values()].map(r => ({ ...r, n: r.prs.size, fr: median(r.frDays), lastTouch: M.filter(p => p.s === 'open' && p.lastMaintBy.toLowerCase() === r.login.toLowerCase()).length }));
  const RCOLS = [
    ['login', 'reviewer', '', r => r.login, r => `<span class="clk" data-reviewer="${esc(r.login)}">${esc(r.login)}</span>`],
    ['n', 'PRs touched', 'num', r => r.n, r => fmtNum(r.n)],
    ['reviews', 'reviews', 'num', r => r.reviews, r => fmtNum(r.reviews)],
    ['approvals', 'approved', 'num', r => r.approvals, r => fmtNum(r.approvals)],
    ['changes', 'changes req.', 'num', r => r.changes, r => fmtNum(r.changes)],
    ['comments', 'comments', 'num', r => r.comments, r => fmtNum(r.comments)],
    ['merges', 'merged', 'num', r => r.merges, r => fmtNum(r.merges)],
    ['closes', 'closed', 'num', r => r.closes, r => fmtNum(r.closes)],
    ['first', 'first responder', 'num', r => r.first, r => fmtNum(r.first)],
    ['fr', 'response', 'num', r => r.fr, r => fmtDays(r.fr)],
    ['lastTouch', 'last touch, open', 'num', r => r.lastTouch, r => r.lastTouch || '—'],
    ['recent', 'last 6mo', 'num', r => r.recent, r => fmtNum(r.recent)],
    ['months', 'PRs / month', '', r => r.n, r => sparkline(r.months, 'var(--accent)')],
  ];
  const rk = sortable(reviewers, S.asort, 'n', RCOLS);
  const groupRows = [...GROUP_NAMES, 'community'].map(g => { const ps = M.filter(p => p.g === g); const merged = ps.filter(p => p.s === 'merged'); return { g, n: ps.length, open: ps.filter(p => p.s === 'open').length, rate: rate(ps), ttm: median(merged.map(p => p.td)), fr: median(ps.filter(p => p.fr >= 0).map(p => p.fr)), authors: new Set(ps.map(p => p.lo)).size }; }).filter(r => r.n);
  tabControls('');
  view.innerHTML = `
    <div class="tiles">${groupRows.map(r => `<div class="tile"><div class="v" style="color:${groupColor(r.g)}"><span class="clk" data-group="${esc(r.g)}" style="color:inherit">${esc(r.g)}</span></div><div class="k">${fmtNum(r.n)} PRs · ${fmtNum(r.authors)} authors</div><div class="d">${r.open} open · merge ${r.rate == null ? '—' : Math.round(r.rate * 100) + '%'} · to merge ${fmtDays(r.ttm)} · response ${fmtDays(r.fr)}</div></div>`).join('')}</div>
    <div class="panels">
    <div class="panel wide table"><h2>authors<span class="desc">${fmtNum(authors.length)} matching · merge rate arrow: last 6 months against the 6 before · click a login to filter</span></h2>${table(ACOLS, authors, ak, 'psort')}</div>
    <div class="panel wide table"><h2>reviewers<span class="desc">maintainers' touches on the matching PRs (${D.maintainers.length} maintainers: group <b>${esc(D.maintainerGroup)}</b>${D.groups[D.maintainerGroup] ? '' : ', from github associations'})</span></h2>${table(RCOLS, reviewers, rk, 'asort')}</div>
    </div>`;
  bindTableSorts(view); bindRowClicks(view);
}

// ---- the services tab ----
function renderAreas(view) {
  const agg = (key, items) => {
    const m = new Map();
    for (const p of M) for (const k of items(p)) { if (!m.has(k)) m.set(k, []); m.get(k).push(p); }
    return [...m.entries()].map(([name, ps]) => {
      const open = ps.filter(p => p.s === 'open'), res = ps.filter(p => p.s !== 'open'), merged = ps.filter(p => p.s === 'merged');
      const top = arr => { const c = {}; for (const x of arr) c[x] = (c[x] || 0) + 1; return Object.entries(c).sort((a, b) => b[1] - a[1]).slice(0, 3).map(([k, v]) => `${k} (${v})`).join(', '); };
      return { name, n: ps.length, open: open.length, merged: merged.length, rate: res.length >= 3 ? merged.length / res.length : null, ttm: median(merged.map(p => p.td)),
        age: median(open.map(p => p.age)), fr: median(ps.filter(p => p.fr >= 0).map(p => p.fr)), effort: open.reduce((n, p) => n + p.ef, 0), inCourt: open.filter(p => p.ct === 'maintainer').length,
        authors: top(ps.map(p => p.a)), reviewers: top(ps.flatMap(p => p.rw)), never: open.filter(p => p.fr < 0).length };
    });
  };
  const cols = (key, label, attr) => [
    ['name', label, '', r => r.name, r => `<span class="clk" data-${attr}="${esc(r.name)}">${esc(r.name)}</span>`],
    ['n', 'PRs', 'num', r => r.n, r => fmtNum(r.n)],
    ['open', 'open', 'num', r => r.open, r => fmtNum(r.open)],
    ['inCourt', 'maint. court', 'num', r => r.inCourt, r => r.inCourt || '—'],
    ['never', 'unanswered', 'num', r => r.never, r => r.never || '—'],
    ['effort', 'open effort', 'num', r => r.effort, r => fmtNum(r.effort)],
    ['merged', 'merged', 'num', r => r.merged, r => fmtNum(r.merged)],
    ['rate', 'merge rate', 'num', r => r.rate, r => r.rate == null ? '—' : Math.round(r.rate * 100) + '%'],
    ['ttm', 'to merge', 'num', r => r.ttm, r => fmtDays(r.ttm)],
    ['age', 'open age', 'num', r => r.age, r => fmtDays(r.age)],
    ['fr', 'first resp', 'num', r => r.fr, r => fmtDays(r.fr)],
    ['authors', 'top authors', '', r => r.authors, r => esc(r.authors)],
    ['reviewers', 'top reviewers', '', r => r.reviewers, r => esc(r.reviewers)],
  ];
  const services = agg('sv', p => p.sv), kinds = agg('k', p => p.k), labels = agg('l', p => p.l);
  const SC = cols('sv', 'service', 'svc'), KC = cols('k', 'kind', 'kind'), LC = cols('l', 'label', 'label');
  const sk = sortable(services, S.asort, 'open', SC); sortable(kinds, S.asort, 'open', KC); sortable(labels, S.asort, 'open', LC);
  tabControls('');
  view.innerHTML = `<div class="panels">
    <div class="panel wide"><h2>open PRs by service<span class="desc">top 30 by open count</span></h2><div class="chart" id="c-svc"></div><div class="legend"><span><i class="box" style="--c:var(--s1)"></i>mostly in the maintainers' court</span><span><i class="box" style="--c:var(--s4)"></i>mostly in the authors' court</span></div></div>
    <div class="panel wide table"><h2>services<span class="desc">from internal/services/&lt;name&gt; in the changed files · click to filter</span></h2>${table(SC, services, sk, 'asort', 80)}</div>
    <div class="panel table"><h2>files changed<span class="desc">schema · code · tests · docs · examples · contributing · vendor · ci · changelog</span></h2>${table(KC.filter(c => !['authors', 'reviewers'].includes(c[0])), kinds, sk, 'asort')}</div>
    <div class="panel table"><h2>labels</h2>${table(LC.filter(c => !['authors', 'reviewers', 'ttm', 'fr'].includes(c[0])), labels, sk, 'asort', 40)}</div>
    </div>`;
  const top = services.slice().sort((a, b) => b.open - a.open).slice(0, 30);
  barChart($('#c-svc'), top.map(r => ({ label: r.name.length > 9 ? r.name.slice(0, 8) + '…' : r.name, value: r.open, color: r.inCourt / Math.max(r.open, 1) > 0.5 ? 'var(--s1)' : 'var(--s4)', title: `${r.name}: ${r.open} open, ${r.inCourt} in the maintainers' court, ${r.never} unanswered` })), { h: 200 });
  bindTableSorts(view); bindRowClicks(view);
}

// ---- the data tab: one month's closed and merged PRs, in the columns of the monthly sheet ----
// dates as the viewer's own clock has them, as a spreadsheet does: a PR opened late on the 29th here is the 29th, not utc's 30th
const localDate = u => { if (!u) return ''; const d = new Date(u * 1000), z = n => String(n).padStart(2, '0'); return `${d.getFullYear()}-${z(d.getMonth() + 1)}-${z(d.getDate())}`; };
// the month picked (mo=yyyy-mm), last month by default
const dataMonth = () => S.mo || (() => { const d = new Date(NOW * 1000); d.setDate(1); d.setMonth(d.getMonth() - 1); return localDate(d.getTime() / 1000).slice(0, 7); })();
// the year picked (yr=yyyy), this one by default, and the years there is data for
const dataYear = () => S.yr || String(new Date(NOW * 1000).getFullYear());
const dataYears = () => { const first = new Date(D.prs.reduce((m, p) => Math.min(m, p.c), NOW) * 1000).getFullYear(), last = new Date(NOW * 1000).getFullYear(); return Array.from({ length: last - first + 1 }, (_, i) => String(last - i)); };
// the data tab's period: a month (the default), a year, or the from/to dates — midnight to midnight on the viewer's
// clock, as [from, to) in seconds, with its name and a slug for file names
function dataRange() {
  const at = (y, m, d = 1) => new Date(y, m, d).getTime() / 1000;
  if (S.pd === 'year') { const y = Number(dataYear()); return { from: at(y, 0), to: at(y + 1, 0), label: String(y), slug: String(y) }; }
  if (S.pd === 'custom') {
    const d = v => v.split('-').map(Number), earliest = D.prs.reduce((m, p) => Math.min(m, p.c), NOW);
    const from = S.from ? at(d(S.from)[0], d(S.from)[1] - 1, d(S.from)[2]) : earliest, to = S.to ? at(d(S.to)[0], d(S.to)[1] - 1, d(S.to)[2] + 1) : NOW;
    return { from, to, label: `${S.from || localDate(earliest)} to ${S.to || 'now'}`, slug: `${S.from || localDate(earliest)}_${S.to || localDate(NOW)}` };
  }
  const [y, m] = dataMonth().split('-').map(Number);
  return { from: at(y, m - 1), to: at(y, m), label: new Date(y, m - 1, 1).toLocaleString('en', { month: 'long', year: 'numeric' }), slug: dataMonth() };
}
// the matching PRs closed or merged in the period, oldest number first
const monthRows = () => { const { from, to } = dataRange(); return M.filter(p => p.x && p.x >= from && p.x < to).sort((a, b) => a.n - b.n); };
// one name per review, in order, as the sheet lists them: the timeline's reviews, the author's own and bots' left out (the build's own test)
const isBot = w => { const l = (w || '').toLowerCase(); return !l || l.endsWith('[bot]') || l.endsWith('-bot') || l.startsWith('copilot') || ['hashibot', 'github-actions', 'dependabot'].includes(l); };
const reviewsBy = p => p.ev.filter(e => e.k === 'review' && e.w !== p.a && !isBot(e.w)).map(e => e.w);
// the same, shortened for the table: each reviewer once, with how many reviews (the copy and csv keep the full list)
const reviewsShort = p => { const n = new Map(); for (const w of reviewsBy(p)) n.set(w, (n.get(w) || 0) + 1); return [...n].map(([w, c]) => c > 1 ? `${w} ×${c}` : w).join(', '); };
// the GC sheet's columns, as ordinary columns any table can show: its own headers, local dates, the state in capitals,
// a reviewer per review. The blank one keeps a paste lined up with the sheet's empty column L
const GC_COLS = [
  ['gn', 'PR #', p => String(p.n), { cls: 'num' }], ['glink', 'Link', p => prURL(p.n), { html: p => `<a href="${prURL(p.n)}" target="_blank" rel="noopener" onclick="event.stopPropagation()">${prURL(p.n)}</a>` }],
  ['gopened', 'Date Opened', p => localDate(p.c)], ['gclosed', 'Date Closed/Merged', p => localDate(p.x)], ['gstate', 'State', p => p.s.toUpperCase()],
  ['guser', 'User', p => p.a], ['gmergedby', 'Merged By', p => p.mb || ''], ['gtitle', 'Title', p => p.t],
  ['greviews', 'Reviews', p => String(p.rv), { cls: 'num' }], ['greviewcomments', 'Review Comments', p => String(p.rc), { cls: 'num' }],
  ['gcomments', 'Discussion Comments', p => String(p.cm), { cls: 'num' }], ['gblank', '', () => ''],
  ['greviewers', 'Reviewers (by review)', p => reviewsBy(p).join(', '), { html: p => esc(reviewsShort(p)) }], ['glabels', 'Labels', p => p.l.join(', ')],
];
for (const [k, l, text, o = {}] of GC_COLS) {
  col(k, l, `GC sheet: ${l || 'an empty column'}`, o.html || (p => esc(text(p))), text, { cls: o.cls });
  EXPORT_VALUE[k] = text;
}
const GC_KEYS = GC_COLS.map(c => c[0]);
function renderData(view) {
  const range = dataRange(), rows = monthRows(), cols = visibleCols();
  tabControls(`${colPicker(cols)}<button class="plain" id="d-copy" title="tab-separated, the header row first: pastes into a google sheet as cells">copy for sheet</button><button class="plain" id="d-csv">csv</button><span class="note" id="d-note"></span>`);
  // the period at a glance, every number over the PRs the filter matches: what came in, what went out and how,
  // the reviewing done, and whether the queue grew. The period runs midnight to midnight on the viewer's clock
  const { from, to } = range;
  const inMonth = t => t >= from && t < to, end = Math.min(to, NOW);
  const opened = M.filter(p => inMonth(p.c)), stillOpen = opened.filter(p => p.s === 'open').length;
  const mergedPRs = rows.filter(p => p.s === 'merged'), closedPRs = rows.length - mergedPRs.length;
  const reviews = M.flatMap(p => p.ev.filter(e => e.k === 'review' && inMonth(e.t) && e.w !== p.a && !isBot(e.w)).map(e => ({ n: p.n, w: e.w })));
  const reviewedPRs = new Set(reviews.map(r => r.n)).size, maintReviews = reviews.filter(r => MAINT.has(r.w.toLowerCase())).length;
  const openAt = t => M.filter(p => p.c < t && (!p.x || p.x >= t)).length, startOpen = openAt(from), endOpen = openAt(end), moved = endOpen - startOpen;
  const tile = (v, k, d, cls = '') => `<div class="tile"><div class="v ${cls}">${v}</div><div class="k">${k}</div><div class="d">${d}</div></div>`;
  view.innerHTML = `<div class="tiles">
    ${tile(fmtNum(opened.length), `opened in ${range.label}`, `${fmtNum(stillOpen)} still open`)}
    ${tile(fmtNum(mergedPRs.length), 'merged', `median ${fmtDays(median(mergedPRs.map(p => (p.m - p.c) / DAY)))} from opened`, 'ok')}
    ${tile(fmtNum(closedPRs), 'closed without merging', `of ${fmtNum(rows.length)} closed or merged, the table below`)}
    ${tile(fmtNum(reviews.length), 'reviews', `on ${fmtNum(reviewedPRs)} PRs · ${fmtNum(maintReviews)} by maintainers`)}
    ${tile(`${moved > 0 ? '+' : ''}${fmtNum(moved)}`, 'open backlog', `${fmtNum(startOpen)} at the start → ${fmtNum(endOpen)} ${to > NOW ? 'now' : 'at the end'}`, moved > 0 ? 'bad' : moved < 0 ? 'ok' : '')}
    </div>
    <div class="panel wide table"><table><thead><tr>${cols.map(c => `<th class="${c.cls}" data-sort="${c.k}" draggable="true" title="${esc(c.d)}">${esc(c.l)}<span class="rm" title="remove the column">×</span></th>`).join('')}</tr></thead><tbody>
    ${rows.map(p => `<tr class="pr" data-n="${p.n}">${cols.map(c => `<td class="${c.cls}">${c.f(p)}</td>`).join('')}</tr>${S.open_n === String(p.n) ? `<tr class="detail"><td colspan="${cols.length}">${detail(p)}</td></tr>` : ''}`).join('') || `<tr><td colspan="${cols.length}" class="empty">nothing closed or merged in ${range.label} among the matching PRs</td></tr>`}
    </tbody></table></div>`;
  bindRowClicks(view);
  bindColumnPicker(cols);
  view.querySelectorAll('th .rm').forEach(x => x.addEventListener('click', e => { e.stopPropagation(); setCols(cols.map(c => c.k).filter(k => k !== x.parentElement.dataset.sort)); }));
  bindColumnDrag(view, cols);
  const table = sep => [cols.map(c => c.l), ...rows.map(p => cols.map(c => exportCell(c, p)))]
    .map(r => r.map(c => sep === ',' ? (/[",\n]/.test(c) ? '"' + c.replace(/"/g, '""') + '"' : c) : c.replace(/[\t\n]+/g, ' ')).join(sep)).join('\n') + '\n';
  $('#d-copy').addEventListener('click', () => (navigator.clipboard ? navigator.clipboard.writeText(table('\t')) : Promise.reject(new Error('no clipboard')))
    .then(() => { $('#d-note').textContent = `copied ${fmtNum(rows.length)} rows`; }, () => { $('#d-note').textContent = 'the browser would not copy'; }));
  $('#d-csv').addEventListener('click', () => {
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob(['﻿' + table(',')], { type: 'text/csv;charset=utf-8' }));
    a.download = `prs-${range.slug}.csv`; a.click(); URL.revokeObjectURL(a.href);
  });
}

// ---- the checks tab ----
// as the close report draws them: each check's question and evidence classes, then every candidate with its
// evidence in coloured, linked pieces and the AI's score. A check's heading folds it (cf=, the folded ones)
const evBit = b => b.u ? `<a class="${b.k || ''}" href="${esc(b.u)}" target="_blank" rel="noopener">${esc(b.t)}</a>` : `<span class="${b.k || ''}">${esc(b.t)}</span>`;
// waiting for response: open PRs where a reviewer asked the author for something — a review that is not an approval, or a
// comment, from a maintainer or anyone in a configured group — and the author has done nothing since, for over six months. The
// clock starts at the first such ask after the author last acted: a later maintainer comment or a merge of main does
// not restart it, and neither does the waiting-response label, which comes and goes. Worked out on the page
const labelWaiting = 'waiting-response', WAITING_CHECK = 'waiting for response', WAITING_DAYS = 182;
// who asks: the maintainers and everyone in a configured group (partners review too) — not a passer-by's "+1, any update?"
const ASKERS = new Set([...MAINT, ...Object.values(D.groups || {}).flat().map(l => l.toLowerCase())]);
const isAsk = (p, e) => (e.k === 'comment' || (e.k === 'review' && e.x !== 'approved')) && e.w && e.w !== p.a && !isBot(e.w) && ASKERS.has(e.w.toLowerCase());
const waitingAsk = p => { if (p.s !== 'open') return null; const since = p.lu || p.c; return p.ev.find(e => e.t > since && isAsk(p, e)) || null; };
const waitingDays = p => { const e = waitingAsk(p); return e ? (NOW - e.t) / DAY : null; };
function waitingItems() {
  return D.prs.filter(p => waitingDays(p) > WAITING_DAYS)
    .sort((a, b) => waitingDays(b) - waitingDays(a))
    .map(p => { const e = waitingAsk(p), asked = e.k === 'review' ? (e.x === 'changes_requested' ? 'requested changes' : 'reviewed') : 'commented';
      return { check: WAITING_CHECK, n: p.n,
        m: `opened ${fmtDays((NOW - p.c) / DAY)} ago · last activity ${fmtDays((NOW - p.la) / DAY)} ago · 💬 ${p.cm} · 👍 ${p.th}`,
        ev: [[{ t: `@${e.w}`, k: 'ver' }, { t: asked }, { t: `${fmtDate(e.t)}, ${fmtDays((NOW - e.t) / DAY)} ago`, k: 'bad' }, { t: '— nothing from the author since', k: 'dim' }],
          [{ t: 'the author last acted' }, { t: p.lu ? `${fmtDate(p.lu)}, ${fmtDays((NOW - p.lu) / DAY)} ago` : 'never since opening it', k: 'warn' },
            ...(p.l.includes(labelWaiting) ? [{ t: '· labelled', k: 'dim' }, { t: labelWaiting, k: 'mid' }] : [])]] }; });
}
const WAITING_HEAD = { check: WAITING_CHECK, q: 'a reviewer asked the author for something over six months ago and has heard nothing since — nudge, take over, or close?' };
// every candidate the tab lists, and each check's heading, in the order the tab draws them
const checkCandidates = () => [...(D.checks || []), ...waitingItems()];
const checkHeads = () => new Map([...(D.checkSections || []), WAITING_HEAD].map(h => [h.check, h]));
// the candidates as sections of PRs, folded checks left out when only those on show are wanted: what export writes
function checkSections(onlyShown) {
  const inM = new Set(M.map(p => p.n)), folded = new Set(S.cf ? S.cf.split(',') : []), by = new Map();
  for (const c of checkCandidates()) { if (!inM.has(c.n) || (onlyShown && folded.has(c.check))) continue; if (!by.has(c.check)) by.set(c.check, []); const p = BY_N.get(c.n); if (p && !by.get(c.check).includes(p)) by.get(c.check).push(p); }
  return [...by].map(([name, prs]) => ({ name: name === WAITING_CHECK ? name : 'close ' + name, desc: '', prs }));
}
function renderChecks(view) {
  tabControls(`<button class="plain" id="ck-open" title="open every candidate in a new tab — the folded checks left out">open</button><button class="plain" id="ck-export" title="the candidates as a csv, a copy for a spreadsheet, or markdown: pick the columns">export</button>`);
  $('#ck-export').addEventListener('click', openExport);
  $('#ck-open').addEventListener('click', openAll);
  const inM = new Set(M.map(p => p.n)), folded = new Set(S.cf ? S.cf.split(',') : []);
  const heads = checkHeads();
  const byCheck = new Map();
  for (const c of checkCandidates()) { if (!inM.has(c.n)) continue; if (!byCheck.has(c.check)) byCheck.set(c.check, []); byCheck.get(c.check).push(c); }
  view.innerHTML = `<p class="note">every close candidate the checks saw when this page was generated, restricted to the filter · click a check to fold it · regenerate with <code>prawn explore</code>${D.checks && D.checks.length ? '' : ` · ${esc(D.checksNote || 'the close checks found nothing')}`}</p>
    <div class="checks">${[...byCheck.entries()].map(([name, items]) => { const h = heads.get(name) || {}, shut = folded.has(name); return `<section class="check${shut ? ' shut' : ''}">
      <h2 data-check="${esc(name)}" title="${shut ? 'unfold' : 'fold'}"><span class="caret">${shut ? '▸' : '▾'}</span><span class="n">${name === WAITING_CHECK ? '' : 'close '}${esc(name)}</span> <span class="count">— ${fmtNum(items.length)} candidate${items.length === 1 ? '' : 's'}${h.total && h.total !== items.length ? ` of ${fmtNum(h.total)}` : ''}</span></h2>
      ${shut ? '' : `${h.q ? `<p class="q">${esc(h.q)}</p>` : ''}
      ${(h.classes || []).length ? `<p class="pills">${h.classes.map(c => `<span class="pill"><span class="${c.k || ''}">${esc(c.name)}</span> <span class="c">${fmtNum(c.n)}</span></span>`).join('')}</p>` : ''}
      ${h.cmd ? `<p class="cmd">act on these with <code>${esc(h.cmd)}</code></p>` : ''}
      <div class="items">${items.map(c => { const p = BY_N.get(c.n); return `<div class="item">
        <h3><a href="${prURL(c.n)}" target="_blank" rel="noopener">#${c.n}</a> ${esc(p ? p.t : '')} ${p ? `<span class="badge clk" data-author="${esc(p.a)}">${esc(p.a)}</span>` : ''}</h3>
        ${c.m ? `<div class="meta">${esc(c.m)}</div>` : ''}
        ${c.ev.map(line => `<div class="ev">${line.map(evBit).join(' ')}</div>`).join('')}
        ${c.score ? `<div class="ai"><span class="score ${c.sk || ''}">${esc(c.score)}</span> <span class="dim">—</span> ${esc(c.reason || '')}</div>` : ''}
      </div>`; }).join('')}</div>`}
    </section>`; }).join('')}
      ${!byCheck.size ? '<div class="empty">no candidates among the matching PRs</div>' : ''}</div>`;
  view.querySelectorAll('.check > h2').forEach(h => h.addEventListener('click', () => { const n = h.dataset.check; if (folded.has(n)) folded.delete(n); else folded.add(n); set('cf', [...folded].join(',')); }));
  bindRowClicks(view);
}

// ---- update ----
let filtersTab = null; // the tab the filter bar was drawn for
function update(rerenderFilters) {
  writeHash();
  hideTip();
  // the view controls live in the trends control row, which is about to be rebuilt: park them first
  const vc = $('#viewctl'); if (vc) { $('#viewhold').appendChild(vc); vc.hidden = true; }
  applyFilter();
  if (rerenderFilters || S.tab !== filtersTab) { filtersTab = S.tab; renderFilters(); }
  else syncFilterButtons();
  $('#f-n').textContent = fmtNum(M.length);
  if (S.sort === 'priority') S.sort = DEFAULTS.sort; // the old default sort, in bookmarked links and saved views
  S.tab = { queue: 'prs', areas: 'services' }[S.tab] || S.tab; // the tabs' old names, in bookmarked links and saved views
  renderTabs();
  const view = $('#view'); view.innerHTML = '';
  ({ prs: renderQueue, trends: renderTrends, suggested: renderSuggested, people: renderPeople, services: renderAreas, checks: renderChecks, data: renderData }[S.tab] || renderQueue)(view);
  // a view is the whole state but the tab — the filter every tab shares, the prs tab's columns and sort, the trends tab's layouts — so its controls show on every tab
  if (vc) { $('#tabctl').appendChild(vc); vc.hidden = false; }
  if ($('#viewSel').innerHTML) renderViews();
  $('#subtitle').textContent = `${fmtNum(D.prs.length)} PRs open at some point since ${D.since} · ${fmtNum(D.prs.filter(p => p.s === 'open').length)} open now · generated ${D.generated} by prawn ${D.version || 'dev'}`;
}
// ---- saved views: the whole url state under a name, in this browser; save to file and upload move them (tfpp's layouts) ----
const VIEW_KEY = 'prawn.views';
function loadViews() { try { return JSON.parse(localStorage.getItem(VIEW_KEY) || '{}') || {}; } catch (e) { return {}; } }
// views every page has: picked like a saved one, never deleted; saving under the same name keeps an edited copy instead
const BUILTIN_VIEWS = { 'GC sheet': { prawn: 'view', name: 'GC sheet', hash: 'tab=data&ddc=' + GC_KEYS.join(',') } };
const allViews = () => ({ ...BUILTIN_VIEWS, ...loadViews() });
function storeViews(vs) { try { localStorage.setItem(VIEW_KEY, JSON.stringify(vs)); } catch (e) { /* private window, full storage: the view just does not persist */ } }
let viewName = 'default';
// the tab is navigation, not part of a view: strip it before comparing
const viewHash = h => { const p = new URLSearchParams(h); p.delete('tab'); return p.toString(); };
function viewEdited() {
  writeHash();
  if (viewName === 'default') return viewHash(location.hash.slice(1)).length > 0;
  const v = allViews()[viewName]; return !v || viewHash(location.hash.slice(1)) !== viewHash(v.hash);
}
function renderViews(selected) {
  if (selected) viewName = selected;
  const vs = allViews(), sel = $('#viewSel'); if (!sel) return;
  if (!vs[viewName] && viewName !== 'default') viewName = 'default';
  const edited = viewEdited();
  const opt = n => `<option value="${esc(n)}">${esc(n)}${n === viewName && edited ? ' (edited)' : ''}</option>`;
  const saved = Object.keys(vs).filter(n => !(n in BUILTIN_VIEWS) || n in loadViews()).sort(), builtin = Object.keys(BUILTIN_VIEWS).filter(n => !saved.includes(n));
  sel.innerHTML = opt('default') + builtin.map(opt).join('') + saved.map(opt).join('');
  sel.value = viewName;
  $('#viewDel').disabled = viewName === 'default' || !(viewName in loadViews());
}
function currentView(name) { writeHash(); return { prawn: 'view', name, repo: D.repo, saved: new Date().toISOString(), hash: location.hash.slice(1) }; }
function applyView(v) { if (!v || typeof v.hash !== 'string') return; history.replaceState(null, '', '#' + v.hash); readHash(); update(true); }
function saveViewAs(name) { if (!name) return; const vs = loadViews(); vs[name] = currentView(name); storeViews(vs); renderViews(name); }
// importView takes a view object, its json (even inside prose or a code fence), or a bare #hash; unknown metric keys are dropped
function importView(text, fallbackName) {
  let v = null;
  if (typeof text === 'object') v = text;
  else {
    const t = String(text).trim(), m = t.match(/\{[\s\S]*\}/);
    if (m) { try { v = JSON.parse(m[0]); } catch (e) { throw new Error('not valid json: ' + e.message); } }
    else if (/^#?[a-z_]+=/.test(t) || t === '#') v = { prawn: 'view', hash: t.replace(/^#/, '') };
  }
  if (!v || v.prawn !== 'view' || typeof v.hash !== 'string') throw new Error('not a prawn view (need {"prawn":"view","hash":"..."} or a #hash)');
  const h = new URLSearchParams(v.hash);
  const raw = (h.get('m') || '').split(/[|,]/).map(k => k.replace(/^[swbt]+:/, '').replace(/^[!~]+/, '')).filter(Boolean);
  const unknown = raw.filter(k => !METRIC[k]);
  if (raw.length && unknown.length === raw.length) throw new Error('none of the metric keys exist: ' + unknown.slice(0, 5).join(', '));
  const name = (v.name || fallbackName || 'imported view').trim();
  applyView(v);
  const vs = loadViews(); vs[name] = { ...v, name, hash: location.hash.slice(1), saved: new Date().toISOString() }; storeViews(vs); renderViews(name);
  return { name, unknown };
}
function bindViews() {
  // save asks for the name, offering the selected view's (saving over it) or a dated one for a new view
  $('#viewSave').onclick = () => { $('#pastebox').hidden = true; $('#viewbox').hidden = false; const inp = $('#viewName'); inp.value = viewName === 'default' ? `view ${new Date().toISOString().slice(0, 10)}` : viewName; inp.focus(); inp.select(); };
  $('#viewCancel').onclick = () => { $('#viewbox').hidden = true; };
  const saveNamed = () => { const name = $('#viewName').value.trim(); if (!name) { $('#viewName').focus(); return ''; } saveViewAs(name); $('#viewbox').hidden = true; return name; };
  $('#viewOk').onclick = saveNamed;
  // save to file: kept in the list too, and downloaded as the json upload reads
  $('#viewFile').onclick = () => {
    const name = saveNamed(); if (!name) return;
    const v = currentView(name), a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([JSON.stringify(v, null, 2)], { type: 'application/json' }));
    a.download = `prawn-${name.replace(/[^a-z0-9._-]+/gi, '-').toLowerCase()}-${new Date().toISOString().slice(0, 10)}.json`;
    document.body.appendChild(a); a.click(); a.remove(); setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  };
  $('#viewName').addEventListener('keydown', ev => { if (ev.key === 'Enter') $('#viewOk').click(); if (ev.key === 'Escape') $('#viewCancel').click(); });
  $('#viewSel').onchange = () => {
    const n = $('#viewSel').value;
    if (n === 'default') { viewName = 'default'; history.replaceState(null, '', location.pathname); readHash(); update(true); return; }
    const v = allViews()[n]; if (v) { viewName = n; applyView(v); }
  };
  $('#viewDel').onclick = () => { if (viewName === 'default' || !(viewName in loadViews())) return; const vs = loadViews(); delete vs[viewName]; storeViews(vs); viewName = 'default'; renderViews(); };
  $('#viewUp').addEventListener('change', async ev => {
    const f = ev.target.files[0]; if (!f) return;
    try { importView(await f.text(), f.name.replace(/^prawn-/, '').replace(/-\d{4}-\d{2}-\d{2}\.json$/, '').replace(/\.json$/, '')); }
    catch (e) { $('#pastebox').hidden = false; $('#pasteErr').textContent = 'could not load: ' + e.message; }
    ev.target.value = '';
  });
  $('#pasteCancel').onclick = () => { $('#pastebox').hidden = true; };
  $('#pasteOk').onclick = () => { try { const { name, unknown } = importView($('#pasteText').value, 'pasted view'); if (unknown.length) { $('#pasteErr').textContent = `loaded "${name}", dropped ${unknown.length} unknown key${unknown.length > 1 ? 's' : ''}: ${unknown.slice(0, 4).join(', ')}`; } else $('#pastebox').hidden = true; } catch (e) { $('#pasteErr').textContent = e.message; } };
}

// ---- ask an AI to design a view: a prompt with the url grammar, the filter keys, the groups, and every metric ----
const AI_TARGETS = [
  // limit = characters of prompt a prefill url can safely carry; the copied prompt is never trimmed
  { id: 'claude', label: 'Claude', limit: 14000, url: q => 'https://claude.ai/new?q=' + encodeURIComponent(q) },
  { id: 'chatgpt', label: 'ChatGPT', limit: 6000, url: q => 'https://chatgpt.com/?q=' + encodeURIComponent(q) },
  { id: 'perplexity', label: 'Perplexity', limit: 6000, url: q => 'https://www.perplexity.ai/search?q=' + encodeURIComponent(q) },
];
const ASK_PLACEHOLDER = '<PUT YOUR ASK HERE>';
function viewPromptSections(ask, full) {
  writeHash();
  const open = D.prs.filter(p => p.s === 'open').length;
  const head = `${ask || ASK_PLACEHOLDER}

##########
Design a view of prawn explore for the ask above. Context: prawn explore is a page over every pull request of ${D.repo} that was open at any point since ${D.since} — ${fmtNum(D.prs.length)} PRs, ${fmtNum(open)} open now, generated ${D.generated}. A global filter picks a set of PRs; tabs show that set: prs (a sortable table), trends (metric panels over time), suggested (easy reviews), services, people (authors and reviewers), checks (close candidates), data (one month's closed and merged PRs, for a spreadsheet).
Answer with ONE json code block and nothing else, in exactly this form:
{"prawn":"view","name":"<short name for the view>","hash":"tab=trends&m=<panels>&gran=day"} — or, for a table, "tab=prs&q=<query>&sort=<column>&dc=<columns>"
The hash is a url query string. Filter keys (all optional): from=yyyy-mm-dd, to=yyyy-mm-dd (the period), st=open,merged,closed (states; omitted means open on most tabs and every state on trends), g=<group> (author group: ${[...GROUP_NAMES, 'community'].join(' | ')}), a=<login,login> (authors), svc=<service,service>, k=<kind,kind> (${KINDS.join(' | ')}), l=<label,label>, ct=maintainer|author (whose court an open PR is in), ef=<lo>-<hi> (review effort 1..5), dr=yes|no (only the drafts, or none of them; omitted shows them with the rest), q=<query> (a query language: key:value matches, key>n key<n compare, -key:value excludes; keys: author group svc kind label court state status effort age idle size files props prop rounds fr cd waiting reviewer reviewedby approvedby changesby responder lastmaint mergedby assoc approved decision mergeable ci ciage failingfor failing behind behindfor drift tests testsfailed testage testsince failedtest tested milestone draft thumbs comments reviews reviewcomments memberreviews memberreviewcomments membercomments approvals check ai n title, suggested (the suggested tab's categories: ${CATEGORIES.map(c => c[4]).join(' | ')}); e.g. "court:maintainer effort<3 idle>30").
PRs keys: tab=prs, sort=<column key> (omitted means u, the most recently updated first), dir=asc (reverses the sort), gb=suggested|kind|docs|service|court|status|ci|cifail|tests|group|effort|author (the table in sections), gc=<section|section> (the sections folded shut), sh=approved|docs|examples|contributing|tests|api|resource|datasource|prop1|prop2-4 (only that kind of PR: approved, provider docs only, examples only, contributing docs only, ci/test only, an api version upgrade, a new resource, a new data source, a single property changed, 2-4 properties changed), dc=<column key,column key,...> (the columns shown, in order; omitted means ${DEFAULT_COLS.join(',')}). Columns (key: label): ${Object.values(COL).map(c => `${c.k}: ${c.l}`).join(', ')}.
Trends keys: tab=trends, gran=day|week|month, cols=1|2|3, marks=major|minor|none (release markers).
- m: the panels, separated by |. Each panel is an optional flag prefix then a comma separated list of metric keys: "s:" stacks the series as areas (only for same-unit series that add up, like the review statuses or opened-by-group), "b:" draws bars, "sb:" stacked bars, "t:" shows the panel as a table, "w:" makes the panel span the grid; flags combine ("sbw:"). A key prefixed with ! goes on the right-hand axis, with ~ it is plotted but hidden until the user clicks its legend entry (useful for a dominant series that would flatten the others); a panel may mix at most two units and the second unit is put on the right automatically.
- Put closely related series together; 3 to 8 panels is a good view; order them from the most important down. The name should say what the view is about.
Only use metric keys from the list below; anything else is dropped. Groups configured: ${GROUP_NAMES.map(g => `${g} (${D.groups[g].length} logins)`).join(', ')}; maintainers are the ${D.maintainerGroup} group. Services (top 30 by open PRs): ${countBy(p => p.s === 'open' ? p.sv : []).slice(0, 30).map(([v, n]) => `${v} (${n})`).join(', ')}.`;
  const example = `The view currently on the page, as an example of the format:
${location.hash.slice(1) || 'tab=prs (the default: open PRs, no filter)'}`;
  const cat = ['Metrics (key | label | unit | what it measures):'];
  for (const area of AREAS) {
    const ms = METRICS.filter(m => m.area === area);
    if (full) { cat.push(`[${area}]`); for (const m of ms) cat.push(`${m.key} | ${m.label} | ${m.unit} | ${m.desc}`); }
    else cat.push(`[${area}] ` + ms.map(m => `${m.key} (${m.unit})`).join(', '));
  }
  return [head, example, cat.join('\n')].join('\n\n');
}
function viewPrompt(ask, limit) {
  const full = viewPromptSections(ask, true);
  if (!limit || full.length <= limit) return full;
  const compact = viewPromptSections(ask, false);
  return compact.length <= limit ? compact + '\n\n(metric descriptions were left out to fit the url; the copied prompt has them)' : compact.slice(0, limit) + '\n\n(truncated to fit the url; use copy prompt for everything)';
}
function renderAIBox() {
  $('#aiPrompt').value = viewPrompt($('#aiAsk').value.trim());
  $('#aiRow').innerHTML = AI_TARGETS.map(t => `<button class="plain" data-ai="${t.id}">${t.label}</button>`).join('') + `<button class="plain" data-ai="copy">copy prompt</button><button class="plain" data-ai="paste" title="the AI's answer: a view's json, or a #hash">paste answer</button><button class="plain" data-ai="close">close</button>`;
}
function bindAI() {
  $('#viewAI').onclick = () => { const b = $('#aibox'); $('#viewbox').hidden = true; $('#pastebox').hidden = true; b.hidden = !b.hidden; if (!b.hidden) { renderAIBox(); $('#aiAsk').focus(); } };
  $('#aiAsk').addEventListener('input', renderAIBox);
  $('#aiRow').addEventListener('click', ev => {
    const b = ev.target.closest('button[data-ai]'); if (!b) return;
    const ask = $('#aiAsk').value.trim();
    if (b.dataset.ai === 'close') { $('#aibox').hidden = true; return; }
    if (b.dataset.ai === 'paste') { $('#aibox').hidden = true; $('#pastebox').hidden = false; $('#pasteErr').textContent = ''; $('#pasteText').value = ''; $('#pasteText').focus(); return; }
    if (b.dataset.ai === 'copy') { navigator.clipboard?.writeText(viewPrompt(ask)); b.textContent = 'copied'; return; }
    const t = AI_TARGETS.find(x => x.id === b.dataset.ai);
    window.open(t.url(viewPrompt(ask, t.limit)), '_blank', 'noopener');
  });
  document.addEventListener('keydown', ev => { if (ev.key === 'Escape') { $('#aibox').hidden = true; $('#pastebox').hidden = true; $('#viewbox').hidden = true; } });
  document.addEventListener('click', ev => { if (!ev.target.closest('#aibox') && !ev.target.closest('#viewAI')) $('#aibox').hidden = true; });
}

// boot() runs once both scripts are loaded — see the end of explore-trends.js
function boot() {
  readHash();
  renderFilters();
  update();
  bindViews(); renderViews(); bindAI();
  $('#tl-close').addEventListener('click', () => $('#tlbox').close());
  $('#copy-link').addEventListener('click', () => { navigator.clipboard?.writeText(location.href); $('#copy-link').textContent = 'copied'; setTimeout(() => { $('#copy-link').textContent = 'copy link'; }, 1200); });
  // served by prawn explore --serve, the page can ask for a fresh build: refresh takes copy link's place (the url bar
  // has the link). Opened from disk there is nobody to ask, and copy link stays.
  if (location.protocol.startsWith('http')) {
    const btn = $('#refresh'), box = $('#refreshbox'), pre = $('#rf-log');
    const api = from => { const u = new URL('refresh', location.href.split('#')[0]); if (from != null) u.searchParams.set('from', from); return u; };
    // the button is refresh, then refreshing… while one runs (a click then opens the status window), then the page
    // reloads — or, with the window open, waits to be told to. state: idle | busy | failed | done
    let state = 'idle', logAt = 0, timer = null, mayRefresh = true;
    const took = st => { const a = Date.parse(st.started), b = st.finished ? Date.parse(st.finished) : Date.now(); const sec = Math.max(0, Math.round((b - a) / 1000)); return sec < 90 ? `${sec}s` : `${Math.floor(sec / 60)}m ${sec % 60}s`; };
    const paint = st => {
      btn.textContent = { idle: 'refresh', busy: 'refreshing…', failed: 'refresh failed', done: 'refreshed' }[state];
      btn.disabled = state === 'idle' && !mayRefresh; // anyone may watch one run; only an admin may start one
      btn.title = { idle: mayRefresh ? 'fetch what moved on github since this page was built, rebuild it, and reload' : 'only an admin can refresh (PRAWN_ADMINS)', busy: 'fetching and rebuilding — click to watch it', failed: 'the refresh failed — click for what it printed', done: 'the page is rebuilt — click to see what it printed' }[state];
      if (!st) return;
      const who = st.by && st.by !== '-' ? ` · started by ${st.by}` : '';
      $('#rf-head').textContent = { idle: 'refresh', busy: `refreshing · ${took(st)}`, failed: 'the refresh failed', done: `refreshed in ${took(st)}` }[state];
      const doing = st.what === 'upload' ? 'swapping in the uploaded database and rebuilding the page' : 'syncing with github and rebuilding the page';
      $('#rf-desc').textContent = state === 'busy' ? `${doing}${who} — the page reloads when it is done` : state === 'failed' ? (st.error || '') : state === 'done' ? `the page is rebuilt${who} — reload to see it` : '';
      $('#rf-again').hidden = state !== 'failed'; $('#rf-reload').hidden = state !== 'done';
    };
    // what the refresh printed since the last look, appended; a new run starts the log over
    const addLog = st => {
      if (st.logEnd == null) return;
      if (st.logEnd < logAt) { pre.textContent = ''; }
      const atEnd = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 30;
      if (st.log) pre.append(st.log);
      logAt = st.logEnd;
      if (atEnd) pre.scrollTop = pre.scrollHeight;
    };
    // one loop while a refresh runs: every second and with the log while the window is open, every few and bare otherwise
    const watch = () => {
      clearTimeout(timer);
      fetch(api(box.open ? logAt : null)).then(r => r.json()).then(st => {
        if (box.open) addLog(st);
        if (st.running) { state = 'busy'; paint(st); timer = setTimeout(watch, box.open ? 1000 : 3000); return; }
        if (st.error) { state = 'failed'; paint(st); return; }
        if (state !== 'busy') { paint(st); return; } // nothing was running: a look at the last run's log, not a refresh ending
        if (!box.open) { location.reload(); return; }
        state = 'done'; paint(st);
      }).catch(() => { state = 'idle'; paint(); });
    };
    const start = () => { state = 'busy'; paint(); fetch(api(), { method: 'POST', headers: { 'X-Prawn': '1' } }).then(() => { timer = setTimeout(watch, 500); }).catch(() => { state = 'idle'; paint(); }); };
    const openBox = () => { pre.textContent = ''; logAt = 0; box.showModal(); watch(); };
    fetch(api()).then(r => r.ok ? r.json() : Promise.reject(new Error('no refresh here'))).then(st => {
      btn.hidden = false; $('#copy-link').hidden = true; mayRefresh = st.admin !== false; paint();
      btn.addEventListener('click', () => { if (state !== 'idle') openBox(); else if (mayRefresh) start(); });
      // rebuilt while it was watched: closing the window is the reload. Done here and on escape, not on the dialog's
      // close event, which a browser only delivers once the tab next paints
      const shut = () => { if (box.open) box.close(); if (state === 'done') location.reload(); };
      $('#rf-close').addEventListener('click', shut);
      box.addEventListener('cancel', e => { e.preventDefault(); shut(); });
      $('#rf-reload').addEventListener('click', () => location.reload());
      $('#rf-again').addEventListener('click', () => { pre.textContent = ''; logAt = 0; start(); });
      if (st.running) { state = 'busy'; paint(st); timer = setTimeout(watch, 3000); } // someone else's refresh, already under way
    }).catch(() => {}); // a plain file server, or an older prawn: no button
    // db, beside refresh: the server's database down to this machine, or one from this machine up in its place — how
    // a database fetched somewhere else gets into a server without walking the whole repo again
    const dbBox = $('#dbbox'), dbUrl = q => new URL('db' + (q || ''), location.href.split('#')[0]);
    const mb = n => n >= 1 << 30 ? `${(n / (1 << 30)).toFixed(1)} GB` : `${Math.max(1, Math.round(n / (1 << 20)))} MB`;
    let picked = null, sending = false, mayUpload = true;
    const dbNote = t => { $('#db-note').textContent = t; };
    // two faces: download / upload, or — a file picked — the question before it replaces anything
    const dbAsk = file => {
      picked = file;
      $('#db-ok').hidden = !file; $('#db-down').hidden = !!file; $('#db-up').hidden = !!file || !mayUpload;
      dbNote(file ? `replace the server's database with ${file.name} (${mb(file.size)})? the one there now is kept beside it` : '');
    };
    fetch(dbUrl('?info')).then(r => r.ok ? r.json() : Promise.reject(new Error('no db here'))).then(info => {
      $('#db').hidden = false;
      mayUpload = info.admin !== false; // only an admin may put a database in; anyone may take a copy
      $('#db').addEventListener('click', () => {
        dbAsk(null); $('#db-desc').textContent = '';
        dbBox.showModal();
        fetch(dbUrl('?info')).then(r => r.json()).then(i => { $('#db-desc').textContent = i.size ? `${i.name} · ${mb(i.size)} · last written ${new Date(i.modified).toLocaleString()}` : `${i.name} · not there yet`; $('#db-down').disabled = !i.size; }).catch(() => {});
      });
      $('#db-close').addEventListener('click', () => { if (!sending) dbBox.close(); });
      dbBox.addEventListener('cancel', e => { if (sending) e.preventDefault(); });
      $('#db-down').addEventListener('click', () => {
        const a = document.createElement('a'); a.href = dbUrl(); a.download = ''; document.body.append(a); a.click(); a.remove();
        dbNote('copying the database — the download starts in a few seconds');
      });
      $('#db-up').addEventListener('click', () => { $('#db-file').value = ''; $('#db-file').click(); });
      $('#db-file').addEventListener('change', e => { if (e.target.files[0]) dbAsk(e.target.files[0]); });
      $('#db-ok').addEventListener('click', () => {
        if (!picked || sending) return;
        const x = new XMLHttpRequest(), done = msg => { sending = false; $('#db-ok').disabled = false; dbAsk(null); dbNote(msg); };
        sending = true; $('#db-ok').disabled = true; dbNote('uploading…');
        x.open('POST', dbUrl()); x.setRequestHeader('X-Prawn', '1');
        x.upload.onprogress = e => { if (e.lengthComputable) dbNote(`uploading… ${Math.round(100 * e.loaded / e.total)}%`); };
        x.upload.onload = () => dbNote('uploaded — the server is checking it…');
        x.onerror = () => done('the upload did not get through');
        x.onload = () => {
          if (x.status !== 200) { done(x.responseText.trim() || `the server answered ${x.status}`); return; }
          // it is going in: from here it is a refresh like any other, watched in the same window
          done(''); dbBox.close(); state = 'busy'; paint(); openBox();
        };
        x.send(picked);
      });
    }).catch(() => {}); // an older prawn: no button
  }
  window.addEventListener('hashchange', () => { readHash(); update(true); });
  let rt; window.addEventListener('resize', () => { clearTimeout(rt); rt = setTimeout(() => { if (S.tab === 'trends' || S.tab === 'services') update(); }, 150); });
}
