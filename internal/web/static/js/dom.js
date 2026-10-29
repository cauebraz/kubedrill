// DOM helpers shared by every screen: element builder, SVG builder, toast,
// per-browser storage, cleanup registry and the icon set.

export function el(tag, attrs, ...children) {
  const node = document.createElement(tag);
  setAttrs(node, attrs);
  append(node, children);
  return node;
}

export function svg(tag, attrs, ...children) {
  const node = document.createElementNS("http://www.w3.org/2000/svg", tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v === null || v === undefined || v === false) { continue; }
      if (k === "text") { node.textContent = v; } else { node.setAttribute(k, String(v)); }
    }
  }
  append(node, children);
  return node;
}

function setAttrs(node, attrs) {
  if (!attrs) { return; }
  for (const [k, v] of Object.entries(attrs)) {
    if (v === null || v === undefined || v === false) { continue; }
    if (k === "class") { node.className = v; }
    else if (k === "text") { node.textContent = v; }
    else if (k === "html") { node.innerHTML = v; }
    else if (k.startsWith("on") && typeof v === "function") { node.addEventListener(k.slice(2), v); }
    else { node.setAttribute(k, v === true ? "" : v); }
  }
}

function append(node, children) {
  for (const c of children.flat(Infinity)) {
    if (c === null || c === undefined || c === false) { continue; }
    node.appendChild(typeof c === "string" || typeof c === "number" ? document.createTextNode(String(c)) : c);
  }
}

export function clear(node, ...children) {
  node.textContent = "";
  append(node, children);
  return node;
}

let toastTimer = 0;
// toast shows msg for a few seconds; action ({ label, run }) adds a button
// that hides the toast and runs, and keeps the toast up for 10 seconds.
export function toast(msg, isError, action) {
  const t = document.getElementById("toast");
  t.textContent = msg;
  t.className = "toast" + (isError ? " error" : "") + (action ? " with-action" : "");
  if (action) {
    t.appendChild(el("button", { class: "toast-action", type: "button", text: action.label, onclick: () => { t.hidden = true; clearTimeout(toastTimer); action.run(); } }));
  }
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.hidden = true; }, action ? 10000 : isError ? 7000 : 3500);
}

// store reads (one argument) or writes (two) a JSON value in localStorage;
// null removes it. It never throws: a private window just forgets.
export function store(key, value) {
  try {
    if (arguments.length === 1) { return JSON.parse(localStorage.getItem(key) || "null"); }
    if (value === null) { localStorage.removeItem(key); } else { localStorage.setItem(key, JSON.stringify(value)); }
  } catch (e) { return null; }
  return value;
}

// Cleanups registered while a screen is shown run when the route changes.
const cleanups = [];
export function onLeave(fn) { cleanups.push(fn); }
export function leave() {
  while (cleanups.length) {
    try { cleanups.pop()(); } catch (e) { /* a cleanup never blocks the next screen */ }
  }
}

export function initials(name) {
  return (name || "?").split(/\s+/).map((w) => w[0]).join("").slice(0, 2).toUpperCase();
}

export function shuffledOrder(n, seed) {
  let h = 2166136261;
  for (let i = 0; i < seed.length; i++) { h = Math.imul(h ^ seed.charCodeAt(i), 16777619); }
  let a = h >>> 0;
  const next = () => {
    a = (a + 0x6D2B79F5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
  const out = Array.from({ length: n }, (_, i) => i);
  for (let i = n - 1; i > 0; i--) {
    const j = Math.floor(next() * (i + 1));
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}

export function plural(n, word) { return n + " " + word + (n === 1 ? "" : "s"); }

export function duration(seconds) {
  const s = Math.max(0, Math.round(seconds));
  const m = Math.floor(s / 60);
  return m >= 60 ? Math.floor(m / 60) + "h " + (m % 60) + "m" : m + ":" + String(s % 60).padStart(2, "0");
}

export const icons = {
  arrow: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M6 3.5 10.5 8 6 12.5"/></svg>',
  lock: '<svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" aria-hidden="true"><rect x="3.5" y="7" width="9" height="6.5" rx="1.5"/><path d="M5.5 7V5a2.5 2.5 0 0 1 5 0v2"/></svg>',
  search: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><circle cx="7" cy="7" r="4.5"/><path d="m10.5 10.5 3 3"/></svg>',
  play: '<svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor" aria-hidden="true"><path d="M4.5 3.2v9.6L12.5 8z"/></svg>',
  refresh: '<svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M13 8a5 5 0 1 1-1.5-3.6M13 2.5v3h-3"/></svg>',
  chevron: '<svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m4 6 4 4 4-4"/></svg>',
  star: '<svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor" aria-hidden="true"><path d="M12 2.5l2.9 6 6.6.8-4.9 4.5 1.3 6.5L12 17l-5.9 3.3 1.3-6.5-4.9-4.5 6.6-.8z"/></svg>',
  book: '<svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" aria-hidden="true"><path d="M2.5 3.5c2-1 4-1 5.5.5 1.5-1.5 3.5-1.5 5.5-.5v9c-2-1-4-1-5.5.5-1.5-1.5-3.5-1.5-5.5-.5z"/><path d="M8 4v9"/></svg>'
};

export function icon(name) { return el("span", { class: "ic", html: icons[name] }); }

// isTyping reports whether keyboard focus is in a field or the terminal,
// where single-key shortcuts must not fire.
export function isTyping(target) {
  const t = target || document.activeElement;
  if (!t) { return false; }
  if (t.closest && t.closest(".xterm")) { return true; }
  return t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable;
}
