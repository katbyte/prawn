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
const STATE_COLORS = ['var(--other)', 'var(--s1)', 'var(--s4)', 'var(--s3)', 'var(--s8)'];
const STATE_DESC = ['draft — not up for review yet', 'awaiting — the maintainers\' court', 'waiting — the author\'s court (waiting-response, or changes requested and nothing pushed)', 'approved — waiting on a merge', 'blocked — the Blocked milestone'];
const PALETTE = ['var(--s1)', 'var(--s2)', 'var(--s3)', 'var(--s4)', 'var(--s5)', 'var(--s7)', 'var(--s6)', 'var(--s8)'];
const KINDS = ['schema', 'code', 'tests', 'docs', 'vendor', 'ci', 'changelog', 'other'];

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
const DEFAULTS = { tab: 'data', from: D.viewFrom || D.since, to: '', st: null, g: '', a: '', svc: '', k: '', l: '', ct: '', ef: '', q: '', sort: 'priority', dir: '', dc: '', sg: '', gran: 'day', by: '', psort: '', asort: '', marks: 'major', open_n: '', m: '', cols: '2', zero: '1', dots: 'auto', tv: 'charts', ca: '', cb: '' };
const S = { ...DEFAULTS };
// the state filter's default depends on the tab: open PRs everywhere, every state on trends — until it is set explicitly
const stateFilter = () => S.st ?? (S.tab === 'trends' ? '' : 'open');
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
  kind: p => p.k, k: p => p.k, only: p => p.k.length === 1 ? p.k[0] : 'mixed', label: p => p.l, l: p => p.l, court: p => p.ct, c: p => p.ct, state: p => p.s, st: p => p.s,
  effort: p => p.ef, e: p => p.ef, age: p => p.age, idle: p => p.idle, size: p => p.size, files: p => p.f, rounds: p => p.rr,
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
    <label>from<input type="date" id="f-from" value="${esc(S.from)}"></label>
    <label>to<input type="date" id="f-to" value="${esc(S.to)}"></label>
    <label>state${segMulti('f-st', ['open', 'merged', 'closed'], stateFilter(), STATE_MC)}</label>
    <label>group<select id="f-g">${opt('', 'any', S.g)}${GROUP_NAMES.map(g => opt(g, g, S.g)).join('')}${opt('community', 'community', S.g)}</select></label>
    <div class="fl">author${picker('f-a', 'a', countBy(p => [p.a]), 'any author')}</div>
    <div class="fl">service${picker('f-svc', 'svc', countBy(p => p.sv), 'any service')}</div>
    <div class="fl">kind${picker('f-k', 'k', KINDS.map(k => [k, D.prs.filter(p => p.k.length && p.k.every(x => x === k)).length]), 'any kind', 'PRs made of only the picked kinds, from their changed files: schema (resource/data source .go), code (other .go), tests (_test.go), docs (website/), vendor (vendor/, go.mod), ci (.github/, scripts/), changelog; the counts are PRs of exactly that one kind')}</div>
    <div class="fl">label${picker('f-l', 'l', countBy(p => p.l), 'any label')}</div>
    <label>court<select id="f-ct">${opt('', 'any', S.ct)}${opt('maintainer', 'maintainer', S.ct)}${opt('author', 'author', S.ct)}</select></label>
    <label>effort<select id="f-ef">${opt('', 'any', S.ef)}${['1-1', '1-2', '1-3', '2-5', '3-5', '4-5', '5-5'].map(r => opt(r, r.replace('-', '–'), S.ef)).join('')}</select></label>
    <label>query<input class="wide" id="f-q" value="${esc(S.q)}" placeholder="author:x label:bug age>90 -kind:docs …  (enter)"></label>
    <span class="match"><span><b id="f-n"></b> of ${fmtNum(D.prs.length)} PRs</span> <button class="plain" id="f-clear">clear</button></span>
    `;
  const bind = (id, key, ev = 'change') => $(id).addEventListener(ev, e => set(key, e.target.value));
  bind('#f-from', 'from'); bind('#f-to', 'to'); bind('#f-g', 'g'); bind('#f-ct', 'ct'); bind('#f-ef', 'ef'); bind('#f-q', 'q');
  bindPickers();
  $('#f-q').addEventListener('keydown', e => { if (e.key === 'Enter') set('q', e.target.value); });
  const togglePills = (id, key) => $(id).addEventListener('click', e => {
    const b = e.target.closest('button'); if (!b) return; const v = b.dataset.v;
    const val = key === 'st' ? stateFilter() : S[key];
    const cur = val ? val.split(',') : [];
    set(key, (cur.includes(v) ? cur.filter(x => x !== v) : [...cur, v]).join(','));
  });
  togglePills('#f-st', 'st');
  $('#f-clear').addEventListener('click', () => { for (const k of ['from', 'to', 'g', 'a', 'svc', 'k', 'l', 'ct', 'ef', 'q']) S[k] = DEFAULTS[k]; S.st = ''; update(true); });
  $('#qhelp').innerHTML = `<details><summary>query keys</summary> — <code>key:value</code> matches (prefix for text, any of <code>a,b</code>), <code>key&gt;n</code> <code>key&lt;n</code> compare, <code>-key:value</code> excludes, bare words search title and author.
    keys: <code>author group svc kind only label court state status effort age idle size files rounds fr cd waiting reviewer reviewedby approvedby changesby responder lastmaint mergedby assoc approved decision mergeable ci milestone draft thumbs comments reviews reviewcomments memberreviews memberreviewcomments membercomments approvals check ai suggested n title</code> (<code>kind:docs</code> touches docs, <code>only:docs</code> is docs and nothing else; <code>approved</code> is a maintainer's, <code>decision</code> is github's; <code>status</code> is merged, closed, or an open PR's state today; <code>ci</code> is passing, failing, running, none).
    e.g. <code>court:maintainer effort&lt;3 idle&gt;30</code> · <code>label:waiting-response cd&gt;60</code> · <code>fr:none state:open</code> · <code>reviewer:katbyte rounds&gt;2</code> · <code>approvedby:katbyte ci:passing</code> · <code>check:stale ai&gt;0.8</code> · <code>suggested:fixes</code> (${CATEGORIES.map(c => c[4]).join(', ')})</details>`;
}

// the toggle buttons and pickers reflect the state after every change, without rebuilding the bar (which would drop focus)
function syncFilterButtons() {
  for (const [id, key] of [['#f-st', 'st']]) {
    const val = key === 'st' ? stateFilter() : S[key];
    const on = val ? val.split(',') : [];
    document.querySelectorAll(id + ' button').forEach(b => b.classList.toggle('on', on.includes(b.dataset.v)));
  }
  document.querySelectorAll('.picker').forEach(syncPicker);
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
  document.querySelectorAll('.picker').forEach(el => {
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
const TABS = [['data', 'data'], ['trends', 'trends'], ['suggested', 'suggested'], ['areas', 'areas'], ['people', 'people'], ['checks', 'checks']];
function renderTabs() {
  const counts = { data: M.length, trends: M.length, suggested: suggestions().reduce((n, c) => n + c.prs.length, 0), people: new Set(M.map(p => p.a)).size, areas: new Set(M.flatMap(p => p.sv)).size, checks: M.reduce((n, p) => n + p.checks.length, 0) };
  const c = $('#controls');
  c.innerHTML = `<span class="seg" id="tabs">${TABS.map(([id, label]) => `<button data-tab="${id}" class="${S.tab === id ? 'on' : ''}">${label}<span class="n">${fmtNum(counts[id])}</span></button>`).join('')}</span><span id="tabctl" style="display:contents"></span>`;
  $('#tabs').addEventListener('click', e => { const b = e.target.closest('button'); if (b) set('tab', b.dataset.tab); });
}
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

// ---- the data tab's columns: every field collected on a PR, each one renderable, sortable, and pickable ----
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
col('size', '±', 'lines added and deleted', p => `+${fmtNum(p.ad)}/−${fmtNum(p.de)}`, p => p.size, { cls: 'num', hi: true });
col('f', 'files', 'files changed', p => fmtNum(p.f), p => p.f, { cls: 'num', hi: true });
col('k', 'kinds', 'the kinds of change, from the changed files', p => p.k.map(k => `<span class="badge clk" data-kind="${k}">${k}</span>`).join(''), p => p.k[0] || LAST);
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
col('ci', 'ci', "the head commit's checks: passing, failing, running", p => p.ci ? `<span class="${{ passing: 'ok', failing: 'bad', running: 'mid' }[p.ci]}">${p.ci}</span>` : '', p => p.ci || LAST);
col('mb', 'merged by', 'who merged it', p => p.mb ? `<span class="clk" data-q="mergedby:${esc(p.mb)}">${esc(p.mb)}</span>` : '', p => p.mb || LAST);
col('c', 'created', 'when it opened', p => fmtDate(p.c), p => p.c, { cls: 'num', hi: true });
col('x', 'closed', 'when it closed', p => p.x ? fmtDate(p.x) : '', p => p.x || 0, { cls: 'num', hi: true });
col('m', 'merged', 'when it merged', p => p.m ? fmtDate(p.m) : '', p => p.m || 0, { cls: 'num', hi: true });
col('li', 'closes', 'the issues its closing keywords reference', p => p.li.map(n => `<a href="https://github.com/${D.repo}/issues/${n}" target="_blank" rel="noopener" onclick="event.stopPropagation()">#${n}</a>`).join(' '), p => p.li.length, { hi: true });
col('aiMax', 'ai', 'the highest AI close score', p => p.aiMax == null ? '' : `<span title="${esc(Object.entries(p.ai).map(([k, v]) => k + ' ' + v.toFixed(2)).join(', '))}">${p.aiMax.toFixed(2)}</span>`, p => p.aiMax, { cls: 'num', hi: true });
col('checks', 'checks', 'the close checks that flagged it', p => p.checks.map(c => `<span class="badge">${esc(c.check)}${c.score ? ' ' + c.score : ''}</span>`).join(''), p => p.checks.length, { hi: true });
const DEFAULT_COLS = ['n', 't', 'a', 's', 'ct', 'age', 'idle', 'ef', 'size', 'sv', 'l', 'rr', 'fr', 'aiMax'];
// the columns shown, in order: the dc hash key, or the default; unknown keys (an older view) are dropped
const visibleCols = () => { const ks = (S.dc ? S.dc.split(',') : DEFAULT_COLS).filter(k => COL[k]); return (ks.length ? ks : DEFAULT_COLS).map(k => COL[k]); };
const setCols = keys => set('dc', keys.join(',') === DEFAULT_COLS.join(',') ? '' : keys.join(','));
const SORT_PRIORITY = (a, b) => (a.ct === 'maintainer' ? 0 : 1) - (b.ct === 'maintainer' ? 0 : 1) || a.ef - b.ef || (b.cd || 0) - (a.cd || 0);
// a column's comparator: its own when it has one, else by its sort value with empties last, ties broken by priority
function colSorter(k) {
  const c = COL[k]; if (k === 'priority' || !c) return SORT_PRIORITY;
  if (c.s) return c.s;
  const cmp = (x, y) => {
    if (x == null && y == null) return 0; if (x == null) return 1; if (y == null) return -1;
    return typeof x === 'number' ? x - y : String(x).localeCompare(String(y));
  };
  return (a, b) => { const x = c.v(a), y = c.v(b); const r = x == null || y == null ? cmp(x, y) : c.hi ? cmp(y, x) : cmp(x, y); return r || SORT_PRIORITY(a, b); };
}
const VIEWS = [
  ['needs first look', 'state:open fr:none -draft:yes'],
  ['approved, unmerged', 'state:open approved:yes'],
  ['pushed after changes', 'state:open court:maintainer rounds>0'],
  ['waiting > 30d', 'state:open label:waiting-response cd>30'],
  ['small & idle', 'state:open effort<3 idle>60'],
  ['conflicted', 'state:open mergeable:conflicting'],
  ['big & old', 'state:open effort>3 age>180'],
  ['maintainer court > 14d', 'state:open court:maintainer cd>14'],
];
let queueLimit = 200;
function renderQueue(view) {
  const rows = M.slice();
  rows.sort(colSorter(S.sort)); if (S.dir === 'asc') rows.reverse();
  const cols = visibleCols();
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
      ${rows.slice(0, queueLimit).map(p => `<tr class="pr" data-n="${p.n}">${cols.map(c => `<td class="${c.cls}">${c.f(p)}</td>`).join('')}</tr>${S.open_n === String(p.n) ? `<tr class="detail"><td colspan="${cols.length}">${detail(p)}</td></tr>` : ''}`).join('')}
      ${rows.length > queueLimit ? `<tr><td colspan="${cols.length}" class="more">showing ${queueLimit} of ${rows.length} — <a id="q-more">show 200 more</a></td></tr>` : ''}
      ${!rows.length ? `<tr><td colspan="${cols.length}" class="empty">nothing matches</td></tr>` : ''}
    </tbody></table></div>`;
  const sortOpts = [['priority', 'priority: maintainer court · cheapest · longest'], ...cols.map(c => [c.k, c.l])];
  if (!sortOpts.some(([k]) => k === S.sort) && COL[S.sort]) sortOpts.push([S.sort, COL[S.sort].l]); // sorted by a hidden column
  const shown = new Set(cols.map(c => c.k));
  tabControls(`<span>sort <select id="q-sort">${sortOpts.map(([v, l]) => `<option value="${v}"${S.sort === v ? ' selected' : ''}>${l}</option>`).join('')}</select></span>
    <span class="picker" id="colpick"><button class="plain pick on" type="button">columns <span class="dim">${cols.length}</span></button>
      <div class="pop" hidden><input type="search" placeholder="filter…"><div class="opt reset"><span class="sw all"></span><span class="name">reset to the default columns</span></div><div class="rows">${Object.values(COL).map(c => `<div class="opt${shown.has(c.k) ? ' on' : ''}" data-v="${c.k}" data-name="${esc(c.l.toLowerCase())}" title="${esc(c.d)}"><span class="sw"></span><span class="name">${esc(c.l)}</span><span class="desc">${esc(c.d)}</span></div>`).join('')}</div></div></span>
    <span class="views">views ${VIEWS.map(([l, q]) => `<button class="pillbtn${S.q === q ? ' on' : ''}" data-q="${esc(q)}">${l}</button>`).join('')}</span>`);
  $('#q-sort').addEventListener('change', e => { S.dir = ''; set('sort', e.target.value); });
  document.querySelectorAll('#tabctl .views .pillbtn').forEach(el => el.addEventListener('click', () => set('q', S.q === el.dataset.q ? '' : el.dataset.q)));
  bindColumnPicker(cols);
  view.querySelectorAll('th[data-sort]').forEach(th => th.addEventListener('click', e => { if (e.target.closest('.rm')) return; const k = th.dataset.sort; if (S.sort === k) S.dir = S.dir === 'asc' ? '' : 'asc'; else S.dir = ''; set('sort', k); }));
  view.querySelectorAll('th .rm').forEach(x => x.addEventListener('click', e => { e.stopPropagation(); setCols(cols.map(c => c.k).filter(k => k !== x.parentElement.dataset.sort)); }));
  bindColumnDrag(view, cols);
  const more = $('#q-more'); if (more) more.addEventListener('click', () => { queueLimit += 200; renderQueue(view); });
  bindRowClicks(view);
}
// the columns picker: a popover of every column, checked when shown; a click adds it at the end or removes it
function bindColumnPicker(cols) {
  const el = $('#colpick'), pop = el.querySelector('.pop'), search = pop.querySelector('input');
  el.querySelector('button.pick').addEventListener('click', () => { const open = pop.hidden; document.querySelectorAll('.picker .pop').forEach(p => { p.hidden = true; }); pop.hidden = !open; if (open) { search.value = ''; search.dispatchEvent(new Event('input')); search.focus(); } });
  search.addEventListener('input', () => { const f = search.value.toLowerCase(); pop.querySelectorAll('.rows .opt').forEach(l => { l.hidden = !!f && !l.dataset.name.includes(f) && !l.dataset.v.toLowerCase().includes(f); }); });
  pop.addEventListener('click', e => {
    const l = e.target.closest('.opt'); if (!l) return; e.preventDefault();
    if (l.classList.contains('reset')) { set('dc', ''); return; }
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
    const clk = e.target.closest('.clk');
    if (clk) { e.stopPropagation(); if (clk.dataset.author) set('a', clk.dataset.author); else if (clk.dataset.svc) set('svc', clk.dataset.svc); else if (clk.dataset.label) set('l', clk.dataset.label); else if (clk.dataset.reviewer) set('q', 'reviewer:' + clk.dataset.reviewer); else if (clk.dataset.group) set('g', clk.dataset.group); else if (clk.dataset.kind) set('k', clk.dataset.kind); else if (clk.dataset.q != null) { S.q = clk.dataset.q; set('tab', 'data'); } return; }
    const tr = e.target.closest('tr.pr'); if (!tr) return;
    set('open_n', S.open_n === tr.dataset.n ? '' : tr.dataset.n);
  });
}
// a PR's detail: its life as a bar of states, the numbers, and the timeline
function detail(p) {
  const start = p.c, end = p.x || NOW, span = Math.max(end - start, 1);
  const life = p.iv.map(iv => `<span style="width:${(((iv.e || NOW) - iv.s) / span * 100).toFixed(2)}%;background:${STATE_COLORS[iv.st]}" title="${STATE_NAMES[iv.st]} ${fmtDate(iv.s)} → ${iv.e ? fmtDate(iv.e) : 'now'} (${fmtDays(((iv.e || NOW) - iv.s) / DAY)})"></span>`).join('');
  const evs = p.ev.slice().reverse();
  const evLine = e => { const m = MAINT.has((e.w || '').toLowerCase()) && e.w.toLowerCase() !== p.lo; return `<div class="${m ? 'maint' : ''}"><span class="t">${fmtDate(e.t)}</span><span class="w">${esc(e.w || '')}</span> ${esc(e.k)}${e.x ? ' <span class="dim">' + esc(e.x.length > 60 ? e.x.slice(0, 59) + '…' : e.x) + '</span>' : ''}</div>`; };
  return `<div class="detail">
    <div class="row"><b>life</b>${fmtDate(p.c)} → ${p.x ? fmtDate(p.x) : 'now'} · ${fmtDays((end - start) / DAY)} ${p.s} ${p.mb ? '· merged by ' + esc(p.mb) : ''} ${p.ms ? '· milestone ' + esc(p.ms) : ''}</div>
    <div class="life">${life}</div>
    <div class="legend">${STATE_NAMES.map((s, i) => `<span title="${STATE_DESC[i]}"><i class="box" style="--c:${STATE_COLORS[i]}"></i>${s}</span>`).join('')}</div>
    <div class="row"><b>review</b>${p.rv} reviews · ${p.rr} changes-requested rounds · ${p.ap} approvals · first maintainer response ${p.fr < 0 ? 'none' : fmtDays(p.fr) + ' (' + esc(p.fw) + ')'} · waiting-response ${fmtDays(p.wd)} over ${p.wc} cycle${p.wc === 1 ? '' : 's'} · decision ${p.rd || 'none'} · ${p.mg || ''}</div>
    <div class="row"><b>roll</b>reviewed by ${p.rb.length ? logins(p.rb, 'reviewedby', 6) : 'nobody'} · approved by ${p.ab.length ? logins(p.ab, 'approvedby', 6) : 'nobody'} · changes requested by ${p.cb.length ? p.cb.map(c => `<span class="clk" data-q="changesby:${esc(c.l)}">${esc(c.l)}</span><span class="dim">(×${c.n} ✎${c.c})</span>`).join(', ') : 'nobody'} · ${p.rc} review comment${p.rc === 1 ? '' : 's'}${p.ci ? ` · ci <span class="${{ passing: 'ok', failing: 'bad', running: 'mid' }[p.ci]}">${p.ci}</span>` : ''}</div>
    <div class="row"><b>people</b>author <span class="clk" data-author="${esc(p.a)}">${esc(p.a)}</span> (${esc(p.as.toLowerCase().replace('_', ' '))}, ${esc(p.g)}) · reviewers ${p.rw.length ? p.rw.map(r => `<span class="clk" data-reviewer="${esc(r)}">${esc(r)}</span>`).join(', ') : 'none'} ${p.lastMaintBy ? '· last maintainer touch ' + esc(p.lastMaintBy) : ''}</div>
    <div class="row"><b>change</b>+${fmtNum(p.ad)}/−${fmtNum(p.de)} over ${p.f} files · effort ${p.ef}/5 · ${p.k.map(k => `<span class="badge clk" data-kind="${k}">${k}</span>`).join('')} ${p.sv.map(s => `<span class="badge clk" data-svc="${esc(s)}">${esc(s)}</span>`).join('')}</div>
    <div class="row"><b>labels</b>${p.l.length ? p.l.map(l => `<span class="badge clk" data-label="${esc(l)}">${esc(l)}</span>`).join('') : 'none'} ${p.li && p.li.length ? '· closes ' + p.li.map(n => `<a href="https://github.com/${D.repo}/issues/${n}" target="_blank" rel="noopener">#${n}</a>`).join(' ') : ''} · 👍 ${p.th} · 💬 ${p.cm}</div>
    ${p.checks.length ? `<div class="row"><b>checks</b>${p.checks.map(c => `<span class="badge">${esc(c.check)}${c.score ? ' ' + c.score : ''}</span> ${c.ev.map(esc).join(' · ')}`).join('<br>')}</div>` : ''}
    <div class="row"><b>timeline</b>${p.ev.length} events</div>
    <div class="events">${evs.slice(0, 40).map(evLine).join('')}${evs.length > 40 ? `<div>… ${evs.length - 40} earlier</div>` : ''}</div>
  </div>`;
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
// each category: name, description, the test, the why-line, and a slug the data tab's query filters by (suggested:<slug>)
const CATEGORIES = [
  ['approved, unmerged', `a maintainer's approval (the ${D.maintainerGroup} group's, not any collaborator's) is on it and nothing has been requested since — merge, or say what is missing`, p => p.approved, p => `approved by ${p.rw.filter(r => MAINT.has(r)).slice(0, 2).join(', ') || 'a maintainer'} · idle ${fmtDays(p.idle)}`, 'approved'],
  ['docs and changelog only', 'no code changes: a read-through', p => only(p, 'docs', 'changelog'), p => `${p.k.join(' + ')} only · +${p.ad}/−${p.de}`, 'docs'],
  ['tests and ci only', 'test and workflow changes, nothing user-facing', p => only(p, 'tests', 'ci'), p => `${p.k.join(' + ')} only · +${p.ad}/−${p.de}`, 'tests'],
  ['dependency bumps', 'vendor and go.mod changes only', p => only(p, 'vendor'), p => `vendor only · +${p.ad}/−${p.de} · ${p.f} files`, 'deps'],
  ['small fixes waiting on us', 'effort 1–2, fix-shaped, in the maintainers\' court', p => p.ef <= 2 && p.ct === 'maintainer' && fixShaped(p) && !p.approved && !only(p, 'docs', 'changelog', 'tests', 'ci', 'vendor'), p => `effort ${p.ef} · ${p.sv.join(', ') || 'no service'} · ${p.fr < 0 ? 'never answered' : 'last maintainer touch ' + fmtDays(daysSince(p.lm)) + ' ago'}`, 'fixes'],
  ['small enhancements, one service', 'effort 1–2, a single service, in the maintainers\' court', p => p.ef <= 2 && p.ct === 'maintainer' && p.sv.length === 1 && !fixShaped(p) && !p.approved && !only(p, 'docs', 'changelog', 'tests', 'ci', 'vendor'), p => `effort ${p.ef} · ${p.sv[0]} · ${p.li && p.li.length ? 'closes #' + p.li.join(', #') : 'no linked issue'}`, 'enhancements'],
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
  view.querySelectorAll('a.clk[data-q]').forEach(a => a.addEventListener('click', e => { e.stopPropagation(); if (a.dataset.q) S.q = a.dataset.q; set('tab', 'data'); }));
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

// ---- the areas tab ----
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
    <div class="panel table"><h2>kinds of change<span class="desc">schema · code · tests · docs · vendor · ci · changelog</span></h2>${table(KC.filter(c => !['authors', 'reviewers'].includes(c[0])), kinds, sk, 'asort')}</div>
    <div class="panel table"><h2>labels</h2>${table(LC.filter(c => !['authors', 'reviewers', 'ttm', 'fr'].includes(c[0])), labels, sk, 'asort', 40)}</div>
    </div>`;
  const top = services.slice().sort((a, b) => b.open - a.open).slice(0, 30);
  barChart($('#c-svc'), top.map(r => ({ label: r.name.length > 9 ? r.name.slice(0, 8) + '…' : r.name, value: r.open, color: r.inCourt / Math.max(r.open, 1) > 0.5 ? 'var(--s1)' : 'var(--s4)', title: `${r.name}: ${r.open} open, ${r.inCourt} in the maintainers' court, ${r.never} unanswered` })), { h: 200 });
  bindTableSorts(view); bindRowClicks(view);
}

// ---- the checks tab ----
function renderChecks(view) {
  tabControls('');
  if (!D.checks || !D.checks.length) { view.innerHTML = `<div class="panel wide"><p class="note">${esc(D.checksNote || 'no close candidates')}</p></div>`; return; }
  const inM = new Set(M.map(p => p.n));
  const byCheck = new Map();
  for (const c of D.checks) { if (!inM.has(c.n)) continue; if (!byCheck.has(c.check)) byCheck.set(c.check, []); byCheck.get(c.check).push(c); }
  view.innerHTML = `<p class="note">every close candidate the checks saw when this page was generated, restricted to the filter · act with <code>prawn close &lt;check&gt; --apply-with-ai</code> · regenerate with <code>prawn explore</code></p>
    <div class="checks panels">${[...byCheck.entries()].map(([name, items]) => `<div class="panel wide check"><h2>close ${esc(name)}<span class="desc">${items.length} candidates</span></h2>
      ${items.map(c => { const p = BY_N.get(c.n); return `<div class="item"><a href="${prURL(c.n)}" target="_blank" rel="noopener">#${c.n}</a> ${esc(p ? p.t : '')} <span class="badge">${esc(p ? p.a : '')}</span>${c.score ? `<span class="score ${c.score >= 0.7 ? 'ok' : c.score >= 0.4 ? 'mid' : 'bad'}">${c.score}</span>` : ''}
        ${c.ev.map(e => `<div class="ev-line">${esc(e)}</div>`).join('')}${c.reason ? `<div class="ev-line"><i>${esc(c.reason)}</i></div>` : ''}</div>`; }).join('')}</div>`).join('')}
      ${!byCheck.size ? '<div class="empty">no candidates among the matching PRs</div>' : ''}</div>`;
}

// ---- update ----
function update(rerenderFilters) {
  writeHash();
  hideTip();
  // the view controls live in the trends control row, which is about to be rebuilt: park them first
  const vc = $('#viewctl'); if (vc) { $('#viewhold').appendChild(vc); vc.hidden = true; }
  applyFilter();
  if (rerenderFilters) renderFilters();
  else syncFilterButtons();
  $('#f-n').textContent = fmtNum(M.length);
  renderTabs();
  const view = $('#view'); view.innerHTML = '';
  if (S.tab === 'queue') S.tab = 'data'; // the tab's old name, in bookmarked links
  ({ data: renderQueue, trends: renderTrends, suggested: renderSuggested, people: renderPeople, areas: renderAreas, checks: renderChecks }[S.tab] || renderQueue)(view);
  // the saved views are the data tab's columns and sort and the trends tab's layouts: their controls live in those tabs' control rows
  if ((S.tab === 'trends' || S.tab === 'data') && vc) { $('#tabctl').appendChild(vc); vc.hidden = false; }
  if ($('#viewSel').innerHTML) renderViews();
  $('#subtitle').textContent = `${fmtNum(D.prs.length)} PRs open at some point since ${D.since} · ${fmtNum(D.prs.filter(p => p.s === 'open').length)} open now · generated ${D.generated} by prawn ${D.version || 'dev'}`;
}
// ---- saved views: the whole url state under a name, in this browser; download/upload/paste to move them (tfpp's layouts) ----
const VIEW_KEY = 'prawn.views';
function loadViews() { try { return JSON.parse(localStorage.getItem(VIEW_KEY) || '{}') || {}; } catch (e) { return {}; } }
function storeViews(vs) { try { localStorage.setItem(VIEW_KEY, JSON.stringify(vs)); } catch (e) { /* private window, full storage: the view just does not persist */ } }
let viewName = 'default';
// the tab is navigation, not part of a view: strip it before comparing
const viewHash = h => { const p = new URLSearchParams(h); p.delete('tab'); return p.toString(); };
function viewEdited() {
  writeHash();
  if (viewName === 'default') return viewHash(location.hash.slice(1)).length > 0;
  const v = loadViews()[viewName]; return !v || viewHash(location.hash.slice(1)) !== viewHash(v.hash);
}
function renderViews(selected) {
  if (selected) viewName = selected;
  const vs = loadViews(), sel = $('#viewSel'); if (!sel) return;
  if (!vs[viewName] && viewName !== 'default') viewName = 'default';
  const edited = viewEdited();
  const opt = n => `<option value="${esc(n)}">${esc(n)}${n === viewName && edited ? ' (edited)' : ''}</option>`;
  sel.innerHTML = opt('default') + Object.keys(vs).sort().map(opt).join('');
  sel.value = viewName;
  $('#viewSave').disabled = viewName === 'default' || !edited;
  $('#viewDel').disabled = viewName === 'default';
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
  $('#viewSave').onclick = () => { if (viewName !== 'default') saveViewAs(viewName); };
  $('#viewSaveAs').onclick = () => { $('#pastebox').hidden = true; $('#viewbox').hidden = false; const inp = $('#viewName'); inp.value = viewName === 'default' ? '' : viewName; inp.focus(); inp.select(); };
  $('#viewCancel').onclick = () => { $('#viewbox').hidden = true; };
  $('#viewOk').onclick = () => { saveViewAs($('#viewName').value.trim()); $('#viewbox').hidden = true; };
  $('#viewName').addEventListener('keydown', ev => { if (ev.key === 'Enter') $('#viewOk').click(); if (ev.key === 'Escape') $('#viewCancel').click(); });
  $('#viewSel').onchange = () => {
    const n = $('#viewSel').value;
    if (n === 'default') { viewName = 'default'; history.replaceState(null, '', location.pathname); readHash(); update(true); return; }
    const v = loadViews()[n]; if (v) { viewName = n; applyView(v); }
  };
  $('#viewDel').onclick = () => { if (viewName === 'default') return; const vs = loadViews(); delete vs[viewName]; storeViews(vs); viewName = 'default'; renderViews(); };
  $('#viewDown').onclick = () => {
    const v = currentView(viewName), a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([JSON.stringify(v, null, 2)], { type: 'application/json' }));
    a.download = `prawn-${viewName.replace(/[^a-z0-9._-]+/gi, '-').toLowerCase()}-${new Date().toISOString().slice(0, 10)}.json`;
    document.body.appendChild(a); a.click(); a.remove(); setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  };
  $('#viewUp').addEventListener('change', async ev => {
    const f = ev.target.files[0]; if (!f) return;
    try { importView(await f.text(), f.name.replace(/^prawn-/, '').replace(/-\d{4}-\d{2}-\d{2}\.json$/, '').replace(/\.json$/, '')); }
    catch (e) { $('#pastebox').hidden = false; $('#pasteErr').textContent = 'could not load: ' + e.message; }
    ev.target.value = '';
  });
  $('#viewPaste').onclick = () => { $('#viewbox').hidden = true; $('#pastebox').hidden = false; $('#pasteErr').textContent = ''; $('#pasteText').value = ''; $('#pasteText').focus(); };
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
Design a view of prawn explore for the ask above. Context: prawn explore is a page over every pull request of ${D.repo} that was open at any point since ${D.since} — ${fmtNum(D.prs.length)} PRs, ${fmtNum(open)} open now, generated ${D.generated}. A global filter picks a set of PRs; tabs show that set: data (a sortable table), trends (metric panels over time), suggested (easy reviews), areas (services), people (authors and reviewers), checks (close candidates).
Answer with ONE json code block and nothing else, in exactly this form:
{"prawn":"view","name":"<short name for the view>","hash":"tab=trends&m=<panels>&gran=day"} — or, for a table, "tab=data&q=<query>&sort=<column>&dc=<columns>"
The hash is a url query string. Filter keys (all optional): from=yyyy-mm-dd, to=yyyy-mm-dd (the period), st=open,merged,closed (states; omitted means open on most tabs and every state on trends), g=<group> (author group: ${[...GROUP_NAMES, 'community'].join(' | ')}), a=<login,login> (authors), svc=<service,service>, k=<kind,kind> (${KINDS.join(' | ')}), l=<label,label>, ct=maintainer|author (whose court an open PR is in), ef=<lo>-<hi> (review effort 1..5), q=<query> (a query language: key:value matches, key>n key<n compare, -key:value excludes; keys: author group svc kind label court state status effort age idle size files rounds fr cd waiting reviewer reviewedby approvedby changesby responder lastmaint mergedby assoc approved decision mergeable ci milestone draft thumbs comments reviews reviewcomments memberreviews memberreviewcomments membercomments approvals check ai n title, suggested (the suggested tab's categories: ${CATEGORIES.map(c => c[4]).join(' | ')}); e.g. "court:maintainer effort<3 idle>30").
Data keys: tab=data, sort=priority|<column key>, dir=asc (reverses the sort), dc=<column key,column key,...> (the columns shown, in order; omitted means ${DEFAULT_COLS.join(',')}). Columns (key: label): ${Object.values(COL).map(c => `${c.k}: ${c.l}`).join(', ')}.
Trends keys: tab=trends, gran=day|week|month, cols=1|2|3, marks=major|minor|none (release markers).
- m: the panels, separated by |. Each panel is an optional flag prefix then a comma separated list of metric keys: "s:" stacks the series as areas (only for same-unit series that add up, like the review statuses or opened-by-group), "b:" draws bars, "sb:" stacked bars, "t:" shows the panel as a table, "w:" makes the panel span the grid; flags combine ("sbw:"). A key prefixed with ! goes on the right-hand axis, with ~ it is plotted but hidden until the user clicks its legend entry (useful for a dominant series that would flatten the others); a panel may mix at most two units and the second unit is put on the right automatically.
- Put closely related series together; 3 to 8 panels is a good view; order them from the most important down. The name should say what the view is about.
Only use metric keys from the list below; anything else is dropped. Groups configured: ${GROUP_NAMES.map(g => `${g} (${D.groups[g].length} logins)`).join(', ')}; maintainers are the ${D.maintainerGroup} group. Services (top 30 by open PRs): ${countBy(p => p.s === 'open' ? p.sv : []).slice(0, 30).map(([v, n]) => `${v} (${n})`).join(', ')}.`;
  const example = `The view currently on the page, as an example of the format:
${location.hash.slice(1) || 'tab=data (the default: open PRs, no filter)'}`;
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
  $('#aiRow').innerHTML = AI_TARGETS.map(t => `<button class="plain" data-ai="${t.id}">${t.label}</button>`).join('') + `<button class="plain" data-ai="copy">copy prompt</button><button class="plain" data-ai="close">close</button>`;
}
function bindAI() {
  $('#viewAI').onclick = () => { const b = $('#aibox'); $('#viewbox').hidden = true; $('#pastebox').hidden = true; b.hidden = !b.hidden; if (!b.hidden) { renderAIBox(); $('#aiAsk').focus(); } };
  $('#aiAsk').addEventListener('input', renderAIBox);
  $('#aiRow').addEventListener('click', ev => {
    const b = ev.target.closest('button[data-ai]'); if (!b) return;
    const ask = $('#aiAsk').value.trim();
    if (b.dataset.ai === 'close') { $('#aibox').hidden = true; return; }
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
  $('#copy-link').addEventListener('click', () => { navigator.clipboard?.writeText(location.href); $('#copy-link').textContent = 'copied'; setTimeout(() => { $('#copy-link').textContent = 'copy link'; }, 1200); });
  window.addEventListener('hashchange', () => { readHash(); update(true); });
  let rt; window.addEventListener('resize', () => { clearTimeout(rt); rt = setTimeout(() => { if (S.tab === 'trends' || S.tab === 'areas') update(); }, 150); });
}
