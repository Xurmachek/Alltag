/* AllTag — left editing panel: batch-aware fields + album art */
'use strict';

const FIELD_KEYS = ['TITLE', 'ARTIST', 'ALBUM', 'YEAR', 'TRACK', 'GENRE', 'COMMENT',
  'ALBUMARTIST', 'COMPOSER', 'DISCNUMBER', 'BPM'];

const Editor = {
  ids: [],
  activeId: null,
  merged: {},
  dirty: new Map(),
  art: { action: null, data: null, mime: null },
  currentArtURL: null,
  hasSelection: false,

  init(onSaved) {
    this.onSaved = onSaved;
    for (const key of FIELD_KEYS) {
      const input = $('#f_' + key);
      if (!input) continue;
      input.addEventListener('input', () => {
        const v = input.value;
        if (v === input.dataset.original) this.setDirty(key, null);
        else this.setDirty(key, v);
        const field = input.closest('.field');
        field.classList.toggle('dirty', this.dirty.has(key));
        document.dispatchEvent(new Event('editor:dirty'));
      });
      input.addEventListener('keydown', e => {
        if (e.key === 'Delete' && (e.ctrlKey || !input.value)) {
          input.value = '';
          input.dispatchEvent(new Event('input'));
        }
        if (e.key === 'Enter') { e.preventDefault(); this.save(); }
      });
    }
    $('#btnSaveFields').addEventListener('click', () => this.save());
    $('#btnRevert').addEventListener('click', () => this.reload(this.ids, this.activeId, true));

    $('#btnArtLoad').addEventListener('click', () => $('#artInput').click());
    $('#artInput').addEventListener('change', e => this.pickArt(e.target.files));
    $('#btnArtRemove').addEventListener('click', () => this.removeArt());
    $('#btnArtExtract').addEventListener('click', () => this.extractArt());

    document.addEventListener('editor:dirty', () => this.updateButtons());
  },

  /* ── selection → fields ─────────────────────────────────────────── */

  async load(ids, activeId) {
    this.ids = ids || [];
    this.activeId = activeId != null ? activeId : (this.ids[0] ?? null);
    this.dirty.clear();
    this.art = { action: null, data: null, mime: null };
    this.hasSelection = this.ids.length > 0;
    this.setDisabled(!this.hasSelection);

    if (!this.hasSelection) {
      this.fillEmpty();
      this.showArt(null);
      this.updateButtons();
      this.setHint('');
      return;
    }

    try {
      const res = await API.merge(this.ids);
      this.merged = res.fields || {};
      for (const key of FIELD_KEYS) {
        const input = $('#f_' + key);
        if (!input) continue;
        const f = this.merged[key] || { value: '', mixed: false };
        const field = input.closest('.field');
        field.classList.remove('dirty');
        field.classList.toggle('mixed', !!f.mixed);
        input.dataset.original = f.mixed ? '__mixed__' : (f.value || '');
        input.value = f.mixed ? '' : (f.value || '');
        input.placeholder = f.mixed ? '< оставить без изменений >' : ' ';
        input.title = f.mixed ? 'Значения различаются — введите новое, чтобы применить ко всем выбранным файлам' : '';
      }
      const sel = this.ids.length;
      this.setHint(sel > 1 ? `Правки применятся к ${fmtCount(sel, 'файлу', 'файлам', 'файлам')}` : '');
      this.refreshArt();
    } catch (err) {
      snack(err.message, 'error');
    }
    this.updateButtons();
  },

  fillEmpty() {
    for (const key of FIELD_KEYS) {
      const input = $('#f_' + key);
      if (!input) continue;
      const field = input.closest('.field');
      field.classList.remove('mixed', 'dirty');
      input.value = '';
      input.dataset.original = '';
      input.placeholder = ' ';
    }
  },

  setDisabled(disabled) {
    for (const key of FIELD_KEYS) {
      const input = $('#f_' + key);
      if (input) input.disabled = disabled;
    }
    $('#btnArtLoad').disabled = disabled;
    $('#btnArtRemove').disabled = disabled;
    $('#btnArtExtract').disabled = disabled || !this.activeHasArt();
    $('#btnExtended').classList.toggle('dim', !this.hasSelection);
  },

  setHint(text) {
    const hint = $('#editorHint');
    if (hint) hint.textContent = text;
    const info = $('#listInfo');
    if (info && text) { /* info stays owned by the table */ }
  },

  activeHasArt() {
    const r = Table.activeRow();
    return !!(r && r.hasArt);
  },

  /* ── dirty state ────────────────────────────────────────────────── */

  setDirty(key, value) {
    if (value === null || value === undefined) this.dirty.delete(key);
    else this.dirty.set(key, value);
    this.updateButtons();
  },

  updateButtons() {
    const n = this.dirty.size + (this.art.action ? 1 : 0);
    const canSave = this.hasSelection && n > 0;
    $('#btnSave').disabled = !canSave;
    $('#btnSaveFields').disabled = !canSave;
    $('#btnRevert').disabled = !canSave;
    const badge = $('#dirtyCount');
    badge.hidden = !n;
    badge.textContent = n;
    $('#btnArtExtract').disabled = !this.hasSelection || !this.currentArtURL;
  },

  revert() {
    this.reload(this.ids, this.activeId, true);
  },

  async reload(ids, activeId, force) {
    if (this.dirty.size && !force) return;
    await this.load(ids, activeId);
  },

  /* ── artwork ────────────────────────────────────────────────────── */

  async refreshArt() {
    this.currentArtURL = null;
    const row = Table.activeRow();
    if (!row) { this.showArt(null); return; }
    if (!row.hasArt) { this.showArt(null); return; }
    try {
      const res = await API.artwork(row.id);
      this.currentArtURL = res.data;
      this.showArt(res.data);
    } catch (_) {
      this.showArt(null);
    }
  },

  showArt(dataURL) {
    const img = $('#artImage');
    const empty = $('#artEmpty');
    const badge = $('#artBadge');
    if (dataURL) {
      img.src = dataURL;
      img.hidden = false;
      empty.hidden = true;
      badge.hidden = false;
      badge.textContent = 'обложка';
    } else {
      img.removeAttribute('src');
      img.hidden = true;
      empty.hidden = false;
      badge.hidden = true;
    }
    this.updateButtons();
  },

  pickArt(files) {
    if (!files || !files.length) return;
    const file = files[0];
    if (!/^image\//.test(file.type)) {
      snack('Поддерживаются только изображения JPEG и PNG', 'error');
      return;
    }
    const reader = new FileReader();
    reader.onload = () => {
      this.art = { action: 'set', data: String(reader.result), mime: file.type };
      this.showArt(String(reader.result));
      this.updateButtons();
      snack('Обложка будет записана при сохранении');
    };
    reader.readAsDataURL(file);
    $('#artInput').value = '';
  },

  removeArt() {
    this.art = { action: 'remove', data: null, mime: null };
    this.showArt(null);
    this.updateButtons();
    snack('Обложка будет удалена при сохранении');
  },

  async extractArt() {
    const row = Table.activeRow();
    if (!row) return;
    try {
      const res = await API.artwork(row.id);
      const a = document.createElement('a');
      const ext = res.mime.includes('png') ? 'png' : 'jpg';
      a.href = res.data;
      a.download = row.name.replace(/\.[^.]+$/, '') + ' - cover.' + ext;
      document.body.appendChild(a);
      a.click();
      a.remove();
    } catch (err) {
      snack('Не удалось извлечь обложку: ' + err.message, 'error');
    }
  },

  /* ── saving ─────────────────────────────────────────────────────── */

  async save() {
    if (!this.hasSelection) return;
    if (!this.dirty.size && !this.art.action) {
      snack('Нет изменений для сохранения');
      return;
    }
    const set = {};
    for (const [k, v] of this.dirty) set[k] = v.trim();
    const payload = {
      ids: this.ids,
      set,
      pictureAction: this.art.action || '',
      pictureData: this.art.data,
      pictureMime: this.art.mime,
      removeAllArt: this.art.action === 'remove',
    };
    const btn = $('#btnSaveFields');
    btn.disabled = true;
    try {
      const res = await API.apply(payload);
      const errors = res.errors || [];
      if (errors.length) {
        snack(`Сохранено ${res.saved}, ошибок: ${errors.length}` +
          (errors[0] ? ' — ' + errors[0] : ''), 'error');
      } else {
        snack(`Теги сохранены · ${fmtCount(res.saved, 'файл', 'файла', 'файлов')}`);
      }
      if (res.tracks) App.setTracks(res.tracks, { keepSelection: true });
      this.dirty.clear();
      this.art = { action: null, data: null, mime: null };
      $$('.field').forEach(f => f.classList.remove('dirty'));
      await this.load(this.ids, this.activeId);
      if (this.onSaved) this.onSaved();
    } catch (err) {
      snack(err.message, 'error');
    }
    this.updateButtons();
  },
};
