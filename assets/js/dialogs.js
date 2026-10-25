/* AllTag — modal dialogs: tag ⇄ filename converter, extended tags, about */
'use strict';

const Dialogs = {
  openIds: [],
  extRows: [],
  extRemoved: [],
  extDirty: new Map(),
  extNew: [],
  extSelected: -1,
  renMode: 'tag2file',
  renPreviews: [],

  /* ── generic plumbing ───────────────────────────────────────────── */

  open(id) {
    $$('.dialog').forEach(d => { d.hidden = true; });
    $('#scrim').hidden = false;
    const dlg = $('#' + id);
    dlg.hidden = false;
    const focusable = dlg.querySelector('input:not([type=hidden]), .btn.filled');
    if (focusable) setTimeout(() => focusable.focus(), 40);
  },

  close(id) {
    if (id) { $('#' + id).hidden = true; } else {
      $$('.dialog').forEach(d => { d.hidden = true; });
    }
    const anyOpen = $$('.dialog').some(d => !d.hidden);
    $('#scrim').hidden = !anyOpen;
  },

  closeAll() { this.close(); },

  init() {
    $$('[data-close]').forEach(btn =>
      btn.addEventListener('click', () => this.close(btn.dataset.close)));
    $('#scrim').addEventListener('click', () => this.closeAll());
    document.addEventListener('keydown', e => {
      if (e.key === 'Escape') {
        const open = $$('.dialog').find(d => !d.hidden);
        if (open) { e.preventDefault(); this.close(open.id); }
      }
    });

    /* converter */
    $$('#renMode .seg').forEach(seg => seg.addEventListener('click', () => {
      $$('#renMode .seg').forEach(s => s.classList.toggle('active', s === seg));
      this.renMode = seg.dataset.mode;
      this.updateRenamer();
    }));
    $('#renMask').addEventListener('input', debounce(() => this.updateRenamer(), 220));
    $('#btnRenameApply').addEventListener('click', () => this.applyRenamer());
    this.buildMacroChips();

    /* extended tags */
    $('#extSearch').addEventListener('input', () => this.renderExt());
    $('#extAdd').addEventListener('click', () => this.addExtRow());
    $('#extDelSel').addEventListener('click', () => this.deleteExtRow());
    $('#extSave').addEventListener('click', () => this.saveExtended());

    /* about */
    $('#btnShortcuts').addEventListener('click', () => { this.close('dlgAbout'); this.open('dlgKeys'); });
  },

  /* ── converter ──────────────────────────────────────────────────── */

  buildMacroChips() {
    const macros = ['%artist%', '%title%', '%album%', '%track%', '%year%',
      '%genre%', '%albumartist%', '%comment%', '%_filename%', '%ext%'];
    const host = $('#macroChips');
    host.innerHTML = '';
    for (const m of macros) {
      const b = el('button', 'chip', m);
      b.type = 'button';
      b.addEventListener('click', () => {
        const input = $('#renMask');
        const start = input.selectionStart ?? input.value.length;
        const end = input.selectionEnd ?? input.value.length;
        input.value = input.value.slice(0, start) + m + input.value.slice(end);
        input.focus();
        input.selectionStart = input.selectionEnd = start + m.length;
        this.updateRenamer();
      });
      host.appendChild(b);
    }
  },

  openRenamer() {
    this.openIds = Table.selectedIds();
    if (!this.openIds.length) { snack('Сначала выберите файлы', 'error'); return; }
    $('#renMask').value = App.settings.renameMask || '%track% - %artist% - %title%';
    this.open('dlgRenamer');
    this.updateRenamer();
  },

  async updateRenamer() {
    const mask = $('#renMask').value.trim();
    App.settings.renameMask = mask;
    saveSettings();
    const ids = this.openIds;
    const preview = $('#renPreview');
    if (!mask) {
      $('#renHint').textContent = 'Введите маску, например %track% - %artist% - %title%';
      preview.innerHTML = '';
      $('#btnRenameApply').disabled = true;
      return;
    }
    try {
      if (this.renMode === 'tag2file') {
        const res = await API.renamePreview(ids, mask);
        this.renPreviews = res.previews || [];
        $('#renPreviewHead').textContent = 'Имя файла сейчас → после применения';
        $('#renHint').textContent =
          'Файлы будут переименованы на диске. Каталог остаётся прежним; ' +
          'при совпадении имён добавляется числовой суффикс.';
        const changed = this.renPreviews.filter(p => p.changed).length;
        $('#btnRenameApply').disabled = changed === 0;
        $('#btnRenameApply').textContent = changed ? `Переименовать (${changed})` : 'Нет изменений';
        preview.innerHTML = this.renPreviews.map(p => `
          <div class="prow ${p.changed ? 'changed' : ''}">
            <span class="old" title="${escapeHtml(p.old)}">${escapeHtml(p.old)}</span>
            <span class="arrow">→</span>
            <span class="new ${p.error ? 'bad' : ''}" title="${escapeHtml(p.new || p.error || '')}">
              ${escapeHtml(p.error ? 'ошибка: ' + p.error : p.new)}</span>
          </div>`).join('');
      } else {
        const res = await API.parsePreview(ids, mask);
        this.renPreviews = res.previews || [];
        $('#renPreviewHead').textContent = 'Имя файла → значения тегов';
        $('#renHint').textContent =
          'Значения будут записаны в теги. Поля, не найденные в имени, останутся без изменений.';
        const matched = this.renPreviews.filter(p => p.matched).length;
        $('#btnRenameApply').disabled = matched === 0;
        $('#btnRenameApply').textContent = matched ? `Записать теги (${matched})` : 'Нет совпадений';
        preview.innerHTML = this.renPreviews.map(p => {
          const vals = p.matched
            ? Object.entries(p.fields).map(([k, v]) => `${k.toLowerCase()}=${v}`).join(' · ')
            : 'не совпадает с маской';
          return `<div class="prow ${p.matched ? 'changed' : ''}">
            <span class="old" title="${escapeHtml(p.name)}">${escapeHtml(p.name)}</span>
            <span class="arrow">→</span>
            <span class="new ${p.matched ? '' : 'bad'}" title="${escapeHtml(vals)}">${escapeHtml(vals)}</span>
          </div>`;
        }).join('');
      }
    } catch (err) {
      snack(err.message, 'error');
    }
  },

  async applyRenamer() {
    const mask = $('#renMask').value.trim();
    try {
      if (this.renMode === 'tag2file') {
        const res = await API.renameApply(this.renPreviews);
        if (res.tracks) App.setTracks(res.tracks, { keepSelection: true });
        const errs = res.errors || [];
        snack(errs.length
          ? `Переименовано ${res.renamed}, ошибок ${errs.length}: ${errs[0]}`
          : `Переименовано ${fmtCount(res.renamed, 'файл', 'файла', 'файлов')}`,
          errs.length ? 'error' : 'info');
      } else {
        const res = await API.parseNames(this.openIds, mask);
        if (res.tracks) App.setTracks(res.tracks, { keepSelection: true });
        const errs = res.errors || [];
        snack(errs.length
          ? `Записано ${res.applied}, ошибок ${errs.length}: ${errs[0]}`
          : `Теги записаны в ${fmtCount(res.applied, 'файл', 'файла', 'файлов')}`,
          errs.length ? 'error' : 'info');
      }
      this.close('dlgRenamer');
      Editor.reload(Table.selectedIds(), Table.activeId);
    } catch (err) {
      snack(err.message, 'error');
    }
  },

  /* ── extended tags ──────────────────────────────────────────────── */

  async openExtended() {
    const ids = Table.selectedIds();
    if (!ids.length) { snack('Сначала выберите файлы', 'error'); return; }
    const id = Table.activeId != null ? Table.activeId : ids[0];
    this.openIds = ids;
    this.extDirty = new Map();
    this.extRemoved = [];
    this.extNew = [];
    this.extSelected = -1;
    try {
      const ext = await API.extended(id);
      this.extRows = (ext.fields || []).map(f => ({ key: f.key, value: f.value, tech: f.tech }));
      $('#extSub').textContent = ids.length > 1
        ? `${ext.name} — изменения применятся к ${fmtCount(ids.length, 'файлу', 'файлам', 'файлам')}`
        : `${ext.name} · ${ext.format} · ${ext.tagType}`;
      $('#extSearch').value = '';
      $('#extNote').textContent = '';
      this.open('dlgExtended');
      this.renderExt();
      this.renderExtPics(id, ext.pictures || []);
    } catch (err) {
      snack(err.message, 'error');
    }
  },

  renderExt() {
    const q = $('#extSearch').value.trim().toLowerCase();
    const host = $('#extList');
    const rows = [];
    const all = [
      ...this.extRows.map((r, i) => ({ ...r, idx: i })),
      ...this.extNew.map((r, i) => ({ ...r, idx: -1 - i, isNew: true })),
    ];
    for (const r of all) {
      if (r.idx >= 0 && this.extRemoved.includes(r.key) && !r.isNew) continue;
      if (q && !(r.key.toLowerCase().includes(q) || String(r.value).toLowerCase().includes(q))) continue;
      const keyCell = r.isNew
        ? `<input class="ext-key-input" value="${escapeHtml(r.key)}" data-newkey="${r.idx}" placeholder="КЛЮЧ">`
        : `<div class="ext-key" title="${escapeHtml(r.key)}">${escapeHtml(r.key)}</div>`;
      rows.push(`
        <div class="ext-row ${r.isNew ? 'new' : ''} ${this.extSelected === r.idx ? 'selected' : ''}"
             data-idx="${r.idx}">
          ${keyCell}
          <input class="ext-val" value="${escapeHtml(r.value)}" data-idx="${r.idx}"
                 spellcheck="false" autocomplete="off">
          <button class="ext-del" data-del="${r.idx}" title="Удалить поле">✕</button>
        </div>`);
    }
    host.innerHTML = rows.length ? rows.join('') :
      '<div class="ext-row"><div class="ext-key">—</div><div class="ext-val" style="opacity:.6">Нет полей по фильтру</div><div></div></div>';

    host.querySelectorAll('.ext-val').forEach(inp => {
      inp.addEventListener('input', () => {
        const idx = Number(inp.dataset.idx);
        if (idx >= 0) {
          this.extRows[idx].value = inp.value;
          this.extDirty.set(this.extRows[idx].key, inp.value);
        } else {
          this.extNew[-1 - idx].value = inp.value;
        }
        $('#extDelSel').disabled = true;
      });
      inp.addEventListener('focus', () => {
        this.extSelected = Number(inp.dataset.idx);
        host.querySelectorAll('.ext-row').forEach(row =>
          row.classList.toggle('selected', Number(row.dataset.idx) === this.extSelected));
        $('#extDelSel').disabled = false;
      });
    });
    host.querySelectorAll('.ext-key-input').forEach(inp => {
      inp.addEventListener('input', () => {
        const idx = -1 - Number(inp.dataset.newkey);
        if (this.extNew[idx]) this.extNew[idx].key = inp.value.toUpperCase();
      });
    });
    host.querySelectorAll('.ext-del').forEach(btn => {
      btn.addEventListener('click', () => {
        const idx = Number(btn.dataset.del);
        if (idx >= 0) {
          const key = this.extRows[idx].key;
          this.extDirty.delete(key);
          if (!this.extRemoved.includes(key)) this.extRemoved.push(key);
          this.extRows[idx].removed = true;
          this.extRows.splice(idx, 1);
        } else {
          this.extNew.splice(-1 - idx, 1);
        }
        this.extSelected = -1;
        this.renderExt();
      });
    });
  },

  addExtRow() {
    this.extNew.push({ key: '', value: '' });
    this.renderExt();
    const inputs = $$('#extList .ext-key-input');
    if (inputs.length) inputs[inputs.length - 1].focus();
  },

  deleteExtRow() {
    if (this.extSelected < 0) return;
    const idx = this.extSelected;
    if (idx >= 0 && this.extRows[idx]) {
      const key = this.extRows[idx].key;
      this.extDirty.delete(key);
      if (!this.extRemoved.includes(key)) this.extRemoved.push(key);
      this.extRows.splice(idx, 1);
    } else if (idx < 0) {
      this.extNew.splice(-1 - idx, 1);
    }
    this.extSelected = -1;
    $('#extDelSel').disabled = true;
    this.renderExt();
  },

  async renderExtPics(id, pics) {
    const host = $('#extPics');
    $('#extPicCount').textContent = pics.length ? `(${pics.length})` : '';
    if (!pics.length) {
      host.innerHTML = '<div class="ext-pic none">нет встроенных изображений</div>';
      return;
    }
    try {
      const res = await API.artwork(id);
      host.innerHTML = `<div class="ext-pic"><img src="${res.data}" alt="">
        <span class="cap">${escapeHtml(pics[0].mime || 'image')}</span></div>` +
        pics.slice(1).map(p => `<div class="ext-pic none">${escapeHtml(p.mime)}<br>${fmtSize(p.size)}</div>`).join('');
    } catch (_) {
      host.innerHTML = pics.map(p =>
        `<div class="ext-pic none">${escapeHtml(p.mime)}<br>${fmtSize(p.size)}</div>`).join('');
    }
  },

  async saveExtended() {
    const set = {};
    for (const [k, v] of this.extDirty) {
      if (!this.extRemoved.includes(k)) set[k] = v;
    }
    for (const row of this.extNew) {
      const key = (row.key || '').trim().toUpperCase();
      if (key) set[key] = row.value;
    }
    const remove = this.extRemoved.slice();
    if (!Object.keys(set).length && !remove.length) {
      this.close('dlgExtended');
      return;
    }
    try {
      const res = await API.extendedSave(this.openIds, set, remove);
      if (res.tracks) App.setTracks(res.tracks, { keepSelection: true });
      const errs = res.errors || [];
      snack(errs.length
        ? `Сохранено ${res.saved}, ошибок ${errs.length}: ${errs[0]}`
        : `Расширенные теги сохранены · ${fmtCount(res.saved, 'файл', 'файла', 'файлов')}`,
        errs.length ? 'error' : 'info');
      this.close('dlgExtended');
      Editor.reload(Table.selectedIds(), Table.activeId);
    } catch (err) {
      snack(err.message, 'error');
    }
  },

  openAbout() {
    $('#aboutVer').textContent = 'версия ' + (App.version || '1.0.0') +
      ' · ' + (App.platform || 'windows');
    this.open('dlgAbout');
  },
};
