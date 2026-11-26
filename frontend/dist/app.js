/* ===== iFileManager：前端 ===== */
/* 後端方法由 Wails 注入在 window.go.main.App；開發預覽時由 dev/mock.js 模擬。 */
'use strict';
(() => {

/* ---------- 基本工具 ---------- */
const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => [...r.querySelectorAll(s)];
const sleep = ms => new Promise(r => setTimeout(r, ms));
const clamp = (v, a, b) => Math.max(a, Math.min(b, v));
const esc = s => String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const errMsg = e => (typeof e === 'string' ? e : (e && e.message) || String(e));

function el(tag, props, ...kids) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') n.className = v;
    else if (k === 'onClick') n.addEventListener('click', v);
    else n.setAttribute(k, v === true ? '' : v);
  }
  for (const c of kids.flat()) {
    if (c == null || c === false) continue;
    n.append(c.nodeType ? c : document.createTextNode(c));
  }
  return n;
}

const api = new Proxy({}, { get: (_, name) => (...args) => window.go.main.App[name](...args) });

async function waitForApi() {
  for (let i = 0; i < 200; i++) {
    if (window.go && window.go.main && window.go.main.App) return true;
    await sleep(50);
  }
  return false;
}

/* ---------- 狀態與設定 ---------- */
const LS_KEY = 'fm.v1';
const S = {
  set: { fontSize: 18, dark: false, dual: false, vertical: false, hidden: false, closed: {}, split: 0.5 },   // vertical：第二窗格上下排列（false＝左右）
    // split：左窗格佔的寬度比例（欄位顯示與檔名欄寬度是每個窗格各自的設定，存在 panes[i]）
  panes: [],
  active: 0,
  clip: null,          // { mode: 'copy' | 'cut', dir, names:Set, paths:[] }
  boot: null,
  rowH: 41,
  calc: null,          // { key, text }
};

function loadPrefs() {
  try { return JSON.parse(localStorage.getItem(LS_KEY)) || {}; } catch (e) { return {}; }
}
let saveTimer = 0;
function savePrefs() {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => {
    try {
      localStorage.setItem(LS_KEY, JSON.stringify({
        set: S.set,
        active: S.active,
        panes: S.panes.map(p => ({ ti: p.ti, tabs: p.tabs.map(t => ({ path: t.path, sort: t.sort })), cols: p.cols, nameW: p.nameW })),
      }));
    } catch (e) { /* 儲存失敗不影響使用 */ }
  }, 300);
}

const tabOf = p => p.tabs[p.ti];
const curPane = () => S.panes[S.active];
const curTab = () => tabOf(curPane());
const otherPane = () => S.panes[1 - S.active];

function newTab(path, sort) {
  return {
    path, parent: '', hasParent: false, hist: [path], hi: 0,
    entries: [], view: [], nameIdx: new Map(), hiddenCount: 0,
    sel: new Set(), anchor: -1, cursor: -1,
    sort: sort || { key: 'name', dir: 1 }, filter: '', loaded: false, token: 0,
  };
}

/* ---------- 路徑工具 ---------- */
const sepOf = p => (p.includes('\\') || /^[A-Za-z]:/.test(p) ? '\\' : '/');

function joinPath(dir, name) {
  if (dir === '') return name;                 // 「本機」底下的項目名稱就是磁碟機路徑（如 C:\）
  const s = sepOf(dir);
  return dir.endsWith(s) ? dir + name : dir + s + name;
}

function baseName(p) {
  if (p === '') return '';
  const s = sepOf(p);
  const t = p.length > 1 && p.endsWith(s) ? p.slice(0, -1) : p;
  const i = t.lastIndexOf(s);
  const b = i >= 0 ? t.slice(i + 1) : t;
  return b === '' || /^[A-Za-z]:$/.test(b) ? p : b;   // 根目錄：回傳原樣（如 C:\ 或 /）
}

