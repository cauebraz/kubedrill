// Keyboard shortcuts. A screen registers its bindings with bind(); they are
// dropped when the route changes. Esc closes the topmost overlay, ? opens
// the list of shortcuts. Single keys never fire while the learner types in
// a field or the terminal; modifier chords (Ctrl or Cmd) always do.

import { el, isTyping, onLeave } from "./dom.js";

const bindings = [];
const overlays = [];

function chord(e) {
  const parts = [];
  if (e.ctrlKey || e.metaKey) { parts.push("mod"); }
  if (e.shiftKey && e.key.length > 1) { parts.push("shift"); }
  if (e.altKey) { parts.push("alt"); }
  parts.push(e.key.length === 1 ? e.key.toLowerCase() : e.key);
  return parts.join("+");
}

// bind registers keys (for example "1", "Enter", "mod+Enter") with a
// description for the help overlay; it lasts until the route changes or
// the returned function drops it.
export function bind(keys, description, fn) {
  const b = { keys: [].concat(keys), description, fn };
  bindings.push(b);
  const drop = () => {
    const i = bindings.indexOf(b);
    if (i >= 0) { bindings.splice(i, 1); }
  };
  onLeave(drop);
  return drop;
}

// pushOverlay registers an open drawer or modal; Esc closes the last one.
export function pushOverlay(close) {
  overlays.push(close);
  return () => {
    const i = overlays.indexOf(close);
    if (i >= 0) { overlays.splice(i, 1); }
  };
}

document.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && overlays.length) {
    e.preventDefault();
    overlays[overlays.length - 1]();
    return;
  }
  const k = chord(e);
  const modified = k.startsWith("mod+");
  if (!modified && isTyping(e.target)) { return; }
  if (k === "?" && !modified) {
    e.preventDefault();
    showHelp();
    return;
  }
  for (let i = bindings.length - 1; i >= 0; i--) {
    if (bindings[i].keys.includes(k)) {
      e.preventDefault();
      bindings[i].fn(e);
      return;
    }
  }
}, true);

function label(k) {
  return k.replace("mod+", navigator.platform.includes("Mac") ? "Cmd " : "Ctrl ").replace("shift+", "Shift ");
}

function showHelp() {
  const rows = [["?", "Show these shortcuts"], ["Esc", "Close the drawer or dialog"]];
  for (const b of bindings) {
    if (b.description) { rows.push([b.keys.map(label).join(" / "), b.description]); }
  }
  const back = el("div", { class: "modal-back", role: "dialog", "aria-modal": "true", "aria-label": "Keyboard shortcuts" });
  let release = () => {};
  const close = () => { release(); back.remove(); };
  release = pushOverlay(close);
  back.addEventListener("click", (e) => { if (e.target === back) { close(); } });
  back.appendChild(el("div", { class: "modal narrow" },
    el("div", { class: "eyebrow", text: "Keyboard" }),
    el("h2", { class: "modal-title", text: "Shortcuts on this screen" }),
    el("dl", { class: "shortcuts" }, rows.map(([k, d]) => [el("dt", null, el("kbd", { class: "kbd", text: k })), el("dd", { text: d })])),
    el("div", { class: "modal-actions" }, el("button", { class: "btn", text: "Close", onclick: close }))));
  document.body.appendChild(back);
  back.querySelector("button").focus();
}
