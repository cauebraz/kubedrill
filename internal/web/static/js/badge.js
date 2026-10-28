// Badges: the SVG of a module, level or exam track badge, its colors, the
// list of every badge of a catalog, and the last states the learner saw.
// The shape is the tier, the ring of five arcs is the method (the five
// steps of the mark), the color is the level.

import { el, svg, store } from "./dom.js";

export const SHAPES = {
  module: "M60 5L108 32.5V87.5L60 115L12 87.5V32.5Z",
  level: "M60 5L106 19V58C106 87 86 106 60 116C34 106 14 87 14 58V19Z",
  track: rosette()
};

export const KIND_WORDS = { module: "Module", level: "Level", track: "Exam track" };

export const STATE_WORDS = { locked: "Locked", "in-progress": "In progress", earned: "Earned", mastered: "Mastered" };

const TRACKS = ["cka", "ckad", "cks"];

function rosette() {
  const pts = [];
  for (let i = 0; i < 24; i++) {
    const a = (i * 15 - 90) * Math.PI / 180;
    const r = i % 2 === 0 ? 56 : 49;
    pts.push((60 + r * Math.cos(a)).toFixed(2) + " " + (60 + r * Math.sin(a)).toFixed(2));
  }
  return "M" + pts.join("L") + "Z";
}

// arcPath is arc i of the ring: radius 34, from -126 + 72i + 7 degrees for
// 58 degrees.
export function arcPath(i) {
  const a0 = -126 + i * 72 + 7;
  const p = (a) => [60 + 34 * Math.cos(a * Math.PI / 180), 60 + 34 * Math.sin(a * Math.PI / 180)];
  const s = p(a0);
  const e = p(a0 + 58);
  return "M" + s[0].toFixed(2) + " " + s[1].toFixed(2) + "A34 34 0 0 1 " + e[0].toFixed(2) + " " + e[1].toFixed(2);
}

// colorVar is the custom property of a badge's color: the level of a
// module or level badge, the exam of a track.
export function colorVar(kind, id, level) {
  if (kind === "track") {
    const t = String(id || "").toLowerCase();
    return TRACKS.includes(t) ? "--track-" + t : "--brand";
  }
  const m = /^L([0-6])$/.exec(level || id || "");
  return m ? "--level-" + m[1] : "--brand";
}

// SMALL is the size under which the code in the center cannot be read; a
// smaller badge draws the center dot of the mark instead.
export const SMALL = 64;

// badgeSVG draws badge b of kind (module, level, track) at size pixels.
export function badgeSVG(kind, b, opts) {
  const o = opts || {};
  const size = o.size || 120;
  const d = SHAPES[kind] || SHAPES.module;
  const lit = b.state === "locked" ? 0 : Math.max(0, Math.min(5, b.lit || 0));
  const label = o.label || ((b.code || "") + " " + (KIND_WORDS[kind] || "").toLowerCase() + " badge, " + (STATE_WORDS[b.state] || b.state).toLowerCase() + ": " + b.caption);
  const node = svg("svg", { class: "bdg " + b.state, viewBox: "0 0 120 120", width: size, height: size, role: "img", "aria-label": label, style: "--bc: var(" + colorVar(kind, b.code, o.level) + ")" },
    b.state === "mastered" ? svg("path", { class: "bdg-halo", d }) : null,
    svg("path", { class: "bdg-shape", d }),
    svg("g", { class: "bdg-ring" }, [0, 1, 2, 3, 4].map((i) => svg("path", { class: i < lit ? "on" : "", d: arcPath(i) }))),
    size < SMALL
      ? svg("circle", { class: "bdg-dot", cx: 60, cy: kind === "level" ? 61 : 60, r: 8 })
      : svg("text", { x: 60, y: kind === "level" ? 67 : 66, "text-anchor": "middle", "font-size": kind === "level" ? 18 : 15, text: b.code || "" }));
  return node;
}

// pips shows the variants solved clean out of 3.
export function pips(kind, b, level) {
  const n = Math.max(0, Math.min(3, b.pips || 0));
  return el("span", { class: "pips", role: "img", "aria-label": n + " of 3 variants solved clean", title: n + " of 3 variants solved clean in your last attempts", style: "--bc: var(" + colorVar(kind, b.code, level) + ")" },
    [0, 1, 2].map((i) => el("i", { class: i < n ? "on" : "" })));
}

export function stateLabel(kind, b, level) {
  const word = STATE_WORDS[b.state] || b.state;
  return el("span", { class: "bdg-state " + b.state, style: "--bc: var(" + colorVar(kind, b.code, level) + ")" },
    el("span", { text: word }),
    b.state === "in-progress" ? el("span", { text: (b.lit || 0) + " of 5" }) : null);
}

export function isEarned(b) { return b.state === "earned" || b.state === "mastered"; }

// allBadges lists every badge of a catalog in page order: modules with a
// written assignment (in curriculum order), levels, exam tracks.
export function allBadges(c) {
  const out = [];
  for (const lv of c.levels) {
    for (const m of lv.modules) {
      if (m.assignments.length) { out.push({ kind: "module", key: "module:" + m.id, title: m.title, level: lv.id, levelTitle: lv.title, module: m, badge: m.badge }); }
    }
  }
  for (const lv of c.levels) { out.push({ kind: "level", key: "level:" + lv.id, title: lv.title, level: lv.id, levelObj: lv, badge: lv.badge }); }
  for (const e of c.exams || []) { out.push({ kind: "track", key: "track:" + e.id, title: e.id + " track", exam: e, badge: e.badge }); }
  return out;
}

const SEEN = "kd:badges-seen";

// remember stores the state of every badge of c and returns the states
// stored before, keyed like allBadges; a page that compares them shows a
// badge whose state changed since the learner last saw it.
export function remember(c) {
  const before = store(SEEN) || {};
  const now = {};
  for (const x of allBadges(c)) { now[x.key] = x.badge.state; }
  store(SEEN, now);
  return before;
}