function crumbsOf(p) {
  const out = [{ label: '本機', path: '' }];
  if (p === '') return out;
  const s = sepOf(p);
  const m = s === '\\' ? p.match(/^([A-Za-z]:\\|\\\\[^\\]+\\[^\\]+\\?)/) : p.match(/^\//);
  const root = m ? m[0] : '';
  if (root) out.push({ label: root.length > 1 ? root.replace(/\\$/, '') : root, path: root });
  let acc = root;
  for (const seg of p.slice(root.length).split(s).filter(Boolean)) {
    acc = acc === '' || acc.endsWith(s) ? acc + seg : acc + s + seg;
    out.push({ label: seg, path: acc });
  }
  return out;
}

/* ---------- 格式化與圖示 ---------- */
const pad = n => String(n).padStart(2, '0');
function fmtDateParts(ms) {
  if (!ms) return ['', ''];
  const d = new Date(ms);
  return [`${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`, `${pad(d.getHours())}:${pad(d.getMinutes())}`];
}
function fmtDate(ms) {
  const [d, t] = fmtDateParts(ms);
  return d ? `${d} ${t}` : '';
}
function fmtSize(b) {
  if (b == null) return '';
  if (b < 1024) return b + ' B';
  const u = ['KB', 'MB', 'GB', 'TB'];
  let i = -1;
  do { b /= 1024; i++; } while (b >= 1024 && i < u.length - 1);
  return (b >= 100 ? b.toFixed(0) : b >= 10 ? b.toFixed(1) : b.toFixed(2)) + ' ' + u[i];
}

const SVG = {
  folder: '<svg viewBox="0 0 24 24"><path fill="currentColor" d="M3 6.5A2.5 2.5 0 0 1 5.5 4h4.2c.6 0 1.2.3 1.6.8L12.5 6h6A2.5 2.5 0 0 1 21 8.5v9a2.5 2.5 0 0 1-2.5 2.5h-13A2.5 2.5 0 0 1 3 17.5z"/></svg>',
  file: '<svg viewBox="0 0 24 24"><path fill="currentColor" d="M6.5 3h7.2L19 8.3V19.5a1.5 1.5 0 0 1-1.5 1.5h-11A1.5 1.5 0 0 1 5 19.5v-15A1.5 1.5 0 0 1 6.5 3z"/><path fill="rgba(255,255,255,.55)" d="M13.5 3v4.2A1.3 1.3 0 0 0 14.8 8.5H19z"/></svg>',
  drive: '<svg viewBox="0 0 24 24"><rect x="2.5" y="7.5" width="19" height="10" rx="2.2" fill="currentColor"/><circle cx="17.5" cy="12.5" r="1.2" fill="#fff"/><rect x="5.5" y="11.6" width="7" height="1.8" rx=".9" fill="rgba(255,255,255,.6)"/></svg>',
};
const EXT = {
  'c-image': 'jpg jpeg png gif bmp webp svg ico tif tiff heic avif',
  'c-media': 'mp4 mkv avi mov wmv flv webm mp3 wav flac aac ogg m4a',
  'c-archive': 'zip rar 7z tar gz bz2 xz iso',
  'c-doc': 'txt md pdf doc docx xls xlsx ppt pptx csv rtf odt',
  'c-code': 'js ts json html htm css go rs py java c cpp h cs bat cmd ps1 sh sql xml yml yaml toml ini log',
  'c-exe': 'exe msi dll',
};
const EXT_MAP = {};
for (const [cls, list] of Object.entries(EXT)) for (const x of list.split(' ')) EXT_MAP[x] = cls;
const extOf = n => { const i = n.lastIndexOf('.'); return i > 0 ? n.slice(i + 1).toLowerCase() : ''; };

function iconOf(t, e) {
  if (t.path === '') return ['drive', SVG.drive];
  if (e.isDir) return ['folder', SVG.folder];
  return [EXT_MAP[extOf(e.name)] || 'c-file', SVG.file];
}
function displayName(t, e) {
  return t.path === '' && e.label ? `${e.name}  ${e.label}` : e.name;
}

/* ---------- 提示訊息 ---------- */
function toast(msg, kind) {
  const box = $('#toasts');
  while (box.children.length >= 4) box.firstChild.remove();
  const t = el('div', { class: 'toast ' + (kind || '') }, msg);
  box.append(t);
  setTimeout(() => t.remove(), kind === 'err' ? 7000 : 3500);
}

/* ---------- 對話框 ---------- */
let modalCount = 0;

function makeModal({ title, wide = false, buttons = [] }) {
  const host = $('#modal');
  const dlg = el('div', { class: 'dlg' + (wide ? ' wide-dlg' : ''), role: 'dialog', 'aria-modal': 'true', 'aria-label': title });
  const body = el('div', { class: 'body' });
  const actions = el('div', { class: 'actions' });
  dlg.append(el('h2', null, title), body, actions);
  let resolve;
  const result = new Promise(r => { resolve = r; });
  let closed = false;
  const close = v => {
    if (closed) return;
    closed = true;
    document.removeEventListener('keydown', onKey, true);
    host.classList.remove('show');
    host.replaceChildren();
    modalCount--;
    resolve(v);
  };
  for (const b of buttons) {
    b.node = el('button', { class: 'btn ' + (b.kind || ''), type: 'button' }, b.label);
    b.node.addEventListener('click', () => (b.onClick ? b.onClick(close) : close(b.value)));
    actions.append(b.node);
  }
  function onKey(e) {
    if (e.key === 'Escape') {
      e.preventDefault();
      const c = buttons.find(b => b.cancel);
      if (c) c.node.click(); else close(null);
    } else if (e.key === 'Enter') {
      const tag = e.target.tagName;
      if (tag === 'BUTTON' || tag === 'SELECT' || tag === 'TEXTAREA') return;
      const p = buttons.find(b => b.primary && !b.node.disabled);
      if (p) { e.preventDefault(); p.node.click(); }
    }
  }
  document.addEventListener('keydown', onKey, true);
  host.replaceChildren(dlg);
  host.classList.add('show');
  modalCount++;
  setTimeout(() => {
    const f = dlg.querySelector('[data-focus]') || dlg.querySelector('input:not([type=checkbox]),select') || (buttons.find(b => b.primary) || {}).node;
    if (f) f.focus();
  }, 0);
  return { dlg, body, close, result, buttons };
}

async function askText({ title, label, value = '', okLabel = '確定', selectBase = false, hint = '' }) {
  const input = el('input', { type: 'text', value, spellcheck: 'false', 'data-focus': '1' });
  const m = makeModal({
    title,
    buttons: [
      { label: '取消', cancel: true, value: null },
      { label: okLabel, kind: 'primary', primary: true, onClick: close => close(input.value) },
    ],
  });
  // 原生 append() 會把 null 轉成文字「null」顯示出來，所以先濾掉空的項目
  m.body.append(...[label ? el('label', null, label) : null, input, hint ? el('div', { class: 'hint' }, hint) : null].filter(Boolean));
  input.focus();                                            // 對話框已在 DOM 中，可同步設定選取範圍
  const dot = value.lastIndexOf('.');
  if (selectBase && dot > 0) input.setSelectionRange(0, dot); else input.select();
  return m.result;
}

async function confirmBox({ title, message, okLabel = '確定', danger = false, list = null }) {
  const m = makeModal({
    title,
    buttons: [
      { label: '取消', cancel: true, value: false },
      { label: okLabel, kind: danger ? 'danger' : 'primary', primary: !danger, value: true },
    ],
  });
  m.body.append(el('p', null, message));
  if (list && list.length) {
    const ul = el('ul', { class: 'namelist' });
    list.slice(0, 8).forEach(n => ul.append(el('li', null, n)));
    if (list.length > 8) ul.append(el('li', null, `…另有 ${list.length - 8} 項`));
    m.body.append(ul);
  }
  const r = await m.result;
  return r === true;
}

function showMessage(title, lines, kind) {
  const m = makeModal({ title, buttons: [{ label: '關閉', kind: 'primary', primary: true, cancel: true, value: true }] });
  const ul = el('ul', { class: 'namelist', style: 'max-height:18rem' });
  lines.forEach(l => ul.append(el('li', kind === 'err' ? { class: 'err-text', style: 'font-weight:400' } : null, l)));
  m.body.append(ul);
  return m.result;
}

// 秒數轉成「1 小時 5 分」「3 分 20 秒」「12 秒」
function fmtDuration(sec) {
  if (!isFinite(sec)) return '—';
  sec = Math.round(sec);
  const h = Math.floor(sec / 3600), m = Math.floor(sec % 3600 / 60), s = sec % 60;
  if (h) return `${h} 小時 ${m} 分`;
  if (m) return `${m} 分 ${s} 秒`;
  return `${s} 秒`;
}

/* ---------- 進度視窗（超過 0.25 秒才出現，避免快速操作時閃一下） ---------- */
let onProgress = null;

function beginProgress(title) {
  let m = null, bar = null, cur = null, info = null, track = null;
  const samples = [];   // [時間 ms, 已完成位元組]，用來計算速率
  const timer = setTimeout(() => {
    m = makeModal({
      title,
      buttons: [{ label: '取消', cancel: true, onClick: () => { api.CancelOp(); } }],
    });
    track = el('div', { class: 'bar busy' }, el('i'));
    cur = el('div', { class: 'curfile' }, '正在計算大小…');
    info = el('div', { class: 'hint' });
    bar = track.firstChild;
    m.body.append(cur, track, info);
  }, 250);
  onProgress = p => {
    if (!m) return;
    if (p.phase === 'scan') { cur.textContent = '正在計算大小…'; return; }
    track.classList.remove('busy');
    const pct = p.total > 0 ? clamp(p.done / p.total * 100, 0, 100) : 0;
    bar.style.width = pct.toFixed(1) + '%';
    cur.textContent = p.current || '';
    // 速率：取最近約 3 秒的取樣計算，避免數字跳動；剩餘時間＝剩餘量 ÷ 速率
    const now = performance.now();
    samples.push([now, p.done]);
    while (samples.length > 2 && now - samples[0][0] > 3000) samples.shift();
    const [t0, d0] = samples[0];
    const speed = now - t0 >= 500 ? (p.done - d0) / ((now - t0) / 1000) : 0;
    let rate = '';
    if (speed > 0) {
      const left = Math.max(0, p.total - p.done) / speed;
      rate = `｜${fmtSize(Math.round(speed))}/s｜剩餘 ${fmtDuration(left)}`;
    } else if (now - t0 >= 500) rate = '｜0 B/s';
    info.textContent = `${fmtSize(p.done)} / ${fmtSize(p.total)}（${p.filesDone} / ${p.filesTotal} 個檔案）${rate}`;
  };
  return {
    stop() { clearTimeout(timer); onProgress = null; if (m) m.close(); },
  };
}

/* ---------- 篩選框（預設隱藏） ---------- */
function openFilter(pane) {
  const f = pane.dom.filter;
  f.hidden = false;
  pane.dom.fbtn.setAttribute('aria-expanded', 'true');
  f.focus(); f.select();
}
function clearFilter(pane) {
  const f = pane.dom.filter;
  if (f.value) { f.value = ''; f.dispatchEvent(new Event('input')); }
}
/** 篩選框沒有內容且不在輸入中就收起來；force＝不論焦點都收起。 */
function syncFilter(pane, force) {
  const f = pane.dom.filter;
  const show = !!f.value || (!force && document.activeElement === f);
  f.hidden = !show;
  pane.dom.fbtn.classList.toggle('on', !!f.value);
  pane.dom.fbtn.setAttribute('aria-expanded', String(show));
}

/* ---------- 窗格 DOM ---------- */
function buildPane(idx) {
  const dom = {};
  dom.tabs = el('div', { class: 'tabs', role: 'tablist' });
  dom.crumbs = el('div', { class: 'crumbs', title: '點空白處可直接輸入路徑（Ctrl+L）' });
  dom.input = el('input', { class: 'pathinput', type: 'text', spellcheck: 'false', 'aria-label': '路徑', hidden: true });
  dom.pathbox = el('div', { class: 'pathbox' }, dom.crumbs, dom.input);
  dom.filter = el('input', { class: 'filter', type: 'text', placeholder: '篩選名稱…', 'aria-label': '篩選名稱', spellcheck: 'false', hidden: true });
  // 篩選框預設隱藏，按這個鈕（或 Ctrl+F）才彈出
  dom.fbtn = el('button', { class: 'fbtn', type: 'button', title: '篩選名稱（Ctrl+F）', 'aria-label': '篩選名稱', 'aria-expanded': 'false', tabindex: '-1' });
  dom.fbtn.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6" fill="none" stroke="currentColor" stroke-width="2.2"/><path d="M15 15l5 5" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"/></svg>';
  dom.head = el('div', { class: 'colhead' },
    el('span', { class: 'ico' }),
    el('span', { class: 'nm' }, el('button', { 'data-sort': 'name' }, '名稱'),
      el('span', { class: 'nmgrip', title: '拖曳調整檔名欄寬度（雙擊還原）', 'aria-hidden': 'true' })),
    el('span', { class: 'dt' }, el('button', { class: 'ct', 'data-col': 'mod', tabindex: '-1' }), el('button', { 'data-sort': 'mod' }, '修改時間')),
    el('span', { class: 'sz' }, el('button', { class: 'ct', 'data-col': 'size', tabindex: '-1' }), el('button', { 'data-sort': 'size' }, '大小')));
  dom.rows = el('div', { class: 'rows' });
  dom.empty = el('div', { class: 'empty', hidden: true });
  dom.spacer = el('div', { class: 'spacer' }, dom.rows, dom.empty);
  dom.list = el('div', { class: 'list', tabindex: '-1' }, dom.spacer);
  dom.cnt = el('span');
  dom.selinfo = el('span');
  // 分頁列右上角：上一頁（倒退）、下一頁、上一層
  dom.bBack = el('button', { class: 'pnav', type: 'button', 'data-pnav': 'back', title: '上一頁（Alt+←）', 'aria-label': '上一頁', tabindex: '-1' }, '←');
  dom.bFwd = el('button', { class: 'pnav', type: 'button', 'data-pnav': 'forward', title: '下一頁（Alt+→）', 'aria-label': '下一頁', tabindex: '-1' }, '→');
  dom.bUp = el('button', { class: 'pnav', type: 'button', 'data-pnav': 'up', title: '上一層（Backspace）', 'aria-label': '上一層', tabindex: '-1' }, '↑');
  dom.root = el('section', { class: 'pane', 'data-p': idx },
    el('div', { class: 'tabbar' }, dom.tabs, el('div', { class: 'pnavs' }, dom.bBack, dom.bFwd, dom.bUp)),
    el('div', { class: 'pathrow' }, dom.pathbox, dom.filter, dom.fbtn),
    dom.head, dom.list,
    el('div', { class: 'pstatus' }, dom.cnt, dom.selinfo));
  return dom;
}

function wirePane(pane) {
  const d = pane.dom;
  d.root.addEventListener('mousedown', () => setActive(pane), true);
  // 窗格右上角的上一頁／上一層：先切換成作用窗格再執行
  d.bBack.addEventListener('click', () => { setActive(pane); ACT.back(); });
  d.bFwd.addEventListener('click', () => { setActive(pane); ACT.forward(); });
  d.bUp.addEventListener('click', () => { setActive(pane); ACT.up(); });

  // 分頁
  d.tabs.addEventListener('click', e => {
    const close = e.target.closest('[data-close]');
    if (close) { closeTab(pane, +close.dataset.close); return; }
    if (e.target.closest('[data-new]')) { addTab(pane); return; }
    const tab = e.target.closest('.tab');
    if (tab) switchTab(pane, +tab.dataset.i);
  });
  d.tabs.addEventListener('auxclick', e => {
    if (e.button !== 1) return;
    const tab = e.target.closest('.tab');
    if (tab) { e.preventDefault(); closeTab(pane, +tab.dataset.i); }
  });

  // 路徑列
  d.crumbs.addEventListener('click', e => {
    const b = e.target.closest('.crumb');
    if (b) go(pane, b.dataset.path);
    else startPathEdit(pane);
  });
  d.input.addEventListener('keydown', async e => {
    if (e.key === 'Enter') {
      e.preventDefault();
      if (await go(pane, d.input.value)) endPathEdit(pane);
    } else if (e.key === 'Escape') {
      e.preventDefault();
      endPathEdit(pane);
    }
  });
  d.input.addEventListener('blur', () => endPathEdit(pane));

  // 篩選
  d.filter.addEventListener('input', () => {
    const t = tabOf(pane);
    t.filter = d.filter.value;
    d.fbtn.classList.toggle('on', !!t.filter);
    rebuildView(t);
    renderRows(pane); renderStatus(pane);
  });
  d.fbtn.addEventListener('mousedown', e => e.preventDefault());   // 不讓篩選框先失去焦點而被收起
  d.fbtn.addEventListener('click', () => {
    if (d.filter.hidden) openFilter(pane);
    else { clearFilter(pane); syncFilter(pane, true); d.list.focus(); }
  });
  d.filter.addEventListener('blur', () => syncFilter(pane));
  d.filter.addEventListener('keydown', e => {
    if (e.key === 'Escape') { clearFilter(pane); d.filter.blur(); }
    else if (e.key === 'Enter' || e.key === 'ArrowDown') { e.preventDefault(); d.filter.blur(); d.list.focus(); if (tabOf(pane).cursor < 0) moveCursor(1, false); }
  });

  // 排序
  // 檔名欄寬度：拖曳標題右緣的分隔，雙擊還原為自動
  d.head.addEventListener('pointerdown', e => {
    const g = e.target.closest('.nmgrip');
    if (!g || e.button !== 0) return;
    e.preventDefault();
    const nm = g.parentElement, x0 = e.clientX, w0 = nm.getBoundingClientRect().width;
    // 最大寬度＝目前寬度＋標題列右側剩餘空間，避免拖過頭後要往回拖一大段才有反應
    const free = Math.max(0, d.head.getBoundingClientRect().right - parseFloat(getComputedStyle(d.head).paddingRight) - d.head.lastElementChild.getBoundingClientRect().right);
    g.setPointerCapture(e.pointerId);
    document.documentElement.classList.add('nmdrag');
    const move = ev => setNameW(pane, Math.min(w0 + ev.clientX - x0, w0 + free) / S.set.fontSize, false);
    const up = () => {
      g.removeEventListener('pointermove', move); g.removeEventListener('pointerup', up); g.removeEventListener('pointercancel', up);
      document.documentElement.classList.remove('nmdrag');
      savePrefs();
    };
    g.addEventListener('pointermove', move); g.addEventListener('pointerup', up); g.addEventListener('pointercancel', up);
  });
  d.head.addEventListener('dblclick', e => {
    if (e.target.closest('.nmgrip')) { setNameW(pane, 0); }
  });
  d.head.addEventListener('click', e => {
    const c = e.target.closest('[data-col]');            // 欄位標題前的 + / −：顯示或隱藏該欄（不觸發排序）
    if (c) { toggleCol(pane, c.dataset.col); return; }
    const b = e.target.closest('[data-sort]');
    if (!b) return;
    const t = tabOf(pane), k = b.dataset.sort;
    t.sort = t.sort.key === k ? { key: k, dir: -t.sort.dir } : { key: k, dir: 1 };
    rebuildView(t);
    renderHead(pane); renderRows(pane); savePrefs();
  });

  // 清單
  let raf = 0;
  d.list.addEventListener('scroll', () => {
    if (raf) return;
    raf = requestAnimationFrame(() => { raf = 0; renderRows(pane); });
  });
  new ResizeObserver(() => renderRows(pane)).observe(d.list);

  d.rows.addEventListener('mousedown', e => rowMouseDown(pane, e));
  d.rows.addEventListener('click', e => rowClick(pane, e));
  d.list.addEventListener('mousedown', e => {
    if (e.clientX - d.list.getBoundingClientRect().left > d.list.clientWidth) return;   // 點在捲軸上
    if (e.target === d.list || e.target === d.spacer || e.target === d.rows) {
      const t = tabOf(pane);
      t.sel = new Set(); t.anchor = -1;
      onSelChange();
    }
  });
  d.list.addEventListener('contextmenu', e => onListContextMenu(pane, e));
  d.rows.addEventListener('dragstart', e => rowDragStart(pane, e));
  d.rows.addEventListener('dragend', () => dragEnd());
  d.list.addEventListener('dragover', e => dragOver(pane, e));
  d.list.addEventListener('dragleave', e => { if (!d.list.contains(e.relatedTarget)) clearDropMarks(); });
  d.list.addEventListener('drop', e => dropOn(pane, e));
}

function setActive(pane) {
  if (S.panes[S.active] === pane) return;
  S.active = pane.idx;
  S.panes.forEach(p => p.dom.root.classList.toggle('active', p === pane));
  updateSide();
  savePrefs();
}

/* ---------- 讀取與檢視 ---------- */
const collator = new Intl.Collator('zh-Hant-TW', { numeric: true, sensitivity: 'base' });

function rebuildView(t) {
  const q = t.filter.trim().toLowerCase();
  let v = t.entries;
  if (!S.set.hidden) v = v.filter(e => !e.hidden);
  if (q) v = v.filter(e => e.name.toLowerCase().includes(q));
  const { key, dir } = t.sort;
  v = v.slice().sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;     // 資料夾永遠在前
    let r = 0;
    if (key === 'name') r = collator.compare(a.name, b.name);
    else if (key === 'mod') r = a.mod - b.mod;
    else if (key === 'size') r = a.size - b.size;
    if (r === 0 && key !== 'name') r = collator.compare(a.name, b.name);
    return r * dir;
  });
  t.view = v;
  t.nameIdx = new Map(v.map((e, i) => [e.name, i]));
  t.hiddenCount = S.set.hidden ? 0 : t.entries.filter(e => e.hidden).length;
  t.sel = new Set([...t.sel].filter(n => t.nameIdx.has(n)));
  if (t.cursor >= v.length) t.cursor = v.length - 1;
  if (t.anchor >= v.length) t.anchor = -1;
}

