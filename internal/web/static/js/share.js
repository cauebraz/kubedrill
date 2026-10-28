// The share card of an earned or mastered badge: a 1200x627 canvas drawn
// with 2D primitives from the badge data (no external font or image, so the
// page's Content-Security-Policy holds), a name kept in this browser, a PNG
// download and a one-line post. Nothing is hosted, so the card carries no
// verification link: it says self-reported, local.

import { el, store, toast } from "./dom.js";
import { pushOverlay } from "./keys.js";
import { SHAPES, KIND_WORDS, arcPath, colorVar, allBadges, isEarned } from "./badge.js";

export const CARD_W = 1200;
export const CARD_H = 627;
const NAME_KEY = "kd:share-name";
const MARK_RING = ["M17.91 5.17A11 11 0 0 1 25.71 10.84", "M26.89 14.47A11 11 0 0 1 23.91 23.64", "M20.82 25.89A11 11 0 0 1 11.18 25.89", "M8.09 23.64A11 11 0 0 1 5.11 14.47"];
const MARK_STEP = "M6.29 10.84A11 11 0 0 1 14.09 5.17";
const SITE = "github.com/cauebraz/kubedrill";

// cardTitle names the badge on the card: the module or level code and
// title, or the exam id and the word track.
function cardTitle(x) {
  return x.kind === "track" ? x.exam.id + " track" : x.badge.code + " " + x.title;
}

function assignmentsIn(x) {
  if (x.kind === "module") { return x.module.assignments.length; }
  if (x.kind === "level") { return x.levelObj.modules.reduce((n, m) => n + m.assignments.length, 0); }
  return 0;
}

// cardFacts are the numbers and sentences the card shows, from the data.
export function cardFacts(x, c, now) {
  const b = x.badge;
  const mastered = b.state === "mastered";
  const n = assignmentsIn(x);
  const incidents = n + " Kubernetes " + (n === 1 ? "incident" : "incidents");
  let line;
  if (x.kind === "track") {
    line = b.caption + ": no guide, no hint, within the expected time. A kubedrill practice track, not a certification.";
  } else if (mastered) {
    line = incidents + ", each mastered: 3 clean solves in the last 4 attempts, on distinct variants where it has them, across 2 sessions.";
  } else {
    line = incidents + ", each solved clean: no guide, no hint, within the expected time.";
  }
  const when = b.earnedAt ? new Date(b.earnedAt) : now;
  return {
    verb: mastered ? "mastered" : "earned",
    title: cardTitle(x),
    line,
    stats: [
      [String(b.variants || 0), (b.variants === 1 ? "variant" : "variants") + " solved clean"],
      [String(allBadges(c).filter((y) => y.kind === "module" && isEarned(y.badge)).length), "module badges"],
      [when.toLocaleDateString("en-US", { year: "numeric", month: "short", day: "numeric" }), b.earnedAt ? "earned" : "shared"]
    ],
    post: "I " + (mastered ? "mastered" : "earned") + " the kubedrill " + cardTitle(x) + " badge: " + b.caption.charAt(0).toLowerCase() + b.caption.slice(1) +
      " (self-reported, local" + (x.kind === "track" ? "; not a certification" : "") + "). " + SITE
  };
}

function wrap(ctx, text, width) {
  const lines = [];
  let cur = "";
  for (const w of text.split(/\s+/)) {
    const next = cur ? cur + " " + w : w;
    if (cur && ctx.measureText(next).width > width) { lines.push(cur); cur = w; } else { cur = next; }
  }
  if (cur) { lines.push(cur); }
  return lines;
}

function roundRect(ctx, x, y, w, h, r) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

