/* AllTag — virtualised, sortable, multi-select track table */
'use strict';

const COLUMNS = [
  { key: 'format', label: '', w: 46, sortable: true, title: 'Формат' },
  { key: 'name', label: 'Имя файла', w: 268, grow: true, sortable: true },
  { key: 'dir', label: 'Путь', w: 240, sortable: true, cls: 'path' },
  { key: 'tagType', label: 'Тег', w: 92, sortable: true },
  { key: 'title', label: 'Название', w: 210, sortable: true, cls: 'title-cell' },
  { key: 'artist', label: 'Артист', w: 168, sortable: true },
  { key: 'album', label: 'Альбом', w: 168, sortable: true },
  { key: 'track', label: 'Трек', w: 56, sortable: true, num: true },
  { key: 'year', label: 'Год', w: 54, sortable: true, num: true },
  { key: 'genre', label: 'Жанр', w: 118, sortable: true },
  { key: 'bitrate', label: 'Битрейт', w: 76, sortable: true, num: true },
  { key: 'duration', label: 'Длительность', w: 92, sortable: true, num: true },
];

const ROW_H = 22;

const Table = {
  rows: [],
  view: [],
  byId: new Map(),
  selected: new Set(),
  anchor: -1,
  activeId: null,
  sortKey: 'name',
  sortAsc: true,
  filter: '',
  onChange: null,       // (ids, activeId) => void
  onActivate: null,     // (id) => void   double click
  onContext: null,      // (id, x, y) => void
  _raf: 0,

  init(opts) {
    this.onChange = opts.onChange || null;
    this.onActivate = opts.onActivate || null;
    this.onContext = opts.onContext || null;
    this.buildHead();
    const scroller = $('#tableScroll');
    scroller.addEventListener('scroll', () => {
      if (this._raf) return;
      this._raf = requestAnimationFrame(() => { this._raf = 0; this.paint(); });
    });
    scroller.addEventListener('keydown', e => this.onKey(e));
    $('#tbody').addEventListener('mousedown', e => this.onMouseDown(e));
    $('#tbody').addEventListener('dblclick', e => {
      const row = e.target.closest('.tr');
      if (row && this.onActivate) this.onActivate(Number(row.dataset.id));
    });
    $('#tbody').addEventListener('contextmenu', e => {
      const row = e.target.closest('.tr');
      if (!row) return;
      e.preventDefault();
      const id = Number(row.dataset.id);
      if (!this.selected.has(id)) this.selectOne(id);
      if (this.onContext) this.onContext(id, e.clientX, e.clientY);
    });
    window.addEventListener('resize', debounce(() => this.paint(), 80));
  },

  setRows(rows, opts = {}) {
    this.rows = rows || [];
    this.byId.clear();
    for (const r of this.rows) this.byId.set(r.id, r);
    const ids = new Set(this.rows.map(r => r.id));
    for (const id of Array.from(this.selected)) if (!ids.has(id)) this.selected.delete(id);
    if (opts.resetSelection) { this.selected.clear(); this.anchor = -1; this.activeId = null; }
    if (this.sortKey) this.sortRows();
    this.applyFilter(true);
    this.emit();
  },

  buildHead() {
    const head = $('#thead');
    head.innerHTML = '';
    for (const col of COLUMNS) {
      const th = el('div', 'th' + (col.num ? ' center' : ''));
      th.dataset.key = col.key;
      th.title = col.title || col.label;
      th.style.width = col.w + 'px';
      th.style.flex = col.grow ? '1 1 ' + col.w + 'px' : '0 0 ' + col.w + 'px';
      th.innerHTML = `<span>${escapeHtml(col.label)}</span><span class="sortmark"></span>`;
      th.addEventListener('click', () => this.setSort(col.key));
      head.appendChild(th);
    }
    this.markSort();
  },

  markSort() {
    $$('#thead .th').forEach(th => {
      const on = th.dataset.key === this.sortKey;
      th.classList.toggle('sorted', on);
      const mark = $('.sortmark', th);
      if (mark) mark.textContent = on ? (this.sortAsc ? '▲' : '▼') : '';
    });
  },

  setSort(key) {
    if (this.sortKey === key) {
      this.sortAsc = !this.sortAsc;
    } else {
      this.sortKey = key;
      this.sortAsc = true;
    }
    this.sortRows();
    this.applyFilter(true);
    this.markSort();
    saveSettings();
  },

  sortRows() {
    const key = this.sortKey;
    const dir = this.sortAsc ? 1 : -1;
    const numeric = key === 'bitrate' || key === 'duration' || key === 'size';
    const collator = new Intl.Collator('ru', { numeric: true, sensitivity: 'base' });
    this.rows.sort((a, b) => {
      let x = a[key], y = b[key];
      if (numeric) {
        x = Number(x) || 0; y = Number(y) || 0;
        return (x - y) * dir;
      }
      x = String(x == null ? '' : x);
      y = String(y == null ? '' : y);
      if (!x && y) return 1;
      if (x && !y) return -1;
      return collator.compare(x, y) * dir;
    });
  },

  applyFilter(keepScroll) {
    const q = this.filter.trim().toLowerCase();
    if (!q) {
      this.view = this.rows.slice();
    } else {
      const terms = q.split(/\s+/);
      this.view = this.rows.filter(r => {
        const hay = [r.name, r.dir, r.title, r.artist, r.album, r.genre, r.tagType, r.year]
          .join(' ').toLowerCase();
        return terms.every(t => hay.includes(t));
      });
    }
    $('#emptyState').hidden = this.view.length > 0 || !q;
    if (!keepScroll) $('#tableScroll').scrollTop = 0;
    this.paint();
    this.updateInfo();
  },

  setFilter(q) {
    this.filter = q || '';
    this.applyFilter();
  },

  updateInfo() {
    const info = $('#listInfo');
    if (!info) return;
    const total = this.rows.length;
    if (this.filter) {
      info.textContent = `Найдено ${this.view.length} из ${total}`;
    } else {
      info.textContent = total ? `${fmtCount(total, 'файл', 'файла', 'файлов')}` : '';
    }
  },

  paint() {
    const scroller = $('#tableScroll');
    const body = $('#tbody');
    const total = this.view.length;
    body.style.height = total * ROW_H + 'px';
    if (!total) { body.innerHTML = ''; return; }

    const st = scroller.scrollTop;
    const h = scroller.clientHeight || 600;
    const start = Math.max(0, Math.floor(st / ROW_H) - 6);
    const end = Math.min(total, Math.ceil((st + h) / ROW_H) + 8);

    const parts = [];
    for (let i = start; i < end; i++) {
      const r = this.view[i];
      const selected = this.selected.has(r.id);
      const cls = ['tr', i % 2 ? 'odd' : 'even'];
      if (selected) cls.push('selected');
      if (r.id === this.activeId) cls.push('active');
      const style = `top:${i * ROW_H}px`;
      parts.push(
        `<div class="${cls.join(' ')}" data-id="${r.id}" data-i="${i}" style="${style}">` +
        this.cellFormat(r) + this.cellName(r) + this.cellPath(r) + this.cellTag(r) +
        this.cellText(r.title) + this.cellText(r.artist) + this.cellText(r.album) +
        this.cellNum(r.track) + this.cellNum(r.year) + this.cellText(r.genre) +
        this.cellBitrate(r) + this.cellDuration(r) +
        '</div>');
    }
    body.innerHTML = parts.join('');
  },

  cellFormat(r) {
    const key = String(r.format || '').toLowerCase();
    const known = ['mp3', 'flac', 'ogg', 'opus', 'm4a', 'wma', 'ape', 'wav', 'aiff', 'wv', 'mpc'];
    const cls = known.includes(key) ? key : 'other';
    const label = (r.format || '?').slice(0, 4).toUpperCase();
    return `<div class="td" style="width:${COLUMNS[0].w}px;flex:0 0 ${COLUMNS[0].w}px">` +
      `<span class="fmt ${cls}" title="${escapeHtml(r.format || '')}">${escapeHtml(label)}</span></div>`;
  },

  cellName(r) {
    const c = COLUMNS[1];
    return `<div class="td" style="width:${c.w}px;flex:1 1 ${c.w}px" title="${escapeHtml(r.path)}">` +
      escapeHtml(r.name) + '</div>';
  },

  cellPath(r) {
    const c = COLUMNS[2];
    return `<div class="td path" style="width:${c.w}px;flex:0 0 ${c.w}px" title="${escapeHtml(r.dir)}">` +
      escapeHtml(r.dir) + '</div>';
  },

  cellTag(r) {
    const c = COLUMNS[3];
    const none = !r.hasTags || r.tagType === 'none';
    const art = r.hasArt ? '<span class="art-dot" title="есть обложка"></span>' : '';
    const err = r.error ? ` <span class="tagchip none" title="${escapeHtml(r.error)}">!</span>` : '';
    return `<div class="td" style="width:${c.w}px;flex:0 0 ${c.w}px">` +
      `<span class="tagchip${none ? ' none' : ''}">${escapeHtml(r.tagType || '—')}</span>${art}${err}</div>`;
  },

  cellText(v) {
    return `<div class="td" title="${escapeHtml(v || '')}">${escapeHtml(v || '')}</div>`;
  },

  cellNum(v) {
    return `<div class="td num">${escapeHtml(v || '')}</div>`;
  },

  cellBitrate(r) {
    return `<div class="td num">${r.bitrate ? r.bitrate + ' kbps' : '—'}</div>`;
  },

  cellDuration(r) {
    return `<div class="td num">${fmtDuration(r.duration)}</div>`;
  },

  /* ── selection ─────────────────────────────────────────────────── */

  onMouseDown(e) {
    if (e.button !== 0) return;
    const row = e.target.closest('.tr');
    if (!row) {
      if (!e.shiftKey && !e.ctrlKey) { this.clearSelection(); }
      $('#tableScroll').focus();
      return;
    }
    e.preventDefault();
    $('#tableScroll').focus();
    const idx = Number(row.dataset.i);
    const id = Number(row.dataset.id);
    if (e.shiftKey && this.anchor >= 0) {
      const [a, b] = this.anchor < idx ? [this.anchor, idx] : [idx, this.anchor];
      if (!e.ctrlKey) this.selected.clear();
      for (let i = a; i <= b; i++) this.selected.add(this.view[i].id);
      this.activeId = id;
    } else if (e.ctrlKey || e.metaKey) {
      if (this.selected.has(id)) this.selected.delete(id);
      else this.selected.add(id);
      this.anchor = idx;
      this.activeId = id;
    } else {
      this.selected.clear();
      this.selected.add(id);
      this.anchor = idx;
      this.activeId = id;
    }
    this.paint();
    this.emit();
  },

  selectOne(id) {
    this.selected.clear();
    this.selected.add(id);
    this.activeId = id;
    const idx = this.view.findIndex(r => r.id === id);
    if (idx >= 0) this.anchor = idx;
    this.paint();
    this.emit();
  },

  selectAll() {
    this.selected.clear();
    for (const r of this.view) this.selected.add(r.id);
    this.paint();
    this.emit();
  },

  clearSelection() {
    this.selected.clear();
    this.activeId = null;
    this.paint();
    this.emit();
  },

  selectIds(ids) {
    this.selected = new Set(ids);
    this.paint();
    this.emit();
  },

  selectedIds() { return Array.from(this.selected); },

  activeRow() {
    if (this.activeId != null && this.byId.has(this.activeId)) return this.byId.get(this.activeId);
    return null;
  },

  onKey(e) {
    const total = this.view.length;
    if (!total) return;
    const curIdx = this.activeId != null ? this.view.findIndex(r => r.id === this.activeId) : -1;
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      const next = e.key === 'ArrowDown'
        ? Math.min(total - 1, curIdx + 1)
        : Math.max(0, curIdx <= 0 ? 0 : curIdx - 1);
      if (e.shiftKey) {
        this.selected.add(this.view[next].id);
      } else {
        this.selected.clear();
        this.selected.add(this.view[next].id);
      }
      this.activeId = this.view[next].id;
      if (!e.shiftKey || this.anchor < 0) this.anchor = next;
      this.scrollIntoView(next);
      this.paint();
      this.emit();
      return;
    }
    if (e.key === 'Home' || e.key === 'End') {
      e.preventDefault();
      const next = e.key === 'Home' ? 0 : total - 1;
      this.selected.clear();
      this.selected.add(this.view[next].id);
      this.activeId = this.view[next].id;
      this.anchor = next;
      this.scrollIntoView(next);
      this.paint();
      this.emit();
    }
  },

  scrollIntoView(idx) {
    const scroller = $('#tableScroll');
    const top = idx * ROW_H;
    if (top < scroller.scrollTop) scroller.scrollTop = top;
    else if (top + ROW_H > scroller.scrollTop + scroller.clientHeight) {
      scroller.scrollTop = top + ROW_H - scroller.clientHeight;
    }
  },

  emit() {
    if (this.onChange) this.onChange(this.selectedIds(), this.activeId);
  },
};