/** 讀取資料夾。成功回傳 true。select：讀取後要選取的名稱集合。 */
async function navigate(pane, t, path, { push = true, keepSel = false, quiet = false, select = null, histIndex = null } = {}) {
  const token = ++t.token;
  try {
    const l = await api.ListDir(path);
    if (token !== t.token) return false;
    const samePath = t.loaded && l.path === t.path;
    const prevSel = keepSel ? new Set(t.sel) : new Set();
    const curName = keepSel && t.cursor >= 0 && t.view[t.cursor] ? t.view[t.cursor].name : null;
    t.path = l.path; t.parent = l.parent; t.hasParent = l.hasParent;
    t.entries = l.entries; t.loaded = true;
    if (!samePath) { t.filter = ''; pane.dom.filter.value = ''; syncFilter(pane); t.cursor = -1; t.anchor = -1; }
    if (histIndex != null) t.hi = histIndex;
    else if (push && !samePath) {
      t.hist = t.hist.slice(0, t.hi + 1);
      t.hist.push(l.path);
      t.hi = t.hist.length - 1;
    }
    t.sel = prevSel;
    rebuildView(t);
    if (curName != null && t.nameIdx.has(curName)) t.cursor = t.anchor = t.nameIdx.get(curName);
    if (select && select.size) {
      t.sel = new Set([...select].filter(n => t.nameIdx.has(n)));
      const first = [...t.sel][0];
      if (first != null) t.cursor = t.anchor = t.nameIdx.get(first);
    }
    if (!samePath) pane.dom.list.scrollTop = 0;
    renderPane(pane);
    if (select && t.cursor >= 0) ensureVisible(pane, t.cursor);
    if (pane === curPane()) updateSide();
    savePrefs();
    return true;
  } catch (e) {
    if (token !== t.token) return false;
    if (!quiet) toast(errMsg(e), 'err');
    return false;
  }
}

function go(pane, path, opts) { return navigate(pane, tabOf(pane), path, opts); }

async function goHist(delta) {
  const pane = curPane(), t = tabOf(pane);
  const i = t.hi + delta;
  if (i < 0 || i >= t.hist.length) return;
  await navigate(pane, t, t.hist[i], { push: false, histIndex: i });
}

async function goUp() {
  const pane = curPane(), t = tabOf(pane);
  if (!t.hasParent) return;
  const from = baseName(t.path);
  await navigate(pane, t, t.parent, { select: new Set([from]) });
}

async function refreshTab(pane, t, { keepSel = true, quiet = false, select = null } = {}) {
  if (!t.loaded) return;
  if (await navigate(pane, t, t.path, { push: false, keepSel, quiet: true, select })) return;
  const cs = crumbsOf(t.path);                      // 資料夾已消失：退到最近存在的上層
  for (let i = cs.length - 2; i >= 0; i--) {
    if (await navigate(pane, t, cs[i].path, { push: false, quiet: true })) {
      if (!quiet) toast('原資料夾已不存在，已回到上一層', 'err');
      return;
    }
  }
}

async function refreshAll(opts) {
  await Promise.all(S.panes.map(p => refreshTab(p, tabOf(p), opts || {})));
  updateSide();
}

/* ---------- 分頁 ---------- */
function addTab(pane, path) {
  const t0 = tabOf(pane);
  const t = newTab(path != null ? path : t0.path, { ...t0.sort });
  pane.tabs.splice(pane.ti + 1, 0, t);
  pane.ti += 1;
  navigate(pane, t, t.path, { push: false });
  renderTabs(pane);
}
function closeTab(pane, i) {
  if (pane.tabs.length <= 1) return;
  pane.tabs.splice(i, 1);
  if (pane.ti >= i && pane.ti > 0) pane.ti -= 1;
  switchTab(pane, pane.ti, true);
}
function switchTab(pane, i, force) {
  if (i === pane.ti && !force) return;
  pane.ti = clamp(i, 0, pane.tabs.length - 1);
  const t = tabOf(pane);
  pane.dom.filter.value = t.filter;
  syncFilter(pane);
  if (t.loaded) renderPane(pane); else navigate(pane, t, t.path, { push: false });
  renderTabs(pane);
  if (pane === curPane()) updateSide();
  savePrefs();
}

/* ---------- 路徑列編輯 ---------- */
function startPathEdit(pane) {
  const d = pane.dom;
  d.pathbox.classList.add('editing');
  d.input.hidden = false;
  d.input.value = tabOf(pane).path;
  d.input.focus(); d.input.select();
}
function endPathEdit(pane) {
  const d = pane.dom;
  if (d.input.hidden) return;
  d.input.hidden = true;
  d.pathbox.classList.remove('editing');
}

/* ---------- 繪製 ---------- */
function renderTabs(pane) {
  const box = pane.dom.tabs;
  box.replaceChildren();
  pane.tabs.forEach((t, i) => {
    box.append(el('div', { class: 'tab' + (i === pane.ti ? ' on' : ''), 'data-i': i, role: 'tab', title: t.path || '本機' },
      el('span', { class: 'tl' }, baseName(t.path) || '本機'),
      pane.tabs.length > 1 ? el('button', { class: 'tx', 'data-close': i, 'aria-label': '關閉分頁', tabindex: '-1' }, '×') : null));
  });
  box.append(el('button', { class: 'tnew', 'data-new': '1', title: '新分頁（Ctrl+T）', 'aria-label': '新分頁' }, '＋'));
  renderPaneNav(pane);
}
function renderPaneNav(pane) {
  const t = tabOf(pane);
  pane.dom.bBack.disabled = !(t.hi > 0);
  pane.dom.bFwd.disabled = !(t.hi < t.hist.length - 1);
  pane.dom.bUp.disabled = !t.hasParent;
}

function renderCrumbs(pane) {
  const t = tabOf(pane), box = pane.dom.crumbs;
  box.replaceChildren();
  const cs = crumbsOf(t.path);
  cs.forEach((c, i) => {
    if (i) box.append(el('span', { class: 'sep' }, '›'));
    box.append(el('button', { class: 'crumb' + (i === cs.length - 1 ? ' last' : ''), 'data-path': c.path, title: c.path || '本機', tabindex: '-1' }, c.label));
  });
  box.scrollLeft = box.scrollWidth;
}

function renderHead(pane) {
  const t = tabOf(pane);
  $$('[data-sort]', pane.dom.head).forEach(b => {
    const on = b.dataset.sort === t.sort.key;
    b.classList.toggle('on', on);
    const label = { name: '名稱', mod: '修改時間', size: '大小' }[b.dataset.sort];
    b.textContent = label + (on ? (t.sort.dir > 0 ? ' ▲' : ' ▼') : '');
  });
  $$('[data-col]', pane.dom.head).forEach(b => {
    const show = !pane.cols || pane.cols[b.dataset.col] !== false;
    const label = { mod: '修改時間', size: '大小' }[b.dataset.col];
    b.textContent = show ? '−' : '+';
    b.title = (show ? '隱藏' : '顯示') + label + '欄';
    b.setAttribute('aria-label', b.title);
    b.setAttribute('aria-expanded', String(show));
  });
}

function isCut(t, e) {
  const c = S.clip;
  return !!c && c.mode === 'cut' && c.dir === t.path && c.names.has(e.name);
}

function rowClass(pane, t, i) {
  const e = t.view[i];
  return 'row' + (t.sel.has(e.name) ? ' sel' : '') + (i === t.cursor ? ' cursor' : '') + (isCut(t, e) ? ' cut' : '') + (e.hidden ? ' hid' : '');
}

function renderRows(pane) {
  const t = tabOf(pane), d = pane.dom, h = S.rowH, n = t.view.length;
  d.spacer.style.height = n * h + 'px';
  const top = d.list.scrollTop;
  const first = Math.max(0, Math.floor(top / h) - 6);
  const last = Math.min(n, Math.ceil((top + d.list.clientHeight) / h) + 6);
  let html = '';
  for (let i = first; i < last; i++) {
    const e = t.view[i];
    const [cls, svg] = iconOf(t, e);
    html += `<div class="${rowClass(pane, t, i)}" style="top:${i * h}px" data-i="${i}" draggable="${t.path === '' ? 'false' : 'true'}">` +
      `<span class="ico ${cls}">${svg}</span>` +
      `<span class="nm" title="${esc(displayName(t, e))}">${esc(displayName(t, e))}</span>` +
      `<span class="dt" title="${t.path === '' ? '' : fmtDate(e.mod)}">${t.path === '' || !e.mod ? '' : `${fmtDateParts(e.mod)[0]}<span class="tm"> ${fmtDateParts(e.mod)[1]}</span>`}</span>` +
      `<span class="sz">${e.isDir ? '' : fmtSize(e.size)}</span></div>`;
  }
  d.rows.innerHTML = html;
  if (!n) {
    d.empty.hidden = false;
    d.empty.textContent = t.loaded ? (t.filter ? '沒有符合篩選條件的項目' : '此資料夾是空的') : '讀取中…';
  } else d.empty.hidden = true;
}

/** 只更新列的選取／游標狀態，不重建元素（避免打斷雙擊與拖曳）。 */
function paintRows(pane) {
  const t = tabOf(pane);
  for (const row of pane.dom.rows.children) row.className = rowClass(pane, t, +row.dataset.i);
}

function selectedEntries(t) {
  return t.view.filter(e => t.sel.has(e.name));
}

function renderStatus(pane) {
  const t = tabOf(pane);
  const sel = selectedEntries(t);
  pane.dom.cnt.textContent = `${t.view.length} 個項目` + (t.hiddenCount ? `（另有 ${t.hiddenCount} 個隱藏項目）` : '');
  if (!sel.length) { pane.dom.selinfo.textContent = ''; return; }
  const bytes = sel.reduce((s, e) => s + (e.isDir ? 0 : e.size), 0);
  pane.dom.selinfo.textContent = `已選取 ${sel.length} 項` + (bytes ? `（${fmtSize(bytes)}）` : '');
}

function renderPane(pane) {
  renderTabs(pane); renderCrumbs(pane); renderHead(pane); renderRows(pane); renderStatus(pane);
}

function renderAll() {
  applyLayout();
  $('#splitter').hidden = !S.set.dual;
  S.panes.forEach(p => {
    p.dom.root.hidden = !S.set.dual && p !== curPane();
    p.dom.root.classList.toggle('active', p === curPane());
    renderPane(p);
  });
  updateSide();
}

function ensureVisible(pane, i) {
  const l = pane.dom.list, top = i * S.rowH, bottom = top + S.rowH;
  if (top < l.scrollTop) l.scrollTop = top;
  else if (bottom > l.scrollTop + l.clientHeight) l.scrollTop = bottom - l.clientHeight;
  renderRows(pane);
}

function onSelChange() {
  const pane = curPane();
  paintRows(pane);
  renderStatus(pane);
  updateSide();
}

/* ---------- 選取與滑鼠 ---------- */
function selectRange(t, a, b) {
  const lo = Math.min(a, b), hi = Math.max(a, b);
  t.sel = new Set(t.view.slice(lo, hi + 1).map(e => e.name));
}

