// Home: what to do now (the active run or the recommendation), the plans
// in progress, reviews due, and the way into the full catalog.

import { api } from "./api.js";
import { el, clear, icon, plural, duration } from "./dom.js";
import { bind } from "./keys.js";
import { assignmentRow, flatten, isDone, ring, skeleton, errorState } from "./ui.js";

export function showHome(app, ctx) {
  ctx.crumbs([{ text: "Home" }]);
  const page = el("div", { class: "page home" }, el("div", { class: "page-inner" }, skeleton(4)));
  clear(app, page);
  Promise.all([api.get("/api/catalog"), api.get("/api/next")]).then(([c, next]) => {
    clear(page, render(c, next));
  }).catch((e) => clear(page, el("div", { class: "page-inner" }, errorState("Could not load your plans: " + e.message, () => showHome(app, ctx)))));
}

function render(c, next) {
  const all = flatten(c);
  const byID = Object.fromEntries(all.map((a) => [a.id, a]));
  const done = all.filter(isDone).length;
  const badges = c.levels.reduce((n, lv) => n + lv.modules.filter((m) => m.badge).length, 0);
  const planned = c.levels.reduce((n, lv) => n + lv.modules.reduce((k, m) => k + m.planned, 0), 0);

  const head = el("header", { class: "home-head" },
    el("div", { class: "eyebrow", text: "kubedrill" }),
    el("h1", null, "Fix real Kubernetes problems, ", el("em", { text: "one customer at a time." })),
    el("p", { class: "profile" },
      el("span", null, el("b", { text: String(done) }), " of " + all.length + " assignments complete"),
      el("span", { class: "sep", text: "/" }),
      el("span", null, el("b", { text: String(badges) }), " " + (badges === 1 ? "badge" : "badges")),
      next.reviewsPending ? [el("span", { class: "sep", text: "/" }), el("span", null, el("b", { text: String(next.reviewsPending) }), " " + (next.reviewsPending === 1 ? "review" : "reviews") + " due")] : null,
      el("span", { class: "sep", text: "/" }),
      el("span", null, planned + " more planned")));

  const cont = continueCard(next, byID);
  const plans = plansInProgress(c, byID);

  const browse = el("a", { class: "card browse", href: "#/catalog" },
    el("div", null,
      el("div", { class: "eyebrow", text: "Catalog" }),
      el("h3", { text: "Browse every assignment" }),
      el("p", { text: "Filter by level, status, exam domain or concept, and search by title." })),
    el("span", { class: "arrow", html: '<svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M6 3.5 10.5 8 6 12.5"/></svg>' }));
  const exams = el("a", { class: "card browse", href: "#/exams" },
    el("div", null,
      el("div", { class: "eyebrow", text: "Exams" }),
      el("h3", { text: "Exam domain coverage" }),
      el("p", { text: "How much of each CKA, CKAD and CKS domain your solved assignments cover." })),
    el("span", { class: "arrow", html: '<svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M6 3.5 10.5 8 6 12.5"/></svg>' }));

  bind("c", "Open the catalog", () => { location.hash = "#/catalog"; });
  bind("Enter", "Continue with the highlighted assignment", () => { const a = cont.querySelector("a.btn.primary"); if (a) { a.click(); } });

  return el("div", { class: "page-inner" }, head, cont,
    el("section", { class: "section" }, el("div", { class: "section-head" }, el("h2", { text: "Your plans" }), el("a", { class: "link", href: "#/catalog", text: "All plans" })), plans),
    el("div", { class: "browse-row" }, browse, exams));
}

function continueCard(next, byID) {
  if (next.active) {
    const a = next.active;
    const stage = a.stage || "practice";
    const elapsed = (Date.now() - new Date(a.startedAt).getTime()) / 1000;
    return el("section", { class: "card continue live", "aria-label": "Continue" },
      el("div", { class: "continue-body" },
        el("span", { class: "pill warn live", text: "Live environment" }),
        el("h2", { text: a.title }),
        el("p", { text: (a.stage === "diy" ? "Do it yourself run" : a.stage === "practice" ? "Guided practice" : "Episode started from the terminal") + " in namespace " + a.namespace + ", running for " + duration(elapsed) + " of the expected " + a.minutes + " min." })),
      el("a", { class: "btn primary", href: "#/a/" + encodeURIComponent(a.scenarioId) + "/" + stage }, icon("play"), "Resume"));
  }
  if (next.scenarioId) {
    const card = byID[next.scenarioId];
    return el("section", { class: "card continue", "aria-label": "Recommended next" },
      el("div", { class: "continue-body" },
        el("span", { class: "pill brand", text: next.review ? "Spaced review" : "Recommended next" }),
        el("h2", { text: next.title }),
        el("p", { text: capitalize(next.reason) + ". About " + next.minutes + " min." }),
        card ? el("div", { class: "continue-meta" }, card.moduleTitle) : null),
      el("a", { class: "btn primary", href: "#/a/" + encodeURIComponent(next.scenarioId) }, icon("play"), card && card.progress && card.progress.meeting && card.progress.meeting.done ? "Continue" : "Start"));
  }
  return el("section", { class: "card continue" },
    el("div", { class: "continue-body" },
      el("span", { class: "pill good", text: "All caught up" }),
      el("h2", { text: "Nothing is due right now" }),
      el("p", { text: "Every written assignment is complete and no review is due. Run a DIY again on another variant, or browse the catalog." })),
    el("a", { class: "btn", href: "#/catalog", text: "Open the catalog" }));
}

function capitalize(s) { return s ? s[0].toUpperCase() + s.slice(1) : s; }

// plansInProgress shows the modules the learner started and the first one
// not started, so the home stays short however many plans exist.
function plansInProgress(c, byID) {
  const mods = [];
  for (const lv of c.levels) {
    for (const m of lv.modules) {
      if (!m.assignments.length) { continue; }
      const done = m.assignments.filter(isDone).length;
      const started = m.assignments.some((a) => a.status !== "todo" || (a.progress && (a.progress.meeting.done || a.progress.learn.done)));
      mods.push({ lv, m, done, started });
    }
  }
  let shown = mods.filter((x) => x.started && !x.m.badge);
  const firstNew = mods.find((x) => !x.started);
  if (firstNew) { shown.push(firstNew); }
  if (!shown.length) { shown = mods.slice(0, 2); }
  if (!shown.length) { return el("div", { class: "empty", text: "No assignment is written yet. The catalog lists the planned ones." }); }
  return el("div", { class: "modules" }, shown.slice(0, 4).map(({ lv, m, done }) =>
    el("article", { class: "card module" },
      el("div", { class: "module-top" },
        el("div", null, el("div", { class: "eyebrow", text: lv.id + " / " + m.id }), el("h3", { text: m.title }), el("p", { class: "summary", text: m.summary })),
        ring(done, m.assignments.length, m.badge)),
      el("div", { class: "assign-list" }, m.assignments.map((a) => assignmentRow(a, byID))),
      m.planned ? el("div", { class: "planned", text: plural(m.planned, "more assignment") + " planned" }) : null)));
}