// drawCard draws the card of badge x into canvas at 1200x627, with the
// colors of the dark token set the .share-card class carries in both
// themes.
export function drawCard(canvas, x, c, name, now) {
  canvas.width = CARD_W;
  canvas.height = CARD_H;
  const css = getComputedStyle(canvas);
  const tok = (n) => css.getPropertyValue(n).trim();
  const sans = tok("--font-sans");
  const mono = tok("--font-mono");
  const color = tok(colorVar(x.kind, x.badge.code, x.level)) || tok("--brand");
  const f = cardFacts(x, c, now || new Date());
  const ctx = canvas.getContext("2d");
  ctx.fillStyle = tok("--bg");
  ctx.fillRect(0, 0, CARD_W, CARD_H);

  const box = 380;
  const bx = 64;
  const by = (CARD_H - box) / 2;
  roundRect(ctx, bx + 0.5, by + 0.5, box - 1, box - 1, 24);
  ctx.fillStyle = tok("--bg-1");
  ctx.fill();
  ctx.strokeStyle = tok("--line");
  ctx.lineWidth = 1;
  ctx.stroke();

  ctx.save();
  ctx.translate(bx + 40, by + 40);
  ctx.scale(2.5, 2.5);
  ctx.lineJoin = "round";
  const shape = new Path2D(SHAPES[x.kind]);
  ctx.strokeStyle = color;
  if (x.badge.state === "mastered") {
    ctx.globalAlpha = 0.22;
    ctx.lineWidth = 10;
    ctx.stroke(shape);
  }
  ctx.globalAlpha = x.badge.state === "mastered" ? 0.24 : 0.16;
  ctx.fillStyle = color;
  ctx.fill(shape);
  ctx.globalAlpha = 1;
  ctx.lineWidth = 2;
  ctx.stroke(shape);
  ctx.lineWidth = 6;
  for (let i = 0; i < 5; i++) { ctx.stroke(new Path2D(arcPath(i))); }
  ctx.fillStyle = tok("--fg");
  ctx.textAlign = "center";
  ctx.textBaseline = "alphabetic";
  ctx.font = "700 " + (x.kind === "level" ? 18 : 15) + "px " + mono;
  ctx.fillText(x.badge.code, 60, x.kind === "level" ? 67 : 66);
  ctx.restore();

  const left = bx + box + 56;
  const width = CARD_W - 64 - left;
  ctx.textAlign = "left";
  ctx.textBaseline = "top";
  let titleSize = 56;
  ctx.font = "700 " + titleSize + "px " + sans;
  while (titleSize > 34 && ctx.measureText(f.title).width > width) {
    titleSize -= 2;
    ctx.font = "700 " + titleSize + "px " + sans;
  }
  ctx.font = "400 18px " + sans;
  const lines = wrap(ctx, f.line, width).slice(0, 3);
  const height = 32 + 22 + 25 + 10 + titleSize * 1.1 + 10 + lines.length * 27 + 22 + 76 + 22 + 16;
  let y = Math.max(40, (CARD_H - height) / 2);

  ctx.save();
  ctx.translate(left, y);
  ctx.lineWidth = 4.5;
  ctx.strokeStyle = tok("--fg");
  for (const d of MARK_RING) { ctx.stroke(new Path2D(d)); }
  ctx.strokeStyle = tok("--brand");
  ctx.stroke(new Path2D(MARK_STEP));
  ctx.fillStyle = tok("--brand");
  ctx.beginPath();
  ctx.arc(16, 16, 2.5, 0, Math.PI * 2);
  ctx.fill();
  ctx.restore();
  ctx.fillStyle = tok("--fg");
  ctx.font = "600 20px " + sans;
  ctx.textBaseline = "middle";
  ctx.fillText("kubedrill", left + 44, y + 16);
  ctx.font = "500 13px " + mono;
  ctx.textAlign = "right";
  ctx.fillStyle = color;
  if ("letterSpacing" in ctx) { ctx.letterSpacing = "1px"; }
  ctx.fillText(f.verb.toUpperCase() + " / " + KIND_WORDS[x.kind].toUpperCase(), left + width, y + 16);
  if ("letterSpacing" in ctx) { ctx.letterSpacing = "0px"; }
  ctx.textAlign = "left";
  ctx.textBaseline = "top";
  y += 32 + 22;

  ctx.fillStyle = tok("--fg-1");
  ctx.font = "400 18px " + sans;
  const who = (name || "").trim();
  ctx.fillText(who ? who + " " + f.verb : f.verb.charAt(0).toUpperCase() + f.verb.slice(1), left, y);
  y += 25 + 10;
  ctx.fillStyle = tok("--fg");
  ctx.font = "700 " + titleSize + "px " + sans;
  ctx.fillText(f.title, left, y);
  y += titleSize * 1.1 + 10;
  ctx.fillStyle = tok("--fg-1");
  ctx.font = "400 18px " + sans;
  for (const l of lines) { ctx.fillText(l, left, y + 2); y += 27; }
  y += 22;

  const gap = 12;
  const cw = (width - 2 * gap) / 3;
  f.stats.forEach(([value, label], i) => {
    const cx = left + i * (cw + gap);
    roundRect(ctx, cx + 0.5, y + 0.5, cw - 1, 75, 10);
    ctx.fillStyle = tok("--bg-2");
    ctx.fill();
    ctx.strokeStyle = tok("--line");
    ctx.lineWidth = 1;
    ctx.stroke();
    ctx.fillStyle = tok("--fg");
    let size = 24;
    ctx.font = "700 " + size + "px " + mono;
    while (size > 14 && ctx.measureText(value).width > cw - 32) { size -= 1; ctx.font = "700 " + size + "px " + mono; }
    ctx.fillText(value, cx + 16, y + 14);
    ctx.fillStyle = tok("--fg-2");
    ctx.font = "400 13px " + sans;
    ctx.fillText(label, cx + 16, y + 48);
  });
  y += 76 + 22;

  ctx.fillStyle = tok("--fg-2");
  ctx.font = "400 13px " + mono;
  ctx.fillText("self-reported, local", left, y);
  ctx.textAlign = "right";
  ctx.fillText(SITE, left + width, y);
  return f;
}