function rowMouseDown(pane, e) {
  if (e.button !== 0 && e.button !== 2) return;
  const row = e.target.closest('.row');
  if (!row) return;
  setActive(pane);
  const t = tabOf(pane), i = +row.dataset.i, ent = t.view[i];
  pane.pendingSingle = null;
  if (e.button === 0 && e.detail === 2 && !e.ctrlKey && !e.shiftKey) { openEntry(pane, ent); return; }
  if (e.shiftKey && t.anchor >= 0) selectRange(t, t.anchor, i);
  else if (e.ctrlKey || e.metaKey) {
    if (t.sel.has(ent.name)) t.sel.delete(ent.name); else t.sel.add(ent.name);
    t.anchor = i;
  } else if (t.sel.has(ent.name) && (t.sel.size > 1 || e.button === 2)) {
    pane.pendingSingle = e.button === 0 ? i : null;      // 多選中按下：放開時才縮成單選，才能拖曳整組
  } else {
    t.sel = new Set([ent.name]); t.anchor = i;
  }
  t.cursor = i;
  onSelChange();
}

function rowClick(pane, e) {
  if (pane.pendingSingle == null) return;
  const t = tabOf(pane), i = pane.pendingSingle;
  pane.pendingSingle = null;
  if (!t.view[i]) return;
  t.sel = new Set([t.view[i].name]); t.anchor = i; t.cursor = i;
  onSelChange();
}

async function openEntry(pane, ent) {
  const t = tabOf(pane);
  const p = joinPath(t.path, ent.name);
  if (ent.isDir) { await navigate(pane, t, p); return; }
  try { await api.Open(p); } catch (e) { toast(errMsg(e), 'err'); }
}

/* ---------- 鍵盤 ---------- */
function moveCursor(delta, extend) {
  const t = curTab(), n = t.view.length;
  if (!n) return;
  setCursor(t.cursor < 0 ? (delta > 0 ? 0 : n - 1) : clamp(t.cursor + delta, 0, n - 1), extend);
}

function setCursor(i, extend) {
  const pane = curPane(), t = tabOf(pane);
  if (!t.view.length) return;
  if (extend) {
    if (t.anchor < 0) t.anchor = t.cursor < 0 ? i : t.cursor;
    selectRange(t, t.anchor, i);
  } else { t.sel = new Set([t.view[i].name]); t.anchor = i; }
  t.cursor = i;
  ensureVisible(pane, i);
  onSelChange();
}

window.addEventListener('keydown', ev => {
  if (modalCount > 0) return;
  const tag = ev.target.tagName;
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
  const k = ev.key, ctrl = ev.ctrlKey || ev.metaKey, shift = ev.shiftKey, alt = ev.altKey;
  if ((k === 'Enter' || k === ' ') && ev.target.closest && ev.target.closest('button, summary')) return;   // 交給按鈕本身處理
  if (k === 'Tab' && !(ev.target === document.body || (ev.target.closest && ev.target.closest('.list')))) return;
  const pane = curPane(), t = tabOf(pane);
  const page = Math.max(1, Math.floor(pane.dom.list.clientHeight / S.rowH) - 1);
  let handled = true;

  if (ctrl && !alt) {
    switch (k.toLowerCase()) {
      case 'a': t.sel = new Set(t.view.map(e => e.name)); onSelChange(); break;
      case 'c': ACT.copy(); break;
      case 'x': ACT.cut(); break;
      case 'v': ACT.paste(); break;
      case 'r': ACT.refresh(); break;
      case 't': ACT.newTab(); break;
      case 'u': ACT.swapPanes(); break;
      case 'w': closeTab(pane, pane.ti); break;
      case 'l': startPathEdit(pane); break;
      case 'f': openFilter(pane); break;
      case '=': case '+': ACT.fontUp(); break;
      case '-': ACT.fontDown(); break;
      case '0': ACT.fontReset(); break;
      default: handled = false;
    }
  } else if (alt) {
    if (k === 'ArrowLeft') ACT.back();
    else if (k === 'ArrowRight') ACT.forward();
    else if (k === 'ArrowUp') ACT.up();
    else handled = false;
  } else {
    switch (k) {
      case 'ArrowDown': moveCursor(1, shift); break;
      case 'ArrowUp': moveCursor(-1, shift); break;
      case 'PageDown': moveCursor(page, shift); break;
      case 'PageUp': moveCursor(-page, shift); break;
      case 'Home': setCursor(0, shift); break;
      case 'End': setCursor(t.view.length - 1, shift); break;
      case 'Enter': ACT.open(); break;
      case 'Backspace': ACT.up(); break;
      case 'Delete': if (shift) ACT.deletePerm(); else ACT.trash(); break;
      case 'F2': ACT.rename(); break;
      case 'F3': ACT.batch(); break;
      case 'F5': ACT.copyOther(); break;
      case 'F6': ACT.moveOther(); break;
      case 'F7': if (shift) ACT.newfile(); else ACT.newdir(); break;
      case 'Tab': setActive(otherPane()); renderAll(); break;
      case 'Escape':
        if (t.sel.size) { t.sel = new Set(); onSelChange(); }
        else if (t.filter) { clearFilter(pane); syncFilter(pane); }
        break;
      default: handled = false;
    }
  }
  if (handled) ev.preventDefault();
});

/* ---------- 右鍵選單：Windows 原生選單（失敗時改用簡易選單） ---------- */
let ctxBusy = false;
let ctxEl = null;

function closeFallbackMenu() {
  if (ctxEl) { ctxEl.remove(); ctxEl = null; }
}

async function onListContextMenu(pane, e) {
  e.preventDefault();
  const list = pane.dom.list;
  if (e.clientX - list.getBoundingClientRect().left > list.clientWidth) return;   // 捲軸上
  closeFallbackMenu();
  if (ctxBusy) return;
  const t = tabOf(pane);
  const row = e.target.closest('.row');
  if (!row && t.path === '') return;                      // 「本機」的空白處沒有選單
  setActive(pane);
  let ents = selectedEntries(t);
  if (row) {
    const i = +row.dataset.i, ent = t.view[i];
    if (!ent) return;
    if (!ents.some(x => x.name === ent.name)) {            // 在未選取的項目上按右鍵：先改選它
      t.sel = new Set([ent.name]); t.anchor = i; t.cursor = i;
      onSelChange();
      ents = [ent];
    }
  } else {
    ents = [];
  }
  const paths = ents.map(x => fullPath(t, x));
  const target = ents.length === 1 && ents[0].isDir ? ents[0] : null;
  const canPaste = !!S.clip && t.path !== '';

  ctxBusy = true;
  let act = '';
  try {
    act = await api.ShellMenu(t.path, paths, canPaste);
  } catch (err) {
    console.warn('[警告] 系統選單無法使用', err);
    toast('系統選單無法開啟，改用簡易選單：' + errMsg(err), 'err');
    showFallbackMenu(e.clientX, e.clientY, pane, ents);
    return;
  } finally {
    ctxBusy = false;
  }
  await runMenuAction(act, pane, target);
}

async function runMenuAction(act, pane, target) {
  switch (act) {
    case 'copy': ACT.copy(); break;
    case 'cut': ACT.cut(); break;
    case 'paste': await doPaste(target ? joinPath(tabOf(pane).path, target.name) : undefined); break;
    case 'rename': ACT.rename(); break;
    case 'delete': ACT.trash(); break;
    case 'invoked':    // 系統已執行（開啟、解壓縮、傳送到…）：稍後重新整理以顯示變更
      setTimeout(() => refreshAll({ keepSel: true, quiet: true }), 700);
      setTimeout(() => refreshAll({ keepSel: true, quiet: true }), 3000);
      break;
    default: break;    // 使用者取消
  }
}

function showFallbackMenu(x, y, pane, ents) {
  const t = tabOf(pane), root = t.path === '', n = ents.length;
  const paste = !!S.clip && !root;
  const defs = n ? [
    ['開啟', ACT.open, n === 1],
    null,
    ['剪下', ACT.cut, !root, 'Ctrl+X'], ['複製', ACT.copy, !root, 'Ctrl+C'], ['貼上', ACT.paste, paste, 'Ctrl+V'],
    null,
    ['重新命名', ACT.rename, n === 1 && !root, 'F2'], ['批次改名', ACT.batch, !root, 'F3'], ['刪除（移到資源回收筒）', ACT.trash, !root, 'Del', true],
    null,
    ['複製路徑', ACT.copyPath, !root], ['在 Windows 檔案總管中顯示', ACT.reveal, !root],
  ] : [
    ['貼上', ACT.paste, paste, 'Ctrl+V'],
    ['新增資料夾', ACT.newdir, !root, 'F7'], ['新增檔案', ACT.newfile, !root, 'Shift+F7'],
    null,
    ['重新整理', ACT.refresh, true, 'Ctrl+R'], ['在 Windows 檔案總管中顯示', ACT.reveal, !root],
  ];
  const menu = el('div', { class: 'ctx', role: 'menu' });
  for (const d of defs) {
    if (!d) { menu.append(el('hr')); continue; }
    const [label, fn, enabled, key, danger] = d;
    const b = el('button', { role: 'menuitem', class: danger ? 'danger' : '', disabled: !enabled }, label, key ? el('small', null, key) : null);
    b.addEventListener('click', () => { closeFallbackMenu(); fn(); });
    menu.append(b);
  }
  document.body.append(menu);
  ctxEl = menu;
  const r = menu.getBoundingClientRect();
  menu.style.left = clamp(x, 4, Math.max(4, innerWidth - r.width - 4)) + 'px';
  menu.style.top = clamp(y, 4, Math.max(4, innerHeight - r.height - 4)) + 'px';
  const first = menu.querySelector('button:not(:disabled)');
  if (first) first.focus();
}

document.addEventListener('mousedown', e => { if (ctxEl && !ctxEl.contains(e.target)) closeFallbackMenu(); }, true);
window.addEventListener('blur', closeFallbackMenu);
window.addEventListener('resize', closeFallbackMenu);
document.addEventListener('keydown', e => {
  if (!ctxEl) return;
  if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeFallbackMenu(); return; }
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault(); e.stopPropagation();
    const bs = [...ctxEl.querySelectorAll('button:not(:disabled)')];
    if (!bs.length) return;
    const i = bs.indexOf(document.activeElement);
    bs[(i + (e.key === 'ArrowDown' ? 1 : -1) + bs.length) % bs.length].focus();
  }
}, true);

document.addEventListener('contextmenu', e => {
  if (!e.target.closest('input, textarea')) e.preventDefault();
});

/* ---------- 檔案操作 ---------- */
const fullPath = (t, e) => joinPath(t.path, e.name);
const selPaths = t => selectedEntries(t).map(e => fullPath(t, e));

function needItems() {
  const t = curTab();
  if (t.path === '') { toast('請先進入磁碟機或資料夾', 'err'); return null; }
  const ents = selectedEntries(t);
  if (!ents.length) { toast('請先選取項目', 'err'); return null; }
  return { t, ents };
}

function openSelected() {
  const pane = curPane(), t = tabOf(pane);
  const ents = selectedEntries(t);
  const ent = ents.length === 1 ? ents[0] : (t.cursor >= 0 ? t.view[t.cursor] : null);
  if (ent) openEntry(pane, ent);
}

async function renameSel() {
  const pane = curPane(), t = tabOf(pane);
  if (t.path === '') return;
  const ents = selectedEntries(t);
  if (ents.length !== 1) { toast(ents.length ? '重新命名一次只能處理一個項目，多個請用「批次改名」' : '請先選取項目', 'err'); return; }
  const ent = ents[0];
  const name = await askText({ title: '重新命名', label: ent.isDir ? '資料夾名稱' : '檔案名稱', value: ent.name, selectBase: !ent.isDir, okLabel: '重新命名' });
  if (name == null || name === ent.name) return;
  try {
    await api.Rename(fullPath(t, ent), name);
    await refreshTab(pane, t, { keepSel: false, select: new Set([name]) });
  } catch (e) { toast(errMsg(e), 'err'); }
}

async function createItem(kind) {
  const pane = curPane(), t = tabOf(pane);
  if (t.path === '') { toast('請先進入磁碟機或資料夾', 'err'); return; }
  const dir = kind === 'dir';
  const name = await askText({ title: dir ? '新增資料夾' : '新增檔案', label: '名稱', value: dir ? '新資料夾' : '新檔案.txt', selectBase: !dir, okLabel: '建立' });
  if (name == null) return;
  try {
    await (dir ? api.CreateDir(t.path, name) : api.CreateFile(t.path, name));
    await refreshTab(pane, t, { keepSel: false, select: new Set([name]) });
  } catch (e) { toast(errMsg(e), 'err'); }
}

