// Badges: every module, level and exam track badge with its state and the
// caption the rules of internal/progress wrote, the legend, and the share
// card of an earned or mastered badge.

import { api } from "./api.js";
import { el, clear } from "./dom.js";
import { skeleton, errorState } from "./ui.js";
import { badgeSVG, pips, stateLabel, allBadges, isEarned, remember } from "./badge.js";
import { openShare } from "./share.js";
import { openReset } from "./reset.js";

export function showBadges(app, ctx) {
  ctx.crumbs([{ href: "#/", text: "Home" }, { text: "Badges" }]);
  const page = el("div", { class: "page" }, el("div", { class: "page-inner" }, skeleton(6)));
  clear(app, page);
  api.get("/api/catalog").then((c) => {
    remember(c);
    clear(page, render(c));
  }).catch((e) => clear(page, el("div", { class: "page-inner" }, errorState("Could not load the badges: " + e.message, () => showBadges(app, ctx)))));
}

function shareButton(x, c) {
  if (!isEarned(x.badge)) { return null; }
  const b = el("button", { class: "btn small", text: "Share", "aria-label": "Share the " + x.badge.code + " badge" });
  b.addEventListener("click", () => openShare(x, c, b));
  return b;
}

function moduleTile(x, c) {
  return el("article", { class: "card bdg-tile", "data-state": x.badge.state },
    badgeSVG("module", x.badge, { size: 128, level: x.level }),
    el("div", { class: "bdg-text" },
      el("h4", { text: x.title }),
      el("p", { class: "bdg-caption", text: x.badge.caption })),
    pips("module", x.badge, x.level),
    stateLabel("module", x.badge, x.level),
    shareButton(x, c));
}

function levelTile(x, c) {
  return el("article", { class: "card bdg-tile level", "data-state": x.badge.state },
    badgeSVG("level", x.badge, { size: 104 }),
    el("div", { class: "bdg-text" },
      el("h4", { text: x.title }),
      el("p", { class: "bdg-caption", text: x.badge.caption })),
    stateLabel("level", x.badge),
    shareButton(x, c));
}

function trackTile(x, c) {
  return el("article", { class: "card bdg-tile track", "data-state": x.badge.state },
    badgeSVG("track", x.badge, { size: 128 }),
    el("div", { class: "bdg-text" },
      el("h4", { text: x.title }),
      el("p", { class: "small muted", text: x.exam.title + " domains" }),
      el("p", { class: "bdg-caption", text: x.badge.caption }),
      stateLabel("track", x.badge),
      shareButton(x, c)));
}

function render(c) {
  const all = allBadges(c);
  const mods = all.filter((x) => x.kind === "module");
  const earned = all.filter((x) => isEarned(x.badge)).length;
  const byLevel = c.levels.map((lv) => ({ lv, list: mods.filter((x) => x.level === lv.id) })).filter((g) => g.list.length);
  return el("div", { class: "page-inner badges" },
    el("header", { class: "page-head" },
      el("div", { class: "eyebrow", text: "Badges" }),
      el("h1", { text: "Earned by solving clean" }),
      el("p", { class: "lede", text: "No guide, no hint, within the expected time. " + earned + " of " + all.length + " badges earned or mastered. Self-reported on this machine; the exam tracks are kubedrill practice tracks, not a certification." })),
    el("section", { class: "section", "aria-labelledby": "bdg-modules" },
      el("div", { class: "section-head" }, el("h2", { id: "bdg-modules", text: "Module badges" }), el("span", { class: "small muted", text: "Hexagon, one per module with a written assignment" })),
      byLevel.length ? el("div", { class: "bdg-groups" }, byLevel.map(({ lv, list }) => el("div", { class: "bdg-group" },
        el("h3", { class: "eyebrow muted", text: lv.id + " " + lv.title }),
        el("div", { class: "bdg-grid", style: "--n: " + Math.min(list.length, 4) }, list.map((x) => moduleTile(x, c)))))) : el("div", { class: "empty", text: "No assignment is written yet." })),
    el("section", { class: "section", "aria-labelledby": "bdg-levels" },
      el("div", { class: "section-head" }, el("h2", { id: "bdg-levels", text: "Level badges" }), el("span", { class: "small muted", text: "Shield, one color per level" })),
      el("div", { class: "bdg-grid levels" }, all.filter((x) => x.kind === "level").map((x) => levelTile(x, c)))),
    el("section", { class: "section", "aria-labelledby": "bdg-tracks" },
      el("div", { class: "section-head" }, el("h2", { id: "bdg-tracks", text: "Exam tracks" }), el("span", { class: "small muted", text: "Rosette, every exam domain covered by clean solves" })),
      el("div", { class: "bdg-grid tracks" }, all.filter((x) => x.kind === "track").map((x) => trackTile(x, c)))),
    el("section", { class: "bdg-legend", "aria-label": "How badges work" },
      legend("The ring is the method", "The five arcs come from the kubedrill mark: the five steps of the troubleshooting loop. They light as the badge's criteria are met."),
      legend("The shape is the tier", "Hexagon for a module, shield for a level, rosette for an exam track. The color follows the level."),
      legend("Clean means clean", "A clean solve uses no guide and no hint, ends within the expected minutes and, when the audit log saw the run, reads before its first change. Mastered: 3 clean solves in your last 4 attempts, on distinct variants, across 2 sessions. The three dots are the variants solved clean in your last attempts.")),
    resetAll());
}

// resetAll is the footer that starts every assignment over.
function resetAll() {
  const b = el("button", { class: "btn small danger-quiet", type: "button", text: "Reset all progress" });
  b.addEventListener("click", () => openReset(null, b));
  return el("section", { class: "reset-all", "aria-labelledby": "reset-all-title" },
    el("div", null,
      el("h3", { id: "reset-all-title", text: "Start over" }),
      el("p", { class: "small muted", text: "Every run stops counting toward clean solves, mastery and badges, and every stage checkmark is cleared. The journal keeps the history, and Undo or kubedrill progress restore --all brings it back." })),
    b);
}

function legend(title, text) {
  return el("div", null, el("h3", { text: title }), el("p", { text }));
}
