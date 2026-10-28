// Components shared by the home, catalog and assignment screens.

import { el, svg, icons } from "./dom.js";

// REPO is the public repository; every screen that names the source links it.
export const REPO = "https://github.com/cauebraz/kubedrill";

export const STAGES = [
  { id: "meeting", label: "Meet the customer", sub: "Gather the requirements" },
  { id: "quiz", label: "Check understanding", sub: "Answer the mentor" },
  { id: "learn", label: "Learn the pieces", sub: "Diagram, story, concepts" },
  { id: "practice", label: "Guided practice", sub: "Step by step, live cluster" },
  { id: "diy", label: "Do it yourself", sub: "Another variant, no guide" },
  { id: "done", label: "Result", sub: "Badge and mastery" },
  { id: "exam", label: "Exam rehearsal", sub: "Timed, no help, one submit" }
];

export function stagesFor(a) {
  return a.simulation ? STAGES : STAGES.filter((s) => s.id !== "meeting" && s.id !== "quiz");
}

export function stagesDone(p) {
  return ["meeting", "quiz", "learn", "practice", "diy"].filter((k) => p && p[k] && p[k].done).length;
}

export function stageDots(p, simulation) {
  const keys = simulation === false ? ["learn", "practice", "diy"] : ["meeting", "quiz", "learn", "practice", "diy"];
  const done = keys.filter((k) => p && p[k] && p[k].done).length;
  return el("span", { class: "dots", role: "img", "aria-label": done + " of " + keys.length + " stages done", title: done + " of " + keys.length + " stages done" },
    keys.map((k) => el("i", { class: p && p[k] && p[k].done ? "on" : "" })));
}

export function ring(done, total, earned, size) {
  const s = size || 44;
  const r = s / 2 - 3;
  const c = 2 * Math.PI * r;
  const frac = total ? done / total : 0;
  return el("div", { class: "ring" + (earned ? " earned" : ""), style: "width:" + s + "px;height:" + s + "px", role: "img", "aria-label": earned ? "Badge earned" : done + " of " + total + " done" },
    svg("svg", { viewBox: "0 0 " + s + " " + s, width: s, height: s },
      svg("circle", { class: "track", cx: s / 2, cy: s / 2, r }),
      svg("circle", { class: "bar", cx: s / 2, cy: s / 2, r, "stroke-dasharray": c, "stroke-dashoffset": c * (1 - frac) })),
    el("span", { class: "lbl", text: earned ? "\u2713" : done + "/" + total }));
}

export const STATUS_WORDS = [["todo", "not started"], ["started", "in progress"], ["live", "live"], ["complete", "complete"], ["mastered", "mastered"]];
const STATUS_CLASS = { todo: "", started: "info", live: "warn", complete: "good", mastered: "brand" };
const STATUS_TITLES = {
  todo: "No stage done yet",
  started: "Some stages done; the assignment is complete when the Do it yourself run passes",
  live: "A run of this assignment is active now",
  complete: "The Do it yourself run passed; it counts toward the plan badge",
  mastered: "3 clean solves on different variants; kubedrill brings it back for review"
};

export function statusOf(a) {
  if (a.status === "active") { return "live"; }
  if (a.status === "mastered") { return "mastered"; }
  if (isDone(a)) { return "complete"; }
  if (a.status === "solved" || stagesDone(a.progress) > 0) { return "started"; }
  return "todo";
}

export function statusPill(a) {
  const s = statusOf(a);
  return el("span", { class: "pill " + STATUS_CLASS[s], title: STATUS_TITLES[s], text: Object.fromEntries(STATUS_WORDS)[s] });
}

export function hideFixSection(root, note) {
  const h = [...root.querySelectorAll("h2")].find((x) => x.textContent.trim().toLowerCase() === "the fix and why it works");
  if (!h) { return false; }
  let n = h.nextSibling;
  while (n && !(n.nodeType === 1 && /^H[12]$/.test(n.tagName))) {
    const after = n.nextSibling;
    n.remove();
    n = after;
  }
  h.after(el("div", { class: "callout info fix-hidden" }, el("div", { text: note })));
  return true;
}

// masteryNeed is how many distinct variants mastery asks for.
export function masteryNeed(m) { return Math.min(3, m.variants || 1); }

// cleanVariantsText counts the distinct variants among the clean solves of
// the last attempts against the variants mastery needs.
export function cleanVariantsText(m) {
  const need = masteryNeed(m);
  return Math.min(m.cleanVariants || 0, need) + " of " + need + " variants solved clean";
}

export function masteryMeter(m) {
  const need = masteryNeed(m);
  const title = m.mastered
    ? "Mastered: 3 clean solves in your last 4 attempts, on " + need + " variants"
    : "Mastery: " + m.cleanInLast + "/3 clean solves in your last " + (m.last || 4) + " attempts, " + cleanVariantsText(m) + "; mastery also needs " + need + " different variants among them. Clean means no hint and within the expected minutes.";
  const segs = [];
  for (let i = 0; i < 3; i++) { segs.push(el("i", { class: m.mastered || i < Math.min(m.cleanInLast, 3) ? "on" : "" })); }
  return el("span", { class: "mastery" + (m.mastered ? " done" : ""), title, role: "img", "aria-label": title }, segs);
}

export function isDone(a) { return a.progress && a.progress.diy && a.progress.diy.done; }

// requiresMet reports whether every assignment a requires is solved;
// byID maps ids to cards.
export function requiresMet(a, byID) {
  return (a.requires || []).every((id) => {
    const r = byID[id];
    return !r || r.status === "solved" || r.status === "mastered" || isDone(r);
  });
}

export function assignmentRow(a, byID, compact) {
  const locked = byID && !requiresMet(a, byID);
  return el("a", { class: "assign-row" + (compact ? " compact" : ""), href: "#/a/" + encodeURIComponent(a.id) },
    el("div", { class: "title" },
      locked ? el("span", { class: "lock", title: "Recommended first: " + a.requires.join(", "), html: icons.lock }) : null,
      el("span", { text: a.title })),
    el("div", { class: "meta" },
      el("span", { text: a.minutes + " min" }),
      a.simulation ? null : el("span", { class: "tag", text: "lab only" }),
      stageDots(a.progress, a.simulation),
      a.mastery ? masteryMeter(a.mastery) : null),
    el("div", { class: "right" }, statusPill(a), el("span", { class: "arrow", html: icons.arrow })));
}

// flatten lists every assignment card of a catalog with its level and
// module.
export function flatten(c) {
  const out = [];
  for (const lv of c.levels) {
    for (const m of lv.modules) {
      for (const a of m.assignments) { out.push(Object.assign({ level: lv.id, levelTitle: lv.title, module: m.id, moduleTitle: m.title }, a)); }
    }
  }
  return out;
}

export function skeleton(lines) {
  return el("div", { class: "skeleton", "aria-busy": "true", "aria-label": "Loading" }, Array.from({ length: lines || 3 }, () => el("div", { class: "sk-line" })));
}

export function errorState(message, retry) {
  return el("div", { class: "error-state", role: "alert" },
    el("div", { class: "eyebrow", text: "Something went wrong" }),
    el("p", { text: message }),
    retry ? el("button", { class: "btn", text: "Try again", onclick: retry }) : null);
}