function clipSet(mode) {
  const r = needItems();
  if (!r) return;
  S.clip = { mode, dir: r.t.path, names: new Set(r.ents.map(e => e.name)), paths: r.ents.map(e => fullPath(r.t, e)) };
  toast(`已${mode === 'copy' ? '複製' : '剪下'} ${r.ents.length} 項`);
  S.panes.forEach(p => paintRows(p));
  updateSide();
}

async function doPaste(dstDir) {
  const t = curTab();
  const dst = typeof dstDir === 'string' ? dstDir : t.path;     // 右鍵按在資料夾上時，貼到該資料夾內
  if (!S.clip) { toast('剪貼簿是空的', 'err'); return; }
  if (dst === '') { toast('請先進入磁碟機或資料夾', 'err'); return; }
  const clip = S.clip;
  // 剪下：排入佇列後就清掉剪貼簿，避免同一批檔案被重複移動
  const clearCut = () => { if (clip.mode === 'cut' && S.clip === clip) { S.clip = null; S.panes.forEach(p => paintRows(p)); updateSide(); } };
  await transfer(clip.mode === 'cut' ? 'move' : 'copy', clip.paths, dst, { onQueued: clearCut });
}

function toOther(mode) {
  if (!S.set.dual) return;
  const r = needItems();
  if (!r) return;
  const dst = tabOf(otherPane()).path;
  if (dst === '') { toast('對面窗格必須先進入磁碟機或資料夾', 'err'); return; }
  transfer(mode, r.ents.map(e => fullPath(r.t, e)), dst);
}

function chooseConflict(names, mode) {
  const m = makeModal({
    title: '目的地已有同名項目',
    buttons: [
      { label: '取消', cancel: true, value: null },
      { label: '略過', value: 'skip' },
      { label: '保留兩者', value: 'keepboth' },
      { label: '取代／合併', kind: 'primary', primary: true, value: 'overwrite' },
    ],
  });
  m.body.append(el('p', null, `有 ${names.length} 個項目在目的地已存在，要如何${mode === 'copy' ? '複製' : '移動'}？`));
  const ul = el('ul', { class: 'namelist' });
  names.slice(0, 6).forEach(n => ul.append(el('li', null, n)));
  if (names.length > 6) ul.append(el('li', null, `…另有 ${names.length - 6} 項`));
  m.body.append(ul, el('div', { class: 'hint' }, '取代：以新的覆蓋舊的（資料夾會合併）。保留兩者：新的自動改名為「原檔名_BK數字」。'));
  return m.result;
}

/** 複製／移動。回傳 true 表示全部成功（沒有失敗也沒有取消）。 */
// 複製／移動：先檢查同名衝突，再放進背景工作佇列。回傳的 Promise 在該工作完成時才結束（成功為 true）。
// onQueued：工作放進佇列後立即呼叫（例如剪下後貼上，排入後就清掉剪貼簿，避免重複貼上）。
async function transfer(mode, srcs, dst, { onQueued } = {}) {
  let policy = 'keepboth';
  try {
    const info = await api.CheckConflicts(srcs, dst);
    if (info.sameDir && mode === 'move') { toast('來源與目的地相同，不需移動'); return false; }
    if (!info.sameDir && info.names && info.names.length) {
      policy = await chooseConflict(info.names, mode);
      if (!policy) return false;
    }
  } catch (e) { toast(errMsg(e), 'err'); return false; }

  const done = enqueueJob({ mode, srcs, dst, policy });
  if (onQueued) onQueued();
  return done;
}

/* ---------- 背景工作佇列：複製／移動依序執行，進行中仍可繼續操作 ---------- */
const JOBS = { cur: null, queue: [], seq: 0 };
const BUSY_MSG = '已有檔案操作進行中';     // 後端一次只允許一個工作（例如正在計算資料夾大小）

function enqueueJob(spec) {
  return new Promise(resolve => {
    const job = { ...spec, id: ++JOBS.seq, resolve, p: null, samples: [] };
    JOBS.queue.push(job);
    if (JOBS.cur) toast(`已加入排隊（前面還有 ${JOBS.queue.length} 個工作）`);
    renderJobs();
    runNextJob();
  });
}

function jobLabel(j) {
  const verb = j.mode === 'copy' ? '複製' : '移動';
  const what = j.srcs.length === 1 ? (baseName(j.srcs[0]) || j.srcs[0]) : `${j.srcs.length} 項`;
  return `${verb} ${what} → ${baseName(j.dst) || j.dst}`;
}

async function runNextJob() {
  if (JOBS.cur || !JOBS.queue.length) return;
  const job = JOBS.cur = JOBS.queue.shift();
  console.info('[執行] 背景工作開始', job.id, jobLabel(job));
  renderJobs();
  onProgress = p => { job.p = p; updateJobProgress(job); };
  let res = null, err = null;
  for (;;) {
    try { res = await (job.mode === 'copy' ? api.Copy(job.srcs, job.dst, job.policy) : api.Move(job.srcs, job.dst, job.policy)); err = null; }
    catch (e) { err = e; }
    if (err && errMsg(err).includes(BUSY_MSG) && !job.cancelled) { await new Promise(r => setTimeout(r, 1000)); continue; }   // 其他操作進行中：稍後重試
    break;
  }
  onProgress = null;
  JOBS.cur = null;
  renderJobs();
  let ok = false;
  try {
    if (err) { console.error('[錯誤] 背景工作失敗', job.id, err); toast(errMsg(err), 'err'); }
    else ok = await finishTransfer(job, res);
  } finally {
    job.resolve(ok);
    runNextJob();
  }
}

// 工作結束：重新整理清單並回報結果（失敗清單不等待關閉，避免卡住下一個工作）
async function finishTransfer(job, res) {
  const verb = job.mode === 'copy' ? '複製' : '移動';
  await refreshAll({ keepSel: true, quiet: true });
  const failed = (res.failed || []).length + (res.moreFailed || 0);
  if (res.cancelled) toast(`已取消（已${verb} ${res.ok} 項）`, 'err');
  else if (!failed) toast(`已${verb} ${res.ok} 項` + (res.skipped ? `，略過 ${res.skipped} 項` : ''), 'ok');
  if (failed) {
    const lines = [...res.failed];
    if (res.moreFailed) lines.push(`…另有 ${res.moreFailed} 項失敗`);
    showMessage(`${verb}時有 ${failed} 項失敗`, lines, 'err');
  } else if ((res.warnings || []).length) {
    showMessage('已完成，但有項目被略過', res.warnings);
  }
  return !failed && !res.cancelled;
}

function cancelJob(id) {
  if (JOBS.cur && JOBS.cur.id === id) {
    JOBS.cur.cancelled = true;
    api.CancelOp();
    toast('正在取消…');
    return;
  }
  const i = JOBS.queue.findIndex(j => j.id === id);
  if (i < 0) return;
  const [j] = JOBS.queue.splice(i, 1);
  j.resolve(false);
  toast('已從排隊中移除：' + jobLabel(j));
  renderJobs();
}

// 進度文字：已完成 / 總量、檔案數、速率（最近約 3 秒的平均）與剩餘時間
function progressText(job) {
  const p = job.p;
  if (!p || p.phase === 'scan') return '正在計算大小…';
  const now = performance.now(), s = job.samples;
  s.push([now, p.done]);
  while (s.length > 2 && now - s[0][0] > 3000) s.shift();
  const [t0, d0] = s[0];
  const speed = now - t0 >= 500 ? (p.done - d0) / ((now - t0) / 1000) : 0;
  let rate = '';
  if (speed > 0) rate = `｜${fmtSize(Math.round(speed))}/s｜剩餘 ${fmtDuration(Math.max(0, p.total - p.done) / speed)}`;
  else if (now - t0 >= 500) rate = '｜0 B/s';
  return `${fmtSize(p.done)} / ${fmtSize(p.total)}（${p.filesDone} / ${p.filesTotal} 個檔案）${rate}`;
}

function renderJobs() {
  const box = $('#jobs');
  const cur = JOBS.cur, q = JOBS.queue;
  box.hidden = !cur && !q.length;
  if (box.hidden) { box.replaceChildren(); return; }
  const kids = [];
  if (cur) {
    kids.push(el('div', { class: 'job cur', 'data-job': cur.id },
      el('div', { class: 'jrow' },
        el('span', { class: 'jt', title: `${cur.srcs.join('\n')}\n→ ${cur.dst}` }, jobLabel(cur)),
        q.length ? el('span', { class: 'jq' }, `排隊中 ${q.length} 個`) : null,
        el('button', { class: 'jx', type: 'button', 'data-cancel': cur.id, title: '取消目前的工作' }, '取消')),
      el('div', { class: 'curfile' }, cur.p && cur.p.current ? cur.p.current : '準備中…'),
      el('div', { class: 'bar' + (cur.p && cur.p.phase === 'run' ? '' : ' busy') }, el('i')),
      el('div', { class: 'hint' }, cur.p ? progressText(cur) : '準備中…')));
  }
  q.forEach((j, i) => kids.push(el('div', { class: 'job wait' },
    el('span', { class: 'jn' }, `等待 ${i + 1}`),
    el('span', { class: 'jt', title: `${j.srcs.join('\n')}\n→ ${j.dst}` }, jobLabel(j)),
    el('button', { class: 'jx', type: 'button', 'data-cancel': j.id, title: '從排隊中移除' }, '移除'))));
  box.replaceChildren(...kids);
  if (cur) updateJobProgress(cur, true);
}

function updateJobProgress(job, noText) {
  const row = $(`#jobs [data-job="${job.id}"]`);
  if (!row) return;
  const p = job.p;
  const track = $('.bar', row);
  if (!p || p.phase === 'scan') { track.classList.add('busy'); if (!noText) $('.hint', row).textContent = '正在計算大小…'; return; }
  track.classList.remove('busy');
  track.firstChild.style.width = (p.total > 0 ? clamp(p.done / p.total * 100, 0, 100) : 0).toFixed(1) + '%';
  $('.curfile', row).textContent = p.current || '';
  if (!noText) $('.hint', row).textContent = progressText(job);
}

async function trashSel() {
  const r = needItems();
  if (!r) return;
  const pane = curPane(), t = r.t;
  const idx = t.cursor;
  try {
    await api.Trash(r.ents.map(e => fullPath(t, e)));
    toast(`已將 ${r.ents.length} 項移到資源回收筒`, 'ok');
  } catch (e) { toast(errMsg(e), 'err'); }
  await refreshAll({ keepSel: false, quiet: true });
  if (idx >= 0 && t.view.length) {                       // 刪除後把游標留在附近，方便連續操作
    t.cursor = clamp(idx, 0, t.view.length - 1);
    t.sel = new Set([t.view[t.cursor].name]); t.anchor = t.cursor;
    ensureVisible(pane, t.cursor); onSelChange();
  }
}

async function deletePermSel() {
  const r = needItems();
  if (!r) return;
  const ok = await confirmBox({
    title: '永久刪除', danger: true, okLabel: '永久刪除',
    message: `確定要永久刪除這 ${r.ents.length} 個項目嗎？此動作無法復原，也不會進入資源回收筒。`,
    list: r.ents.map(e => e.name),
  });
  if (!ok) return;
  try {
    const res = await api.DeletePermanent(r.ents.map(e => fullPath(r.t, e)));
    await refreshAll({ keepSel: false, quiet: true });
    if ((res.failed || []).length) await showMessage('部分項目無法刪除', res.failed, 'err');
    else toast(`已永久刪除 ${res.ok} 項`, 'ok');
  } catch (e) { toast(errMsg(e), 'err'); }
}

async function copyText(text) {
  try { await navigator.clipboard.writeText(text); return true; } catch (e) { /* 改用備援 */ }
  const ta = el('textarea', { style: 'position:fixed;opacity:0' });
  ta.value = text; document.body.append(ta); ta.select();
  let ok = false;
  try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
  ta.remove();
  return ok;
}

async function copyPath() {
  const t = curTab();
  const ents = selectedEntries(t);
  const text = ents.length ? ents.map(e => fullPath(t, e)).join('\n') : t.path;
  if (!text) { toast('「本機」沒有路徑可複製', 'err'); return; }
  const ok = await copyText(text);
  toast(ok ? (ents.length > 1 ? `已複製 ${ents.length} 個路徑` : '已複製路徑') : '複製失敗', ok ? '' : 'err');
}

async function revealSel() {
  const t = curTab();
  const ents = selectedEntries(t);
  const p = ents.length ? fullPath(t, ents[0]) : t.path;
  if (!p) { toast('「本機」無法在檔案總管中顯示', 'err'); return; }
  try { await api.Reveal(p); } catch (e) { toast(errMsg(e), 'err'); }
}

