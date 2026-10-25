/* AllTag — application shell: wiring, settings, shortcuts, menus */
'use strict';

const App = {
  version: '1.0.0',
  platform: 'windows',
  browserMode: false,
  settings: {
    accent: '#D0BCFF',
    lastFolder: '',
    sortColumn: 'name',
    sortAsc: true,
    renameMask: '%track% - %artist% - %title%',
    windowState: '',
  },
  tracks: [],
  stats: { count: 0, duration: 0, size: 0, formats: 0 },
  folders: [],
  loading: 0,

  /* ── boot ───────────────────────────────────────────────────────── */

  async init() {
    installRipples();

    Table.init({
      onChange: (ids, activeId) => this.onSelectionChange(ids, activeId),
      onActivate: () => Editor.save(),
      onContext: (id, x, y) => this.openRowMenu(x, y),
    });
    Editor.init(() => this.updateStatus());

    this.wireToolbar();
    this.wireMenus();
    this.wireAccents();
    this.wireShortcuts();
    this.wireDragDrop();
    this.loadLocalSettings();

    try {
      const ping = await API.ping();
      if (ping.version) this.version = ping.version;
      if (ping.platform) this.platform = ping.platform;
      this.browserMode = !!ping.browserMode;
      if (this.browserMode) {
        $('#brandSub').textContent = 'режим браузера';
        snack('WebView2 недоступен — интерфейс открыт в системном браузере', 'info');
      }
    } catch (err) {
      snack(err.message, 'error');
    }

    await this.refresh(true);
    Dialogs.init();
    this.updateStatus();

    subscribeEvents(msg => {
      if (msg.type === 'tracks' && msg.data && msg.data.tracks) {
        this.setTracks(msg.data.tracks, { keepSelection: true, remote: true });
      }
    });

    // keep-alive so the native host knows the UI is still alive
    setInterval(() => { API.ping().catch(() => {}); }, 9000);
  },

  /* ── data ───────────────────────────────────────────────────────── */

  async refresh(resetSelection) {
    try {
      const res = await API.tracks();
      this.setTracks(res.tracks || [], { keepSelection: !resetSelection });
    } catch (err) {
      snack(err.message, 'error');
    }
  },

  setTracks(tracks, opts = {}) {
    this.tracks = tracks || [];
    const selectedBefore = opts.keepSelection ? Table.selectedIds() : [];
    Table.setRows(this.tracks, { resetSelection: !opts.keepSelection });
    if (opts.keepSelection && selectedBefore.length) {
      const alive = selectedBefore.filter(id => Table.byId.has(id));
      if (alive.length) Table.selectIds(alive);
      else if (Table.view.length) Table.selectOne(Table.view[0].id);
      else { Editor.load([], null); }
    } else if (Table.view.length && !Table.selectedIds().length) {
      Table.selectOne(Table.view[0].id);
    } else if (!Table.view.length) {
      Table.clearSelection();
    }
    this.recomputeStats();
    this.updateStatus();
  },

  recomputeStats() {
    let dur = 0, size = 0;
    const formats = new Set();
    for (const t of this.tracks) {
      dur += t.duration || 0;
      size += t.size || 0;
      if (t.format) formats.add(t.format);
    }
    this.stats = { count: this.tracks.length, duration: dur, size, formats: formats.size };
  },

  onSelectionChange(ids, activeId) {
    Editor.load(ids, activeId);
    this.updateStatus();
  },

  updateStatus() {
    const n = this.tracks.length;
    $('#statCount').textContent = fmtCount(n, 'файл', 'файла', 'файлов');
    const sel = Table.selectedIds().length;
    $('#statSel').textContent = sel > 1
      ? `выбрано ${fmtCount(sel, 'файл', 'файла', 'файлов')}`
      : (sel === 1 ? 'выбран 1 файл' : 'ничего не выбрано');
    $('#statTotal').textContent = this.stats.duration
      ? `${fmtTotal(this.stats.duration)} · ${fmtSize(this.stats.size)}`
      : '';
    const folder = this.folders.length
      ? (this.folders.length === 1 ? this.folders[0] : `${this.folders.length} папки`) 
      : (this.settings.lastFolder || '');
    $('#statFolder').textContent = folder;
    $('#statFolder').title = this.folders.join('\n') || folder;
    $('#listInfo').textContent = Table.filter
      ? `Найдено ${Table.view.length} из ${n}`
      : (n ? fmtCount(n, 'файл', 'файла', 'файлов') : '');
  },

  busy(on) {
    this.loading = Math.max(0, this.loading + (on ? 1 : -1));
    document.body.classList.toggle('busy', this.loading > 0);
  },

  /* ── folders ────────────────────────────────────────────────────── */

  async pickFolder(append) {
    this.busy(true);
    try {
      const res = await API.pickFolder(append);
      if (res.cancelled) return;
      this.folders = res.folders || [];
      this.setTracks(res.tracks || [], { keepSelection: append });
      if (!append) {
        this.settings.lastFolder = res.path || '';
        saveSettings();
      }
      const added = res.added || 0;
      snack(added
        ? `Добавлено ${fmtCount(added, 'трек', 'трека', 'треков')}`
        : 'В выбранной папке не найдено аудиофайлов');
    } catch (err) {
      snack(err.message, 'error');
    } finally {
      this.busy(false);
    }
  },

  async reload() {
    if (!this.folders.length) {
      if (this.settings.lastFolder) return this.pickFolder(false);
      snack('Нет открытой папки');
      return;
    }
    this.busy(true);
    try {
      const res = await API.scan(this.folders, false);
      this.setTracks(res.tracks || [], { keepSelection: true });
      this.folders = res.folders || this.folders;
      snack('Список обновлён');
    } catch (err) {
      snack(err.message, 'error');
    } finally {
      this.busy(false);
    }
  },

  /* ── toolbar & menus ────────────────────────────────────────────── */

  wireToolbar() {
    $('#btnPick').addEventListener('click', () => this.pickFolder(false));
    $('#btnPickEmpty').addEventListener('click', () => this.pickFolder(false));
    $('#btnAdd').addEventListener('click', () => this.pickFolder(true));
    $('#btnReload').addEventListener('click', () => this.reload());
    $('#btnSave').addEventListener('click', () => Editor.save());
    $('#btnRenamer').addEventListener('click', () => Dialogs.openRenamer());
    $('#btnExtended').addEventListener('click', () => Dialogs.openExtended());

    $('#search').addEventListener('input', debounce(e => {
      Table.setFilter(e.target.value);
      this.updateStatus();
    }, 120));

    $('#ctxSwitch').addEventListener('change', async e => {
      const want = e.target.checked;
      try {
        const res = await API.contextMenu(want);
        e.target.checked = !!res.enabled;
        snack(res.enabled
          ? 'Пункт «Открыть в AllTag» добавлен в контекстное меню проводника'
          : 'Интеграция с контекстным меню отключена');
      } catch (err) {
        e.target.checked = !want;
        snack(err.message, 'error');
      }
    });
    API.contextMenu().then(res => { $('#ctxSwitch').checked = !!res.enabled; }).catch(() => {});
  },

  wireMenus() {
    const menu = $('#appMenu');
    $('#btnMenu').addEventListener('click', e => {
      e.stopPropagation();
      const r = e.currentTarget.getBoundingClientRect();
      menu.hidden = false;
      menu.style.left = Math.round(Math.min(r.left, window.innerWidth - 250)) + 'px';
      menu.style.top = Math.round(r.bottom + 6) + 'px';
    });
    menu.addEventListener('click', e => {
      const btn = e.target.closest('button[data-action]');
      if (!btn) return;
      menu.hidden = true;
      this.menuAction(btn.dataset.action);
    });

    document.addEventListener('pointerdown', e => {
      if (!e.target.closest('.menu, #appMenu, #btnMenu')) $('#appMenu').hidden = true;
      if (!e.target.closest('.rowmenu, #rowMenu')) $('#rowMenu').hidden = true;
    });

    $('#rowMenu').addEventListener('click', e => {
      const btn = e.target.closest('button[data-action]');
      if (!btn) return;
      $('#rowMenu').hidden = true;
      this.menuAction(btn.dataset.action);
    });

    document.addEventListener('editor:dirty', () => this.updateStatus());
  },

  openRowMenu(x, y) {
    const menu = $('#rowMenu');
    menu.hidden = false;
    const w = menu.offsetWidth || 240, h = menu.offsetHeight || 180;
    menu.style.left = Math.round(Math.min(x, window.innerWidth - w - 8)) + 'px';
    menu.style.top = Math.round(Math.min(y, window.innerHeight - h - 8)) + 'px';
  },

  async menuAction(action) {
    const row = Table.activeRow();
    switch (action) {
      case 'keys': Dialogs.open('dlgKeys'); break;
      case 'about': Dialogs.openAbout(); break;
      case 'extended': Dialogs.openExtended(); break;
      case 'rename': Dialogs.openRenamer(); break;
      case 'reveal':
        if (row) API.reveal(row.id).catch(err => snack(err.message, 'error'));
        else if (this.folders.length) snack('Выберите файл в списке');
        break;
      case 'copy':
        if (row) {
          navigator.clipboard?.writeText(row.name)
            .then(() => snack('Имя файла скопировано'))
            .catch(() => snack('Не удалось скопировать', 'error'));
        }
        break;
      case 'quit':
        API.quit().catch(() => {});
        snack('AllTag закрывается…');
        break;
    }
  },

  wireAccents() {
    $$('#accentGroup .swatch[data-accent]').forEach(sw => {
      sw.addEventListener('click', () => {
        const hex = sw.dataset.accent;
        applyAccent(hex);
        this.settings.accent = hex;
        saveSettings();
      });
    });
    $('#accentPicker').addEventListener('input', e => {
      applyAccent(e.target.value);
      this.settings.accent = e.target.value;
    });
    $('#accentPicker').addEventListener('change', () => saveSettings());
  },

  wireShortcuts() {
    document.addEventListener('keydown', e => {
      const inField = /^(INPUT|TEXTAREA|SELECT)$/.test(e.target.tagName) ||
        e.target.isContentEditable;
      const dlgOpen = $$('.dialog').some(d => !d.hidden);

      if (e.ctrlKey || e.metaKey) {
        switch (e.key.toLowerCase()) {
          case 'o':
            e.preventDefault(); this.pickFolder(false); return;
          case 's':
            e.preventDefault(); Editor.save(); return;
          case 'e':
            e.preventDefault(); Dialogs.openExtended(); return;
          case 'r':
            e.preventDefault(); Dialogs.openRenamer(); return;
          case 'a':
            if (!inField) { e.preventDefault(); Table.selectAll(); }
            return;
          case 'f':
            e.preventDefault(); $('#search').focus(); $('#search').select(); return;
        }
      }
      if (dlgOpen) return;
      if (e.key === 'F5') { e.preventDefault(); this.reload(); return; }
      if (e.key === 'Escape') {
        if (!$('#appMenu').hidden || !$('#rowMenu').hidden) {
          $('#appMenu').hidden = true;
          $('#rowMenu').hidden = true;
          return;
        }
        $('#search').value = '';
        Table.setFilter('');
        this.updateStatus();
      }
    });
  },

  wireDragDrop() {
    /* WebView2 never exposes dropped file paths to the page, so dropping into
       the window can only be an invitation to use the native picker. */
    const app = $('#app');
    let warned = false;
    const stop = e => { e.preventDefault(); e.stopPropagation(); };
    app.addEventListener('dragenter', e => {
      stop(e);
      document.body.classList.add('dragging');
    });
    app.addEventListener('dragover', e => {
      stop(e);
      if (e.dataTransfer) e.dataTransfer.dropEffect = 'none';
    });
    app.addEventListener('dragleave', e => {
      stop(e);
      document.body.classList.remove('dragging');
    });
    app.addEventListener('drop', e => {
      stop(e);
      document.body.classList.remove('dragging');
      if (warned) return;
      warned = true;
      snack('Окно не принимает перетаскивание — перетащите папку на значок AllTag ' +
        'или используйте «Выбрать папку»', 'info');
      setTimeout(() => { warned = false; }, 6000);
    });
  },

  /* ── settings persistence (localStorage mirror) ─────────────────── */

  loadLocalSettings() {
    let stored = {};
    try { stored = JSON.parse(localStorage.getItem('alltag.settings') || '{}'); } catch (_) {}
    this.settings = Object.assign(this.settings, stored);
    applyAccent(this.settings.accent || '#D0BCFF');
    if (this.settings.sortColumn) {
      Table.sortKey = this.settings.sortColumn;
      Table.sortAsc = this.settings.sortAsc !== false;
      Table.markSort();
    }
    if (this.settings.renameMask) $('#renMask').value = this.settings.renameMask;
  },
};

function saveSettings() {
  App.settings.sortColumn = Table.sortKey;
  App.settings.sortAsc = Table.sortAsc;
  try { localStorage.setItem('alltag.settings', JSON.stringify(App.settings)); } catch (_) {}
  clearTimeout(saveSettings._t);
  saveSettings._t = setTimeout(() => {
    API.settings({
      accent: App.settings.accent,
      sortColumn: App.settings.sortColumn,
      sortAsc: App.settings.sortAsc,
      renameMask: App.settings.renameMask,
      lastFolder: App.settings.lastFolder,
    }).catch(() => {});
  }, 500);
}

window.addEventListener('DOMContentLoaded', () => {
  App.init().catch(err => {
    console.error(err);
    snack('Ошибка инициализации: ' + err.message, 'error');
  });
});
