/* AllTag — JSON API client */
'use strict';

const API = {
  async call(method, body) {
    let res;
    try {
      res = await fetch('/api/' + method, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body || {}),
      });
    } catch (err) {
      const e = new Error('Нет связи с движком AllTag');
      e.cause = err;
      throw e;
    }
    let data = null;
    const text = await res.text();
    if (text) {
      try { data = JSON.parse(text); } catch (_) { data = null; }
    }
    if (!res.ok) {
      const msg = (data && data.error) || `Ошибка ${res.status}`;
      throw new Error(msg);
    }
    return data || {};
  },

  ping() { return this.call('ping'); },
  tracks() { return this.call('tracks'); },
  scan(paths, append) { return this.call('scan', { paths, append: !!append }); },
  pickFolder(append) { return this.call('pickFolder', { append: !!append }); },
  clear() { return this.call('clear'); },
  merge(ids) { return this.call('merge', { ids }); },
  apply(payload) { return this.call('apply', payload); },
  extended(id) { return this.call('extended', { id }); },
  extendedSave(ids, set, remove) { return this.call('extendedSave', { ids, set, remove }); },
  artwork(id) { return this.call('artwork', { id }); },
  renamePreview(ids, pattern) { return this.call('renamePreview', { ids, pattern }); },
  renameApply(previews) { return this.call('renameApply', { previews }); },
  parseNames(ids, pattern) { return this.call('parseNames', { ids, pattern }); },
  parsePreview(ids, pattern) { return this.call('parsePreview', { ids, pattern }); },
  contextMenu(enable) { return this.call('contextMenu', enable === undefined ? {} : { enable }); },
  settings(settings) { return this.call('settings', settings ? { settings } : {}); },
  reveal(id) { return this.call('reveal', { id }); },
  quit() { return this.call('quit'); },
};

/* Server-sent events keep every window (and the fallback browser) in sync. */
function subscribeEvents(onUpdate) {
  try {
    const es = new EventSource('/api/events');
    es.addEventListener('update', ev => {
      try {
        const msg = JSON.parse(ev.data);
        onUpdate(msg);
      } catch (_) { /* ignore malformed frames */ }
    });
    es.onerror = () => { /* the browser retries automatically */ };
  } catch (_) { /* EventSource unavailable */ }
}