async function calcSize() {
  const t = curTab(), key = selKey(t);
  const paths = selPaths(t);
  if (!paths.length) return;
  S.calc = { key, text: '計算中…' };
  updateSide();
  try {
    const sz = await api.CalcSize(paths);
    const selDirs = selectedEntries(t).filter(e => e.isDir).length;     // Measure 的資料夾數包含選取的資料夾本身
    if (S.calc && S.calc.key === key) S.calc.text = `總計 ${fmtSize(sz.bytes)}（${sz.files} 個檔案、${Math.max(0, sz.folders - selDirs)} 個子資料夾）`;
  } catch (e) { S.calc = null; toast(errMsg(e), 'err'); }
  updateSide();
}

/* ---------- 批次改名 ---------- */
async function batchRename() {
  const pane = curPane(), t = tabOf(pane);
  if (t.path === '') return;
  const ents = selectedEntries(t);
  if (!ents.length) { toast('請先選取要改名的項目（可用 Ctrl／Shift 多選，或 Ctrl+A 全選）', 'err'); return; }
  const paths = ents.map(e => fullPath(t, e));
  const rule = { mode: 'replace', find: '', replace: '', regex: false, caseSensitive: false, prefix: '', suffix: '', template: '{name}_{n}', start: 1, step: 1, digits: 3, caseMode: 'upper', applyToExt: false };

  const inp = (type, key, props) => {
    const i = el('input', { type, ...props });
    i.value = rule[key];
    i.addEventListener('input', () => {
      if (type === 'number') rule[key] = Number.isFinite(parseInt(i.value, 10)) ? parseInt(i.value, 10) : (key === 'step' ? 1 : 0);
      else rule[key] = i.value;
      schedule();
    });
    return i;
  };
  const chk = (key, text) => {
    const c = el('input', { type: 'checkbox' });
    c.checked = rule[key];
    c.addEventListener('change', () => { rule[key] = c.checked; schedule(); });
    return el('label', { class: 'chk' }, c, text);
  };

  const modes = [['replace', '取代文字'], ['affix', '加前後綴'], ['template', '流水號'], ['case', '大小寫']];
  const seg = el('div', { class: 'seg', role: 'group', 'aria-label': '改名方式' });
  const panels = {};
  modes.forEach(([id, label]) => {
    const b = el('button', { type: 'button', 'aria-pressed': String(id === rule.mode), 'data-mode': id }, label);
    b.addEventListener('click', () => { rule.mode = id; syncMode(); schedule(); });
    seg.append(b);
  });

  const caseSel = el('select');
  [['upper', '全部大寫'], ['lower', '全部小寫'], ['title', '每個字首大寫']].forEach(([v, l]) => caseSel.append(el('option', { value: v }, l)));
  caseSel.addEventListener('change', () => { rule.caseMode = caseSel.value; schedule(); });

  panels.replace = el('div', { class: 'form' },
    el('label', null, '尋找'), inp('text', 'find', { 'data-focus': '1', spellcheck: 'false' }),
    el('label', null, '取代為'), inp('text', 'replace', { spellcheck: 'false' }),
    el('div', { class: 'full' }, chk('regex', '使用規則運算式'), chk('caseSensitive', '區分大小寫')),
    el('div', { class: 'full hint' }, '規則運算式模式下，$1 代表第 1 組括號的內容。留空「取代為」＝刪除找到的文字。'));
  panels.affix = el('div', { class: 'form' },
    el('label', null, '前綴'), inp('text', 'prefix', { spellcheck: 'false' }),
    el('label', null, '後綴'), inp('text', 'suffix', { spellcheck: 'false' }),
    el('div', { class: 'full hint' }, '後綴會加在副檔名之前。'));
  panels.template = el('div', { class: 'form' },
    el('label', null, '新檔名範本'), inp('text', 'template', { spellcheck: 'false' }),
    el('div', { class: 'full trio' },
      el('label', null, '起始號碼', inp('number', 'start')),
      el('label', null, '間隔', inp('number', 'step')),
      el('label', null, '位數（補零）', inp('number', 'digits', { min: 0, max: 9 }))),
    el('div', { class: 'full hint' }, '{name} ＝ 原檔名（不含副檔名），{n} ＝ 流水號。副檔名會自動保留。編號順序依清單目前的排序。'));
  panels.case = el('div', { class: 'form' }, el('label', null, '轉換'), caseSel);
  const extChk = chk('applyToExt', '同時處理副檔名');

  const summary = el('div', { class: 'sumline' });
  const errBox = el('div', { class: 'err-text', hidden: true });
  const tbody = el('tbody');
  const preview = el('div', { class: 'preview' }, el('table', null,
    el('thead', null, el('tr', null, el('th', null, '原名稱'), el('th'), el('th', null, '新名稱'), el('th', null, '狀態'))), tbody));

  const m = makeModal({
    title: `批次改名（${paths.length} 個項目）`, wide: true,
    buttons: [
      { label: '取消', cancel: true, value: false },
      { label: '套用改名', kind: 'primary', primary: true, onClick: apply },
    ],
  });
  const applyBtn = m.buttons[1].node;
  m.body.append(seg, ...Object.values(panels), extChk, el('div', null, summary, errBox), preview);

  function syncMode() {
    $$('[data-mode]', seg).forEach(b => b.setAttribute('aria-pressed', String(b.dataset.mode === rule.mode)));
    for (const [k, p] of Object.entries(panels)) p.hidden = k !== rule.mode;
    extChk.hidden = !(rule.mode === 'replace' || rule.mode === 'case');
  }
  syncMode();

  let timer = 0, seq = 0;
  function schedule() { clearTimeout(timer); timer = setTimeout(refresh, 150); }
  async function refresh() {
    const my = ++seq;
    let items;
    try { items = await api.RenamePreview(paths, rule); }
    catch (e) {
      if (my !== seq) return;
      errBox.hidden = false; errBox.textContent = errMsg(e);
      applyBtn.disabled = true; summary.textContent = '';
      return;
    }
    if (my !== seq) return;
    errBox.hidden = true;
    const ok = items.filter(i => i.status === 'ok').length;
    const bad = items.filter(i => i.status === 'error').length;
    tbody.replaceChildren(...items.map(i => el('tr', { class: 'st-' + i.status },
      el('td', null, i.oldName), el('td', { class: 'arrow' }, '→'),
      el('td', { class: 'new' }, i.newName),
      el('td', null, i.status === 'error' ? i.message : i.status === 'same' ? '不變' : ''))));
    summary.textContent = `共 ${items.length} 項：將改名 ${ok} 項、不變 ${items.length - ok - bad} 項` + (bad ? `，${bad} 項有問題（請修正後才能套用）` : '');
    applyBtn.disabled = bad > 0 || ok === 0;
  }
  async function apply(close) {
    applyBtn.disabled = true;
    try {
      const items = await api.RenameApply(paths, rule);
      close(true);
      const names = new Set(items.map(i => i.newName));
      toast(`已改名 ${items.filter(i => i.status === 'done').length} 項`, 'ok');
      await refreshTab(pane, t, { keepSel: false, select: names, quiet: true });
      S.panes.forEach(p => { if (p !== pane) refreshTab(p, tabOf(p), { quiet: true }); });
    } catch (e) {
      errBox.hidden = false; errBox.textContent = errMsg(e);
      applyBtn.disabled = false;
    }
  }
  refresh();
}

/* ---------- 拖放（拖到另一窗格或資料夾：預設移動，按住 Ctrl 為複製） ---------- */
let dragData = null;

function rowDragStart(pane, e) {
  const row = e.target.closest('.row');
  const t = tabOf(pane);
  if (!row || t.path === '') { e.preventDefault(); return; }
  const ent = t.view[+row.dataset.i];
  if (!t.sel.has(ent.name)) { t.sel = new Set([ent.name]); t.anchor = t.cursor = +row.dataset.i; onSelChange(); }
  pane.pendingSingle = null;
  dragData = { pane, dir: t.path, paths: selPaths(t), names: new Set(t.sel) };
  e.dataTransfer.effectAllowed = 'copyMove';
  e.dataTransfer.setData('text/plain', dragData.paths.join('\n'));
}

function dropTargetOf(pane, e) {
  const t = tabOf(pane);
  const row = e.target.closest ? e.target.closest('.row') : null;
  if (row) {
    const ent = t.view[+row.dataset.i];
    const dragged = dragData && dragData.pane === pane && dragData.names.has(ent.name);
    if (ent && ent.isDir && !dragged) return { path: fullPath(t, ent), row };
  }
  return { path: t.path, row: null };
}

function clearDropMarks() {
  $$('.row.drop').forEach(r => r.classList.remove('drop'));
  $$('.pane.dropzone').forEach(p => p.classList.remove('dropzone'));
}

function dragOver(pane, e) {
  if (!dragData) return;
  const tgt = dropTargetOf(pane, e);
  if (tgt.path === '') return;
  e.preventDefault();
  e.dataTransfer.dropEffect = e.ctrlKey ? 'copy' : 'move';
  clearDropMarks();
  if (tgt.row) tgt.row.classList.add('drop'); else pane.dom.root.classList.add('dropzone');
}

async function dropOn(pane, e) {
  if (!dragData) return;
  e.preventDefault();
  const tgt = dropTargetOf(pane, e);
  const data = dragData;
  dragEnd();
  if (tgt.path === '') return;
  await transfer(e.ctrlKey ? 'copy' : 'move', data.paths, tgt.path);
}

function dragEnd() { dragData = null; clearDropMarks(); }

/* ---------- 左右窗格：調整寬度與互換 ---------- */
const MIN_PANE_REM = 14;

// 依 S.set.vertical 切換窗格排列：左右（預設）或上下
function applyLayout() {
  const v = !!S.set.vertical, sp = $('#splitter');
  $('#panes').classList.toggle('vert', v);
  if (!sp) return;
  sp.setAttribute('aria-orientation', v ? 'horizontal' : 'vertical');
  sp.setAttribute('aria-label', v ? '上下窗格高度' : '左右窗格寬度');
  sp.title = v ? '拖曳調整上下窗格高度（雙擊平分，Ctrl+U 上下互換）' : '拖曳調整左右窗格寬度（雙擊平分，Ctrl+U 左右互換）';
}

function splitBounds() {
  const host = $('#panes'), sp = $('#splitter'), v = !!S.set.vertical;
  const avail = v ? host.clientHeight - sp.offsetHeight : host.clientWidth - sp.offsetWidth;
  if (avail <= 0) return [0.1, 0.9];
  const minPx = Math.min((v ? 8 : MIN_PANE_REM) * S.set.fontSize, avail * 0.35);
  return [minPx / avail, 1 - minPx / avail];
}

// 依 S.set.split 設定兩個窗格的寬度（左窗格＝S.panes[0]）
function applySplit() {
  const [a, b] = S.panes;
  if (!a || !b) return;
  const r = clamp(Number(S.set.split) || 0.5, 0.1, 0.9);
  S.set.split = r;
  // flex-grow 的總和小於 1 時不會填滿剩餘空間（單窗格模式只剩一個窗格），所以放大 1000 倍
  a.dom.root.style.flex = `${r * 1000} 1 0`;
  b.dom.root.style.flex = `${(1 - r) * 1000} 1 0`;
  const sp = $('#splitter');
  sp.style.order = 1;
  a.dom.root.style.order = 0;
  b.dom.root.style.order = 2;
  sp.setAttribute('aria-valuenow', String(Math.round(r * 100)));
}

function setSplit(r) {
  const [lo, hi] = splitBounds();
  S.set.split = clamp(r, lo, hi);
  applySplit();
}

// 左右互換：窗格內容（分頁、選取、捲動位置）整個換邊，寬度跟著窗格走
function swapPanes() {
  if (!S.set.dual || S.panes.length !== 2) return;
  S.panes.reverse();
  S.panes.forEach((p, i) => { p.idx = i; p.dom.root.dataset.p = String(i); });
  S.active = 1 - S.active;                       // 作用窗格仍是同一個，只是換到另一邊
  S.set.split = 1 - clamp(Number(S.set.split) || 0.5, 0.1, 0.9);
  applySplit();
  renderAll();
  savePrefs();
}

