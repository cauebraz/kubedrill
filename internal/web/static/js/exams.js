// Exams: for each certification, every domain with its weight, the
// assignments that train it and how many of them are solved.

import { api } from "./api.js";
import { el, clear } from "./dom.js";
import { flatten, statusPill, skeleton, errorState } from "./ui.js";

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
        el("p", { class: "lede", text: "Each domain lists the assignments that train it. Solved counts any solve; the weight is the share of the exam score." })),
      c.exams.map((e) => examCard(e, byID))));
  }).catch((e) => clear(page, el("div", { class: "page-inner" }, errorState("Could not load the exams: " + e.message, () => showExams(app, ctx)))));
}

function examCard(e, byID) {
  const total = e.domains.reduce((n, d) => n + d.assignments.length, 0);
  const solved = e.domains.reduce((n, d) => n + d.solved, 0);
  return el("section", { class: "card exam" },
    el("div", { class: "exam-head" },
      el("div", null, el("div", { class: "eyebrow", text: e.id }), el("h2", { text: e.title })),
      el("div", { class: "exam-total", text: solved + " / " + total + " solved" })),
    el("div", { class: "domains" }, e.domains.map((d) => {
      const frac = d.assignments.length ? d.solved / d.assignments.length : 0;
      return el("div", { class: "domain" },
        el("div", { class: "domain-head" },
          el("span", { class: "domain-title", text: d.title }),
          el("span", { class: "domain-weight", text: d.weight + "%" })),
        el("div", { class: "meter", role: "progressbar", "aria-valuemin": "0", "aria-valuemax": String(d.assignments.length), "aria-valuenow": String(d.solved), "aria-label": d.title }, el("div", { style: "width:" + Math.round(frac * 100) + "%" })),
        d.assignments.length
          ? el("ul", { class: "domain-list" }, d.assignments.map((id) => {
            const a = byID[id];
            return el("li", null, el("a", { href: "#/a/" + encodeURIComponent(id), text: a ? a.title : id }),
              el("span", { class: "row-end" }, a ? statusPill(a.status) : null, el("a", { class: "link small", href: "#/a/" + encodeURIComponent(id) + "/exam", text: "rehearse" })));
          }))
          : el("p", { class: "planned", text: "No assignment yet; planned in the curriculum." }));
    })));
}
