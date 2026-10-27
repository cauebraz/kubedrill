// Assignment shell: the stepper, the concept chips, and the stage screens.

import { api } from "./api.js";
import { el, clear, icons } from "./dom.js";
import { pushOverlay, bind } from "./keys.js";
import { stagesFor, masteryMeter, skeleton, errorState } from "./ui.js";
import { renderMeeting } from "./meeting.js";
import { renderQuiz } from "./quiz.js";
import { renderLearn } from "./learn.js";
import { renderLab } from "./lab.js";
import { renderDone, renderDebrief } from "./result.js";

export function firstOpenStage(a) {
  for (const s of stagesFor(a)) {
    const p = a.progress[s.id];
    if (s.id !== "done" && s.id !== "exam" && !(p && p.done)) { return s.id; }
  }
  return "done";
}

export function showAssignment(app, ctx, id, stageId, extra) {
  clear(app, el("div", { class: "page" }, el("div", { class: "page-inner" }, skeleton(5))));
  api.get("/api/assignment/" + encodeURIComponent(id)).then((a) => {
    if (!stageId) { location.replace("#/a/" + encodeURIComponent(a.id) + "/" + firstOpenStage(a)); return; }
    ctx.crumbs([{ href: "#/", text: "Home" }, { href: "#/catalog", text: a.moduleTitle || a.module }, { text: a.title }]);
    const stages = stagesFor(a);
    const go = (sid) => { location.hash = "#/a/" + encodeURIComponent(a.id) + "/" + sid; };
    const stepper = el("nav", { class: "stepper", "aria-label": "Assignment stages" },
      el("h2", { text: a.title }),
      el("p", { class: "sub" }, a.level + " / " + a.minutes + " min / " + a.variants + " variants"),
      el("ol", { class: "steps" }, stages.map((s, i) => {
        const p = a.progress[s.id];
        const current = s.id === stageId || (stageId === "debrief" && s.id === "done");
        return el("li", null, el("button", {
          class: "step" + (current ? " current" : "") + (p && p.done ? " done" : ""),
          title: s.label, "aria-current": current ? "step" : null, onclick: () => go(s.id)
        },
        el("span", { class: "num", text: p && p.done ? "\u2713" : String(i + 1) }),
        el("span", { class: "label" }, s.label, el("small", { text: s.sub }))));
      })),
      a.concepts.length ? el("div", { class: "concepts" },
        el("div", { class: "eyebrow muted", text: "Concepts" }),
        el("div", { class: "chips" }, a.concepts.map((c) => el("button", { class: "chip", title: c.summary || c.title, onclick: () => openConcept(c) }, el("span", { class: "ic", html: icons.book }), c.title)))) : null,
      el("div", { class: "stepper-mastery" }, el("div", { class: "eyebrow muted", text: "Mastery" }), masteryMeter(a.mastery), el("span", { class: "small muted", text: a.mastery.mastered ? "mastered" : a.mastery.variantsSolved + " of " + Math.min(3, a.variants) + " variants" })));
    const stage = el("section", { class: "stage", tabindex: "-1" });
    const lab = stageId === "practice" || stageId === "diy" || stageId === "exam";
    clear(app, el("div", { class: "assign" + (lab ? " lab-mode" : "") }, stepper, stage));
    const i = stages.findIndex((s) => s.id === stageId);
    const next = () => go(stages[Math.min(i + 1, stages.length - 1)].id);
    if (i > 0) { bind("mod+ArrowLeft", "Previous stage", () => go(stages[i - 1].id)); }
    if (i >= 0 && i < stages.length - 1) { bind("mod+ArrowRight", "Next stage", next); }
    const render = { meeting: renderMeeting, quiz: renderQuiz, learn: renderLearn, practice: renderLab, diy: renderLab, exam: renderLab, done: renderDone, debrief: renderDebrief }[stageId];
    if (!render) { location.replace("#/a/" + encodeURIComponent(a.id)); return; }
    render(stage, a, next, stageId, extra, ctx);
  }).catch((e) => clear(app, el("div", { class: "page" }, el("div", { class: "page-inner" }, errorState("Could not load the assignment: " + e.message, () => showAssignment(app, ctx, id, stageId, extra))))));
}

// openConcept shows a concept page in a drawer.
export function openConcept(c) {
  const back = el("div", { class: "drawer-back" });
  let release = () => {};
  const close = () => { release(); back.remove(); };
  release = pushOverlay(close);
  back.addEventListener("click", (e) => { if (e.target === back) { close(); } });
  const closeBtn = el("button", { class: "btn ghost small x", text: "Close", onclick: close });
  back.appendChild(el("aside", { class: "drawer", role: "dialog", "aria-modal": "true", "aria-label": c.title },
    el("div", { class: "drawer-head" }, el("span", { class: "ic", html: icons.book }), el("div", null, el("b", { text: c.title }), el("div", { class: "small muted", text: "Concept / " + c.id })), closeBtn),
    el("div", { class: "drawer-body" }, el("div", { class: "prose compact", html: c.body || "<p>" + (c.summary || "No page for this concept yet.") + "</p>" }))));
  document.body.appendChild(back);
  closeBtn.focus();
}