function buildSplitter() {
  const sp = el('div', {
    id: 'splitter', role: 'separator', 'aria-orientation': 'vertical', 'aria-label': '左右窗格寬度',
    'aria-valuemin': '10', 'aria-valuemax': '90', tabindex: '0',
    title: '拖曳調整左右窗格寬度（雙擊平分，Ctrl+U 左右互換）',
  });
  const host = $('#panes');
  let dragging = false;
  const ratioAt = e => {
    const r = host.getBoundingClientRect();
    if (S.set.vertical) {
      const avail = r.height - sp.offsetHeight;
      return avail > 0 ? (e.clientY - r.top - sp.offsetHeight / 2) / avail : 0.5;
    }
    const avail = r.width - sp.offsetWidth;
    return avail > 0 ? (e.clientX - r.left - sp.offsetWidth / 2) / avail : 0.5;
  };
  sp.addEventListener('pointerdown', e => {
    if (e.button !== 0) return;
    e.preventDefault();
    dragging = true;
    sp.setPointerCapture(e.pointerId);
    sp.classList.add('drag');
    document.body.classList.add('resizing');
    document.body.classList.toggle('vresize', !!S.set.vertical);
  });
  sp.addEventListener('pointermove', e => { if (dragging) setSplit(ratioAt(e)); });
  const end = () => {
    if (!dragging) return;
    dragging = false;
    sp.classList.remove('drag');
    document.body.classList.remove('resizing');
    savePrefs();
  };
  sp.addEventListener('pointerup', end);
  sp.addEventListener('pointercancel', end);
  sp.addEventListener('lostpointercapture', end);
  sp.addEventListener('dblclick', () => ACT.resetSplit());
  sp.addEventListener('keydown', e => {
    const step = e.shiftKey ? 0.1 : 0.02;
    const v = !!S.set.vertical;
    if (e.key === (v ? 'ArrowUp' : 'ArrowLeft')) setSplit(S.set.split - step);
    else if (e.key === (v ? 'ArrowDown' : 'ArrowRight')) setSplit(S.set.split + step);
    else if (e.key === 'Home') setSplit(0.5);
    else return;
    e.preventDefault();
    savePrefs();
  });
  return sp;
}

/* ---------- 右側面板 ---------- */
const ACT = {
  back: () => goHist(-1),
  forward: () => goHist(1),
  up: () => goUp(),
  refresh: () => refreshAll({ keepSel: true }),
  // 右側面板頂端的重新整理圖示：重新讀取磁碟、快速存取與所有窗格
  async reloadAll() {
    const b = $('#b-reload');
    if (b.classList.contains('spin')) return;
    b.classList.add('spin');
    try {
      await Promise.all([refreshLocations(true), refreshAll({ keepSel: true, quiet: true })]);
      toast('已重新整理');
    } finally {
      b.classList.remove('spin');
    }
  },
  togglePin() { setPin(curTab().path, !$('#b-pin').dataset.pinned); },
  open: openSelected,
  rename: renameSel,
  batch: batchRename,
  newdir: () => createItem('dir'),
  newfile: () => createItem('file'),
  copy: () => clipSet('copy'),
  cut: () => clipSet('cut'),
  paste: doPaste,
  copyOther: () => toOther('copy'),
  moveOther: () => toOther('move'),
  trash: trashSel,
  deletePerm: deletePermSel,
  reveal: revealSel,
  copyPath,
  calc: calcSize,
  newTab: () => addTab(curPane()),
  swapPanes,
  resetSplit() { setSplit(0.5); savePrefs(); },
  toggleDual() { S.set.dual = !S.set.dual; renderAll(); savePrefs(); },
  toggleVertical() { S.set.vertical = !S.set.vertical; applySplit(); renderAll(); savePrefs(); },
  toggleHidden() {
    S.set.hidden = !S.set.hidden;
    S.panes.forEach(p => { rebuildView(tabOf(p)); renderRows(p); renderStatus(p); });
    updateSide(); savePrefs();
  },
  toggleDark() { S.set.dark = !S.set.dark; applyTheme(); updateSide(); savePrefs(); },
  fontUp: () => setFont(S.set.fontSize + 2),
  fontDown: () => setFont(S.set.fontSize - 2),
  fontReset: () => setFont(18),
  help: showHelp,
};

function setFont(px) {
  S.set.fontSize = clamp(px, 14, 30);
  applyFont();
  S.panes.forEach(p => renderRows(p));
  updateSide(); savePrefs();
}
function applyFont() {
  document.documentElement.style.fontSize = S.set.fontSize + 'px';
  S.rowH = Math.round(S.set.fontSize * 2.3);
  document.documentElement.style.setProperty('--rowh', S.rowH + 'px');
}
/** 修改時間／大小欄的顯示狀態（pane.cols）套用到該窗格；左右窗格各自獨立。 */
function applyCols(pane) {
  const c = pane.cols = { mod: true, size: true, ...(pane.cols || {}) };
  pane.dom.root.classList.toggle('hide-dt', c.mod === false);
  pane.dom.root.classList.toggle('hide-sz', c.size === false);
  renderHead(pane);
}
/** 檔名欄寬度（pane.nameW，rem）；0 表示自動填滿。寬度不足時仍會自動縮小，不會擠出窗格。 */
function applyNameW(pane) {
  const w = Number(pane.nameW) || 0;
  pane.dom.root.classList.toggle('nmfixed', w > 0);
  pane.dom.root.style.setProperty('--nmw', w > 0 ? w + 'rem' : 'auto');
}
function setNameW(pane, rem, save = true) {
  pane.nameW = rem > 0 ? Math.round(clamp(rem, 6, 200) * 10) / 10 : 0;
  applyNameW(pane);
  if (save) savePrefs();
}
function toggleCol(pane, key) {
  applyCols(pane);
  pane.cols[key] = !pane.cols[key];
  applyCols(pane); savePrefs();
}
function applyTheme() {
  document.documentElement.dataset.theme = S.set.dark ? 'dark' : 'light';
}

function selKey(t) { return t.path + '|' + [...t.sel].sort().join('|'); }

function updateSide() {
  const pane = curPane();
  if (!pane) return;
  const t = tabOf(pane);
  const ents = selectedEntries(t);
  const n = ents.length, isRoot = t.path === '';
  const other = S.set.dual ? tabOf(otherPane()) : null;

  // 選取資訊卡
  const title = $('#selTitle'), meta = $('#selMeta'), calc = $('#btnCalc');
  const hasDir = ents.some(e => e.isDir) && !isRoot;
  if (n === 0) {
    title.textContent = baseName(t.path) || '本機';
    meta.textContent = `${t.view.length} 個項目，尚未選取`;
  } else if (n === 1) {
    const e = ents[0];
    title.textContent = e.name;
    meta.textContent = isRoot ? (e.label || '磁碟機')
      : `${e.isDir ? '資料夾' : (extOf(e.name) ? extOf(e.name).toUpperCase() + ' 檔案' : '檔案')}｜${e.isDir ? '大小未計算' : fmtSize(e.size)}｜${fmtDate(e.mod)}`;
  } else {
    const files = ents.filter(e => !e.isDir);
    title.textContent = `已選取 ${n} 項`;
    meta.textContent = `${files.length} 個檔案、${n - files.length} 個資料夾｜檔案合計 ${fmtSize(files.reduce((s, e) => s + e.size, 0))}`;
  }
  const key = selKey(t);
  if (S.calc && S.calc.key !== key) S.calc = null;
  calc.hidden = !hasDir;
  calc.textContent = S.calc ? S.calc.text : '計算資料夾大小';
  calc.disabled = !!S.calc && S.calc.text === '計算中…';

  const set = (id, enabled) => { const b = $('#b-' + id); if (b) b.disabled = !enabled; };
  set('back', t.hi > 0);
  set('forward', t.hi < t.hist.length - 1);
  set('up', t.hasParent);
  set('open', n === 1 || (n === 0 && t.cursor >= 0));
  set('rename', n === 1 && !isRoot);
  set('batch', n >= 1 && !isRoot);
  set('newdir', !isRoot);
  set('newfile', !isRoot);
  set('copy', n >= 1 && !isRoot);
  set('cut', n >= 1 && !isRoot);
  set('paste', !!S.clip && !isRoot);
  set('copyOther', n >= 1 && !isRoot && !!other && other.path !== '');
  set('moveOther', n >= 1 && !isRoot && !!other && other.path !== '');
  set('trash', n >= 1 && !isRoot);
  set('deletePerm', n >= 1 && !isRoot);
  set('reveal', !isRoot);
  set('copyPath', !isRoot);

  $$('.dualonly').forEach(b => { b.hidden = !S.set.dual; });
  $('#b-dual').setAttribute('aria-pressed', String(S.set.dual));
  $('#b-dual').textContent = S.set.dual ? '雙窗格：開' : '雙窗格：關';
  $('#b-vert').textContent = S.set.vertical ? '排列：上下' : '排列：左右';
  $('#b-vert').title = S.set.vertical ? '改成左右排列' : '改成上下排列';
  $('#b-swap').innerHTML = (S.set.vertical ? '上下互換' : '左右互換') + '<small>Ctrl+U</small>';
  S.panes.forEach(renderPaneNav);
  $('#b-hidden').setAttribute('aria-pressed', String(S.set.hidden));
  $('#b-dark').setAttribute('aria-pressed', String(S.set.dark));
  $('#b-font').textContent = S.set.fontSize;
  $('#fontRange').value = String(S.set.fontSize);
  $$('#fontbar [data-act=fontDown]').forEach(b => { b.disabled = S.set.fontSize <= 14; });
  $$('#fontbar [data-act=fontUp]').forEach(b => { b.disabled = S.set.fontSize >= 30; });

  const note = $('#clipNote');
  note.hidden = !S.clip;
  if (S.clip) note.textContent = `剪貼簿：${S.clip.paths.length} 項（${S.clip.mode === 'copy' ? '複製' : '剪下'}）`;
  markPlaces();
}

/* ---------- 位置（磁碟與快速存取） ---------- */
const trimSep = p => p.toLowerCase().replace(/[\\/]+$/, '');

function driveButton(d) {
  const total = d.total || 0, free = d.free || 0;
  const used = total > 0 ? Math.round((1 - free / total) * 100) : null;
  const kind = { removable: '卸除式磁碟', network: '網路磁碟', cdrom: '光碟機' }[d.kind];
  const name = d.label || kind || '本機磁碟';
  const tip = `${d.path} ${name}` + (total > 0 ? `｜可用 ${fmtSize(free)} / 共 ${fmtSize(total)}` : '') + '（Ctrl+點擊：在新分頁開啟）';
  return el('button', { class: 'drv', 'data-nav': d.path, title: tip },
    el('span', { class: 'dl' }, d.path.replace(/\\$/, '')),
    el('span', { class: 'dn' }, name),
    el('span', { class: 'df' }, total > 0 ? `剩 ${fmtSize(free)}` : ''),
    used != null ? el('span', { class: 'bar' }, el('i', { class: used >= 90 ? 'full' : '', style: `width:${used}%` })) : null);
}

// 上次成功讀到的系統快速存取：啟動時先顯示，等 PowerShell 讀完再更新（避免一開始只看到「常用資料夾」）
const LS_QUICK = LS_KEY + '.quick';
function loadQuickCache() {
  try { const q = JSON.parse(localStorage.getItem(LS_QUICK)); return Array.isArray(q) && q.length ? q : null; } catch (e) { return null; }
}
function saveQuickCache(q) {
  try { localStorage.setItem(LS_QUICK, JSON.stringify(q)); } catch (e) { /* 儲存失敗不影響使用 */ }
}

function buildPlaces() {
  const cq = S.loc ? null : loadQuickCache();
  const loc = S.loc || (cq ? { drives: S.boot.drives, quick: cq, quickFrom: 'system' }
                           : { drives: S.boot.drives, quick: S.boot.places, quickFrom: 'fallback' });
  $('#drives').replaceChildren(...(loc.drives || []).map(driveButton));
  const quick = loc.quick || [];
  const sys = loc.quickFrom === 'system';
  // 系統快速存取的項目：滑鼠移上去會出現「×」可取消釘選
  $('#quick').replaceChildren(...quick.map(p => el('div', { class: 'qitem' },
    el('button', { class: 'btn line', 'data-nav': p.path, title: `${p.path}（Ctrl+點擊：在新分頁開啟）` }, el('span', null, p.name)),
    sys ? el('button', { class: 'qx', type: 'button', 'data-unpin': p.path, title: `從快速存取取消釘選：${p.path}`, 'aria-label': '取消釘選' }, '×') : null)));
  $('#quickHead').textContent = loc.quickFrom === 'system' ? '快速存取' : '常用資料夾';
  markPlaces();
}