function copy(text) {
  if (navigator.clipboard && navigator.clipboard.writeText) {
    return navigator.clipboard.writeText(text);
  }
  return Promise.reject(new Error("no clipboard"));
}

// openShare shows the share dialog of badge x (an entry of allBadges) of
// catalog c; opener gets the focus back when it closes.
export function openShare(x, c, opener) {
  const back = el("div", { class: "modal-back", role: "dialog", "aria-modal": "true", "aria-label": "Share the " + cardTitle(x) + " badge" });
  let release = () => {};
  const close = () => { release(); back.remove(); if (opener) { opener.focus(); } };
  release = pushOverlay(close);
  back.addEventListener("click", (e) => { if (e.target === back) { close(); } });
  const canvas = el("canvas", { class: "share-card", width: String(CARD_W), height: String(CARD_H), role: "img", "aria-label": "Share card of the " + cardTitle(x) + " badge, 1200 by 627 pixels" });
  const name = el("input", { class: "input", type: "text", maxlength: "60", placeholder: "Your name, as it should read on the card", "aria-label": "Your name on the card", autocomplete: "name" });
  name.value = store(NAME_KEY) || "";
  const post = el("p", { class: "share-post mono" });
  const redraw = () => { post.textContent = drawCard(canvas, x, c, name.value).post; };
  name.addEventListener("input", () => { store(NAME_KEY, name.value.trim() ? name.value : null); redraw(); });
  const download = el("button", { class: "btn primary", text: "Download PNG" });
  download.addEventListener("click", () => {
    redraw();
    canvas.toBlob((blob) => {
      if (!blob) { toast("Could not draw the card", true); return; }
      const url = URL.createObjectURL(blob);
      const a = el("a", { href: url, download: "kubedrill-" + String(x.badge.code).toLowerCase().replace(/[^a-z0-9.]+/g, "-") + "-badge.png" });
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 10000);
    }, "image/png");
  });
  const copyBtn = el("button", { class: "btn", text: "Copy text" });
  copyBtn.addEventListener("click", () => {
    copy(post.textContent).then(() => toast("Post text copied")).catch(() => {
      const r = document.createRange();
      r.selectNodeContents(post);
      const sel = window.getSelection();
      sel.removeAllRanges();
      sel.addRange(r);
      toast("Select and copy the text below the card; the clipboard is not available here", true);
    });
  });
  back.appendChild(el("div", { class: "modal share" },
    el("div", { class: "eyebrow", text: "Share" }),
    el("h2", { class: "modal-title", text: "Share the " + cardTitle(x) + " badge" }),
    el("div", { class: "share-frame" }, canvas),
    el("label", { class: "field share-name" }, el("span", { text: "Name on the card" }), name),
    el("div", { class: "share-text" }, el("span", { class: "eyebrow muted", text: "Post text" }), post),
    el("p", { class: "small muted", text: "Self-reported: kubedrill runs on your machine and hosts nothing, so the card carries no verification link. The name stays in this browser." }),
    el("div", { class: "modal-actions" }, el("button", { class: "btn ghost", text: "Close", onclick: close }), copyBtn, download)));
  document.body.appendChild(back);
  redraw();
  name.focus();
}
