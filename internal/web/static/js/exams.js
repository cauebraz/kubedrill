// Exams: for each certification, the share of the exam weight practiced
// clean, then every domain with its weight, its coverage and the
// assignments that train it. Coverage uses the badge rule: a domain is
// covered when one of its assignments has a clean solve.

import { api } from "./api.js";
import { el, clear } from "./dom.js";
import { flatten, skeleton, errorState } from "./ui.js";
import { badgeSVG } from "./badge.js";

const TRACKS = { CKA: "--track-cka", CKAD: "--track-ckad", CKS: "--track-cks" };

export function showExams(app, ctx) {
  ctx.crumbs([{ href: "#/", text: "Home" }, { text: "Exams" }]);
  const page = el("div", { class: "page" }, el("div", { class: "page-inner" }, skeleton(5)));
  clear(app, page);
  api.get("/api/catalog").then((c) => {
    const byID = Object.fromEntries(flatten(c).map((a) => [a.id, a]));
    clear(page, el("div", { class: "page-inner" },
      el("header", { class: "page-head" },
        el("div", { class: "eyebrow", text: "Exams" }),
        el("h1", { text: "Exam domain coverage" }),
        el("p", { class: "lede", text: "A domain is covered when one of its assignments was solved clean: no guide, no hint. The weight is the domain's share of the exam score. These are kubedrill practice tracks, not a certification." })),
      c.exams.map((e) => examSection(e, byID))));
  }).catch((e) => clear(page, el("div", { class: "page-inner" }, errorState("Could not load the exams: " + e.message, () => showExams(app, ctx)))));
}

function stateOf(a) {
  if (!a) { return "todo"; }
  if (a.mastery && a.mastery.mastered) { return "mastered"; }
  if (a.clean) { return "clean"; }
  return "todo";
}

const STATE_WORDS = { mastered: "mastered", clean: "solved clean", todo: "not yet" };

function domainState(d, byID) {
  if (!d.assignments.length) { return "empty"; }
  const states = d.assignments.map((id) => stateOf(byID[id]));
  if (states.includes("mastered")) { return "mastered"; }
  if (states.includes("clean")) { return "covered"; }
  return "open";
}

const DOMAIN_WORDS = { mastered: "Mastered", covered: "Covered", open: "Not covered yet", empty: "No assignment yet" };

function examSection(e, byID) {
  const color = "var(" + (TRACKS[e.id] || "--brand") + ")";
  const domains = e.domains.map((d) => ({ d, state: domainState(d, byID) }));
  const weight = domains.reduce((n, x) => n + (x.state === "covered" || x.state === "mastered" ? x.d.weight : 0), 0);
  const total = domains.reduce((n, x) => n + x.d.weight, 0) || 100;
  const pct = Math.round(100 * weight / total);
  return el("section", { class: "card exam", style: "--tc: " + color },
    el("div", { class: "exam-head" },
      e.badge ? el("a", { class: "bdg-link", href: "#/badges", title: e.id + " track badge" }, badgeSVG("track", e.badge, { size: 72 })) : null,
      el("div", { class: "exam-title" },
        el("div", { class: "eyebrow", text: e.id + " track" }),
        el("h2", { text: e.title }),
        e.badge ? el("p", { class: "exam-caption", text: e.badge.caption }) : null),
      el("div", { class: "exam-score" },
        el("span", { class: "exam-pct", text: pct + "%" }),
        el("span", { class: "exam-pct-label", text: "of the exam weight practiced clean" }))),
    el("div", { class: "weight-bar", role: "img", "aria-label": pct + "% of the " + e.id + " exam weight is in covered domains" },
      domains.map((x) => el("div", { class: "weight-seg is-" + x.state, style: "flex-grow: " + x.d.weight, title: x.d.title + ": " + x.d.weight + "%, " + DOMAIN_WORDS[x.state].toLowerCase() }))),
    el("ul", { class: "weight-legend", "aria-hidden": "true" },
      ["covered", "open", "empty"].map((s) => el("li", null, el("span", { class: "weight-key is-" + s }), el("span", { text: DOMAIN_WORDS[s] })))),
    el("ol", { class: "domains" }, domains.map((x) => domainRow(x.d, x.state, byID))));
}

function domainRow(d, state, byID) {
  return el("li", { class: "domain is-" + state },
    el("div", { class: "domain-head" },
      el("span", { class: "domain-title", text: d.title }),
      el("span", { class: "domain-meta" },
        el("span", { class: "domain-weight", text: d.weight + "% of the exam" }),
        el("span", { class: "domain-state is-" + state, text: DOMAIN_WORDS[state] }))),
    d.assignments.length
      ? el("ul", { class: "domain-list" }, d.assignments.map((id) => {
        const a = byID[id];
        const s = stateOf(a);
        const href = "#/a/" + encodeURIComponent(id);
        return el("li", { class: "domain-item" },
          el("span", { class: "item-dot is-" + s, "aria-hidden": "true" }),
          el("a", { class: "item-title", href, text: a ? a.title : id }),
          el("span", { class: "item-state is-" + s, text: STATE_WORDS[s] }),
          el("a", { class: "btn small ghost", href: href + "/exam", "aria-label": "Rehearse " + (a ? a.title : id) + " as an exam", text: "Rehearse" }));
      }))
      : el("p", { class: "planned", text: "Planned in the curriculum; no assignment written yet." }));
}