// 「釘選目前資料夾」按鈕：依目前路徑是否已在快速存取切換文字
function updatePinBtn() {
  const b = $('#b-pin');
  if (!b || !S.panes.length) return;
  const loc = S.loc || {}, cur = curTab().path;
  const pinned = loc.quickFrom === 'system' && (loc.quick || []).some(q => trimSep(q.path) === trimSep(cur));
  b.textContent = pinned ? '－ 取消釘選目前資料夾' : '＋ 釘選目前資料夾';
  b.title = cur ? cur : '請先進入資料夾';
  b.disabled = !cur || S.pinBusy || !S.loc;
  b.dataset.pinned = pinned ? '1' : '';
}

async function setPin(path, pin) {
  if (!path || S.pinBusy) return;
  S.pinBusy = true; updatePinBtn();
  try {
    if (pin) await api.PinQuick(path); else await api.UnpinQuick(path);
    toast((pin ? '已釘選到快速存取：' : '已取消釘選：') + (baseName(path) || path));
  } catch (e) {
    console.error('[錯誤] 快速存取操作失敗', e);
    toast(errMsg(e), 'err');
  } finally {
    S.pinBusy = false;
    await refreshLocations(true);
    updatePinBtn();
  }
}

// 標示目前窗格所在的位置（磁碟：位於其底下即標示；其他：路徑完全相同才標示）
function markPlaces() {
  if (!S.panes.length) return;
  const cur = trimSep(curTab().path);
  $$('#side [data-nav]').forEach(b => {
    const p = trimSep(b.dataset.nav);
    const on = b.classList.contains('drv') ? (cur === p || cur.startsWith(p + '\\') || cur.startsWith(p + '/')) : cur === p;
    b.classList.toggle('on', on);
  });
  updatePinBtn();
}

let locBusy = false, locAgain = false, quickRetry = 0, quickRetryTimer = null;
const QUICK_RETRY_MS = [3000, 8000, 20000];   // 讀不到系統快速存取時的重試間隔（開機後 PowerShell／檔案總管可能還沒準備好）
async function refreshLocations(fresh) {
  if (locBusy) { locAgain = true; return; }   // 讀取中又有變更：結束後再讀一次，避免漏掉
  locBusy = true;
  try {
    const l = await (fresh ? api.RefreshLocations() : api.Locations());   // fresh：略過快取，重新向系統查詢
    if (l && Array.isArray(l.drives)) {
      if (l.quickFrom === 'system') {
        quickRetry = 0;
        saveQuickCache(l.quick || []);
      } else {
        const cq = loadQuickCache();
        if (cq) l.quick = cq, l.quickFrom = 'system';   // 暫時沿用上次的快速存取，不要退回「常用資料夾」
        if (quickRetry < QUICK_RETRY_MS.length) {
          const ms = QUICK_RETRY_MS[quickRetry++];
          console.warn(`[警告] 讀不到系統快速存取，${ms / 1000} 秒後重試（第 ${quickRetry} 次）`);
          clearTimeout(quickRetryTimer);
          quickRetryTimer = setTimeout(() => refreshLocations(true), ms);
        }
      }
      S.loc = l; buildPlaces();
    }
  } catch (e) {
    console.warn('[警告] 讀取位置失敗', e);
  } finally {
    locBusy = false;
    if (locAgain) { locAgain = false; refreshLocations(); }
  }
}

// 插入或拔除磁碟（USB 隨身碟、記憶卡、網路磁碟）時由後端通知
const driveOf = p => { const m = /^([A-Za-z]:)/.exec(p || ''); return m ? m[1].toUpperCase() : ''; };
let drvTimer = null;
function onDrivesChanged(ch) {
  const added = ((ch && ch.added) || []).map(driveOf).filter(Boolean);
  const removed = new Set(((ch && ch.removed) || []).map(driveOf).filter(Boolean));
  console.info('[資訊] 磁碟機變更', added, [...removed]);
  if (added.length) toast('偵測到新磁碟：' + added.join('、'));
  if (removed.size) toast('磁碟已移除：' + [...removed].join('、'));
  refreshLocations();
  clearTimeout(drvTimer);
  drvTimer = setTimeout(refreshLocations, 2500);   // 剛插入時磁碟標籤與容量可能還讀不到，稍後再讀一次
  S.panes.forEach(p => p.tabs.forEach((t, i) => {
    const gone = removed.has(driveOf(t.path));
    if (t.path !== '' && !gone) return;
    if (i === p.ti) {
      if (gone) navigate(p, t, '', { quiet: true });              // 正在看的磁碟被拔除：回到「本機」
      else navigate(p, t, '', { push: false, keepSel: true, quiet: true });   // 更新「本機」清單
    } else {
      if (gone) t.path = '';
      t.loaded = false;                                            // 背景分頁：切換過去時再讀取
    }
  }));
}

function showHelp() {
  const rows = [
    ['↑ ↓ / Shift+↑↓', '移動／連續選取'], ['Enter', '開啟'], ['Backspace', '上一層'], ['Alt+← / Alt+→', '返回／前進'],
    ['Tab', '切換左右窗格'], ['Ctrl+U', '左右窗格互換'], ['拖曳中間分隔線', '調整左右窗格寬度（雙擊平分）'], ['Ctrl+A', '全選'], ['Ctrl+C / X / V', '複製／剪下／貼上'], ['F5 / F6', '複製／移動到對面窗格'],
    ['F2', '重新命名'], ['F3', '批次改名'], ['F7 / Shift+F7', '新增資料夾／檔案'], ['Del', '移到資源回收筒'],
    ['Shift+Del', '永久刪除'], ['Ctrl+T / Ctrl+W', '新增／關閉分頁'], ['Ctrl+L', '輸入路徑'], ['Ctrl+F', '篩選目前資料夾'],
    ['Ctrl+R', '重新整理'], ['滑鼠右鍵', '開啟 Windows 系統選單（內容、傳送到、7-Zip 等）'], ['Ctrl + + / − / 0', '放大／縮小／還原字級'], ['拖曳檔案', '移動到另一窗格或資料夾（按住 Ctrl 為複製）'],
  ];
  const m = makeModal({ title: '快捷鍵', buttons: [{ label: '關閉', kind: 'primary', primary: true, cancel: true, value: true }] });
  const g = el('div', { class: 'keys' });
  rows.forEach(([k, d]) => g.append(el('kbd', null, k), el('span', null, d)));
  m.body.append(g);
}

/* ---------- 啟動 ---------- */
async function init() {
  const prefs = loadPrefs();
  if (prefs.set) S.set = { ...S.set, ...prefs.set };
  S.set.dual = false;          // 每次啟動都只顯示一個窗格，需要時再按「＋ 第二窗格」開啟（第二窗格的分頁、欄位設定仍會保留）
  const oldCols = S.set.cols, oldNameW = S.set.nameW;     // 舊版是兩窗格共用的設定：當成兩邊的預設值後移除
  delete S.set.cols; delete S.set.nameW;
  applyTheme(); applyFont();

  if (!(await waitForApi())) {
    document.body.textContent = '無法連線到後端（找不到 window.go）。請以 iFileManager.exe 開啟。';
    return;
  }
  if (window.runtime && window.runtime.EventsOn) {
    window.runtime.EventsOn('op:progress', p => { if (onProgress) onProgress(p); });
    window.runtime.EventsOn('drives:changed', onDrivesChanged);
  }

  S.boot = await api.Bootstrap();
  const home = S.boot.home || '';
  const dl = (S.boot.places.find(p => p.name === '下載') || {}).path;
  const defaults = [home, dl || home];
  const saved = prefs.panes || [];

  const host = $('#panes');
  for (let i = 0; i < 2; i++) {
    const sp = saved[i];
    const tabs = sp && sp.tabs && sp.tabs.length ? sp.tabs.map(t => newTab(typeof t.path === 'string' ? t.path : defaults[i], t.sort)) : [newTab(defaults[i])];
    const pane = { idx: i, tabs, ti: clamp((sp && sp.ti) || 0, 0, tabs.length - 1), dom: buildPane(i), pendingSingle: null,
      cols: { ...(oldCols || {}), ...((sp && sp.cols) || {}) }, nameW: sp && sp.nameW != null ? sp.nameW : (oldNameW || 0) };
    S.panes.push(pane);
    applyCols(pane); applyNameW(pane);
    host.append(pane.dom.root);
    wirePane(pane);
  }
  S.active = 0;               // 單窗格啟動時固定顯示左窗格
  host.insertBefore(buildSplitter(), S.panes[1].dom.root);
  applySplit();

  buildPlaces();
  refreshLocations();          // 之後再讀取磁碟容量與系統的「快速存取」，不阻擋畫面顯示
  $('#fontbar').addEventListener('click', e => {
    const b = e.target.closest('[data-act]');
    if (b && !b.disabled && ACT[b.dataset.act]) { b.blur(); ACT[b.dataset.act](); }
  });
  const fr = $('#fontRange');
  fr.addEventListener('input', () => setFont(Number(fr.value)));     // 拖曳時即時套用
  fr.addEventListener('pointerup', () => setTimeout(() => fr.blur(), 0));   // 放開滑鼠後讓方向鍵回到檔案清單
  $('#jobs').addEventListener('click', e => {
    const c = e.target.closest('[data-cancel]');
    if (c) cancelJob(+c.dataset.cancel);
  });
  $('#side').addEventListener('click', e => {
    const un = e.target.closest('[data-unpin]');
    if (un) { setPin(un.dataset.unpin, false); return; }
    const nav = e.target.closest('[data-nav]');
    if (nav) {
      const pane = curPane();
      if (e.ctrlKey || e.metaKey) addTab(pane, nav.dataset.nav); else go(pane, nav.dataset.nav);
      return;
    }
    const b = e.target.closest('[data-act]');
    if (b && !b.disabled && ACT[b.dataset.act]) { b.blur(); ACT[b.dataset.act](); }
  });
  // 設定面板：按「設定」開關；點面板外或按 Esc 關閉；切換選項後保持開啟，方便連續調整
  const setBtn = $('#b-settings'), setPanel = $('#settingsPanel');
  const showSettings = open => {
    setPanel.hidden = !open;
    setBtn.setAttribute('aria-expanded', String(open));
    setBtn.classList.toggle('on', open);
  };
  setBtn.addEventListener('click', e => { e.stopPropagation(); showSettings(setPanel.hidden); setBtn.blur(); });
  document.addEventListener('mousedown', e => {
    if (!setPanel.hidden && !setPanel.contains(e.target) && !setBtn.contains(e.target)) showSettings(false);
  }, true);
  window.addEventListener('keydown', e => {
    if (e.key === 'Escape' && !setPanel.hidden && modalCount === 0) { e.preventDefault(); e.stopPropagation(); showSettings(false); }
  }, true);
  // 磁碟、快速存取：點標題收合／展開，狀態存在 S.set.closed（drives、quick）
  $$('#side [data-fold]').forEach(h => {
    const key = h.dataset.fold, box = $('#' + key);
    const apply = () => { const c = !!S.set.closed[key]; box.hidden = c; h.setAttribute('aria-expanded', String(!c)); };
    apply();
    h.addEventListener('click', () => { S.set.closed[key] = !S.set.closed[key]; apply(); savePrefs(); });
  });
  $$('#side details').forEach(d => {
    if (S.set.closed[d.dataset.sec]) d.open = false;
    d.addEventListener('toggle', () => { S.set.closed[d.dataset.sec] = !d.open; savePrefs(); });
  });

  renderAll();

  // 初次載入：失敗就退回使用者資料夾，再失敗退回「本機」
  await Promise.all(S.panes.map(async p => {
    const t = tabOf(p);
    for (const path of [t.path, home, '']) {
      if (await navigate(p, t, path, { push: false, quiet: true })) { t.hist = [t.path]; t.hi = 0; return; }
    }
  }));
  renderAll();

  // 回到視窗時自動重新整理（偵測在其他程式所做的變更）
  let last = 0;
  const onFocus = () => {
    if (modalCount > 0 || Date.now() - last < 1500) return;
    last = Date.now();
    refreshAll({ keepSel: true, quiet: true });
    refreshLocations();
  };
  window.addEventListener('focus', onFocus);
  document.addEventListener('visibilitychange', () => { if (!document.hidden) onFocus(); });
}

window.__fm = { S, ACT, navigate };   // 方便除錯與自動化測試
init().catch(e => {
  console.error('[錯誤] 初始化失敗', e);
  toast('初始化失敗：' + errMsg(e), 'err');
});

})();
