// Catalog: every assignment of the curriculum, searchable and filtered by
// level, status, exam domain and concept. Levels and plans with nothing
// written yet collapse to one line; the filters live in the URL so a
// filtered view can be bookmarked.

import { api } from "./api.js";
import { el, clear, icons, plural } from "./dom.js";
import { bind } from "./keys.js";
import { assignmentRow, flatten, isDone, ring, skeleton, errorState } from "./ui.js";

const STATUSES = [["", "Any status"], ["todo", "Not started"], ["active", "Active"], ["solved", "Solved"], ["mastered", "Mastered"], ["complete", "DIY passed"]];

function readFilters() {
  const q = new URLSearchParams((location.hash.split("?")[1]) || "");
  return { q: q.get("q") || "", level: q.get("level") || "", status: q.get("status") || "", exam: q.get("exam") || "", concept: q.get("concept") || "" };
}

function writeFilters(f) {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(f)) { if (v) { q.set(k, v); } }
  const s = q.toString();
  history.replaceState(null, "", "#/catalog" + (s ? "?" + s : ""));
}

export function showCatalog(app, ctx) {
  ctx.crumbs([{ href: "#/", text: "Home" }, { text: "Catalog" }]);
  const page = el("div", { class: "page" }, el("div", { class: "page-inner" }, skeleton(6)));
  clear(app, page);
  api.get("/api/catalog").then((c) => clear(page, render(c))).catch((e) => clear(page, el("div", { class: "page-inner" }, errorState("Could not load the catalog: " + e.message, () => showCatalog(app, ctx)))));
}

function render(c) {
  const all = flatten(c);
  const byID = Object.fromEntries(all.map((a) => [a.id, a]));
  const f = readFilters();
  const concepts = [...new Set(all.flatMap((a) => a.concepts || []))].sort();
  const domains = c.exams.flatMap((e) => e.domains.map((d) => [e.id + ":" + d.id, e.id + " " + d.title]));

  const search = el("input", { type: "search", class: "input", placeholder: "Search titles, ids, concepts", value: f.q, "aria-label": "Search assignments" });
  const level = select([["", "Any level"]].concat(c.levels.map((l) => [l.id, l.id + " " + l.title])), f.level, "Level");
  const status = select(STATUSES, f.status, "Status");
  const exam = select([["", "Any exam domain"]].concat(domains), f.exam, "Exam domain");
  const concept = select([["", "Any concept"]].concat(concepts.map((x) => [x, x])), f.concept, "Concept");
  const reset = el("button", { class: "btn ghost small", text: "Clear filters" });
  const count = el("div", { class: "result-count", "aria-live": "polite" });
  const results = el("div", { class: "catalog-results" });

  function matches(a, v) {
    if (v.level && a.level !== v.level) { return false; }
    if (v.status === "complete" ? !isDone(a) : v.status && a.status !== v.status) { return false; }
    if (v.exam && !(a.exam || []).includes(v.exam)) { return false; }
    if (v.concept && !(a.concepts || []).includes(v.concept)) { return false; }
    if (v.q) {
      const hay = (a.id + " " + a.title + " " + a.moduleTitle + " " + (a.concepts || []).join(" ")).toLowerCase();
      if (!v.q.toLowerCase().split(/\s+/).every((w) => hay.includes(w))) { return false; }
    }
    return true;
  }

  function update() {
    const v = { q: search.value.trim(), level: level.value, status: status.value, exam: exam.value, concept: concept.value };
    writeFilters(v);
    const filtering = Object.values(v).some(Boolean);
    reset.hidden = !filtering;
    const shown = all.filter((a) => matches(a, v));
    count.textContent = filtering ? plural(shown.length, "assignment") + " match" : plural(all.length, "assignment") + " written, " + c.levels.reduce((n, l) => n + l.modules.reduce((k, m) => k + m.planned, 0), 0) + " planned";
    clear(results, c.levels.map((lv) => levelBlock(lv, shown, byID, filtering, v.level === lv.id)).filter(Boolean));
    if (filtering && !shown.length) {
      results.appendChild(el("div", { class: "empty" }, "No assignment matches these filters. ", el("button", { class: "link", text: "Clear them", onclick: clearAll })));
    }
  }
  function clearAll() {
    search.value = ""; level.value = ""; status.value = ""; exam.value = ""; concept.value = "";
    update();
  }
  let t = 0;
  search.addEventListener("input", () => { clearTimeout(t); t = setTimeout(update, 120); });
  for (const s of [level, status, exam, concept]) { s.addEventListener("change", update); }
  reset.addEventListener("click", clearAll);
  bind("/", "Search the catalog", () => search.focus());
  update();

  return el("div", { class: "page-inner" },
    el("header", { class: "page-head" }, el("div", { class: "eyebrow", text: "Catalog" }), el("h1", { text: "Every assignment" }), count),
    el("div", { class: "catalog" },
      el("aside", { class: "filters", "aria-label": "Filters" },
        el("label", { class: "search" }, el("span", { class: "ic", html: icons.search }), search),
        field("Level", level), field("Status", status), field("Exam domain", exam), field("Concept", concept), reset),
      results));
}

function select(options, value, label) {
  return el("select", { class: "input", "aria-label": label }, options.map(([v, t]) => el("option", { value: v, selected: v === value, text: t })));
}

function field(label, control) { return el("label", { class: "field" }, el("span", { text: label }), control); }

function levelBlock(lv, shown, byID, filtering, open) {
  const ids = new Set(shown.map((a) => a.id));
  const mods = lv.modules.map((m) => ({ m, list: m.assignments.filter((a) => ids.has(a.id)) }));
  const total = lv.modules.reduce((n, m) => n + m.assignments.length, 0);
  const planned = lv.modules.reduce((n, m) => n + m.planned, 0);
  const visible = mods.filter((x) => x.list.length);
  if (filtering && !visible.length) { return null; }
  const head = el("summary", { class: "level-sum" },
    el("span", { class: "chev", html: icons.chevron }),
    el("span", { class: "eyebrow", text: lv.id }),
    el("span", { class: "level-title", text: lv.title }),
    el("span", { class: "level-count", text: total ? plural(total, "assignment") + (planned ? ", " + planned + " planned" : "") : planned + " planned, none written yet" }));
  const details = el("details", { class: "level-block" + (total ? "" : " empty-level"), open: (filtering || open || total > 0) && total > 0 }, head);
  if (!total) {
    details.appendChild(el("p", { class: "level-note", text: lv.after ? "You will: " + lv.after + "." : "Planned in the curriculum." }));
    return details;
  }
  details.appendChild(el("div", { class: "modules" }, (filtering ? visible : mods.filter((x) => x.m.assignments.length || x.m.planned)).map(({ m, list }) =>
    el("article", { class: "card module" },
      el("div", { class: "module-top" },
        el("div", null, el("div", { class: "eyebrow", text: m.id }), el("h3", { text: m.title }), el("p", { class: "summary", text: m.summary })),
        m.assignments.length ? ring(m.assignments.filter(isDone).length, m.assignments.length, m.badge) : null),
      list.length ? el("div", { class: "assign-list" }, list.map((a) => assignmentRow(a, byID, true))) : el("div", { class: "planned", text: "Nothing written yet" }),
      m.planned && !filtering ? el("div", { class: "planned", text: plural(m.planned, "more assignment") + " planned" }) : null))));
  return details;
}
