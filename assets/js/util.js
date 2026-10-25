/* AllTag — small helpers shared by all modules */
'use strict';

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

function el(tag, cls, text) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text != null) n.textContent = text;
  return n;
}

/* ── formatting ─────────────────────────────────────────────────────── */

function fmtDuration(sec) {
  if (!sec || !isFinite(sec) || sec <= 0) return '—';
  const s = Math.round(sec);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const ss = String(s % 60).padStart(2, '0');
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${m}:${ss}`;
}

function fmtTotal(sec) {
  if (!sec || sec <= 0) return '0:00';
  const h = Math.floor(sec / 3600);
  const m = Math.round((sec % 3600) / 60);
  if (h > 0) return `${h} ч ${m} мин`;
  return `${m} мин`;
}

function fmtSize(bytes) {
  if (!bytes) return '';
  const units = ['Б', 'КБ', 'МБ', 'ГБ'];
  let i = 0, v = bytes;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

function fmtCount(n, one, few, many) {
  const a = Math.abs(n) % 100, b = n % 10;
  if (a > 10 && a < 20) return `${n} ${many}`;
  if (b === 1) return `${n} ${one}`;
  if (b >= 2 && b <= 4) return `${n} ${few}`;
  return `${n} ${many}`;
}

/* ── colours ────────────────────────────────────────────────────────── */

function hexToRgb(hex) {
  hex = String(hex || '').replace('#', '').trim();
  if (hex.length === 3) hex = hex.split('').map(c => c + c).join('');
  if (!/^[0-9a-fA-F]{6}$/.test(hex)) return { r: 208, g: 188, b: 255 };
  return {
    r: parseInt(hex.slice(0, 2), 16),
    g: parseInt(hex.slice(2, 4), 16),
    b: parseInt(hex.slice(4, 6), 16),
  };
}

const rgbToHex = (r, g, b) =>
  '#' + [r, g, b].map(v => Math.max(0, Math.min(255, Math.round(v)))
    .toString(16).padStart(2, '0')).join('');

function mix(a, b, t) {
  const A = hexToRgb(a), B = hexToRgb(b);
  return rgbToHex(A.r + (B.r - A.r) * t, A.g + (B.g - A.g) * t, A.b + (B.b - A.b) * t);
}

function luminance(hex) {
  const { r, g, b } = hexToRgb(hex);
  const f = c => { c /= 255; return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4); };
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
}

/* Lighten / darken keeping a pleasant hue, roughly matching MD3 tonal steps. */
function tone(hex, amount) {
  return amount >= 0 ? mix(hex, '#ffffff', amount) : mix(hex, '#000000', -amount);
}

/** Applies an accent colour to every MD3 token in the document. */
function applyAccent(hex) {
  const root = document.documentElement;
  const { r, g, b } = hexToRgb(hex);
  const dark = luminance(hex) > 0.55;
  root.style.setProperty('--md-primary', hex);
  root.style.setProperty('--md-on-primary', dark ? '#1A1A1A' : '#FFFFFF');
  root.style.setProperty('--md-primary-dim', tone(hex, -0.28));
  root.style.setProperty('--md-primary-container', mix(hex, '#121212', 0.74));
  root.style.setProperty('--md-on-primary-container', tone(hex, 0.24));
  root.style.setProperty('--md-primary-rgb', `${r}, ${g}, ${b}`);
  $$('.swatch[data-accent]').forEach(s =>
    s.classList.toggle('active',
      s.dataset.accent.toLowerCase() === String(hex).toLowerCase()));
  const picker = $('#accentPicker');
  if (picker && picker.value.toLowerCase() !== hex.toLowerCase()) picker.value = hex;
}

/* ── ui feedback ────────────────────────────────────────────────────── */

let snackTimer = null;

function snack(message, kind = 'info', actionLabel, action) {
  const box = $('#snackbar');
  const txt = $('#snackText');
  const btn = $('#snackAction');
  txt.textContent = message;
  box.classList.toggle('error', kind === 'error');
  box.hidden = false;
  if (actionLabel && action) {
    btn.hidden = false;
    btn.textContent = actionLabel;
    btn.onclick = () => { hideSnack(); action(); };
  } else {
    btn.hidden = true;
    btn.onclick = null;
  }
  requestAnimationFrame(() => box.classList.add('show'));
  clearTimeout(snackTimer);
  snackTimer = setTimeout(hideSnack, kind === 'error' ? 6000 : 3600);
}

function hideSnack() {
  const box = $('#snackbar');
  box.classList.remove('show');
  clearTimeout(snackTimer);
  snackTimer = setTimeout(() => { box.hidden = true; }, 220);
}

/** Material ripple on any .btn / .icon-btn */
function installRipples() {
  document.addEventListener('pointerdown', ev => {
    const target = ev.target.closest('.btn, .icon-btn, .swatch, .seg');
    if (!target || target.disabled) return;
    const rect = target.getBoundingClientRect();
    const size = Math.max(rect.width, rect.height);
    const rip = el('span', 'ripple');
    rip.style.width = rip.style.height = size + 'px';
    rip.style.left = (ev.clientX - rect.left - size / 2) + 'px';
    rip.style.top = (ev.clientY - rect.top - size / 2) + 'px';
    target.appendChild(rip);
    setTimeout(() => rip.remove(), 580);
  });
}

function debounce(fn, ms) {
  let t;
  return (...args) => { clearTimeout(t); t = setTimeout(() => fn(...args), ms); };
}

function escapeHtml(s) {
  return String(s == null ? '' : s)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

const sleep = ms => new Promise(r => setTimeout(r, ms));
