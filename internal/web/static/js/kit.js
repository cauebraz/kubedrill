// The component kit: every primitive of the design system in its states,
// for reviewing a style change in both themes. Linked from the top bar when
// kubedrill web runs with --static-dir.

import { el, clear, toast } from "./dom.js";
import { ring, statusPill, masteryMeter, stageDots, assignmentRow, skeleton, errorState } from "./ui.js";

export function showKit(app, ctx) {
  ctx.crumbs([{ href: "#/", text: "Home" }, { text: "Component kit" }]);
  const card = { id: "000-example", title: "Example assignment title in the reporter's words", minutes: 12, simulation: true, status: "solved", requires: [], progress: { meeting: { done: true }, quiz: { done: true }, learn: { done: true }, practice: { done: false }, diy: { done: false } }, mastery: { variants: 3, variantsSolved: 1, cleanInLast: 1, last: 2, mastered: false } };
  const section = (title, ...body) => el("section", { class: "kit-section" }, el("div", { class: "eyebrow muted", text: title }), el("div", { class: "kit-row" }, body));
  clear(app, el("div", { class: "page" }, el("div", { class: "page-inner" },
    el("header", { class: "page-head" }, el("div", { class: "eyebrow", text: "Design system" }), el("h1", { text: "Component kit" }),
      el("p", { class: "lede", text: "Tokens and components from app.css. Switch the theme in the top bar to review both." })),
    section("Surfaces", ["--bg", "--bg-1", "--bg-2", "--bg-3"].map((v) => el("div", { class: "swatch", style: "background: var(" + v + ")" }, el("span", { text: v })))),
    section("Text", ["--fg", "--fg-1", "--fg-2", "--fg-3", "--brand", "--good", "--warn", "--bad", "--info"].map((v) => el("span", { class: "swatch-text", style: "color: var(" + v + ")", text: v }))),
    section("Buttons",
      el("button", { class: "btn primary", text: "Primary" }), el("button", { class: "btn", text: "Default" }), el("button", { class: "btn ghost", text: "Ghost" }),
      el("button", { class: "btn small", text: "Small" }), el("button", { class: "btn", disabled: true, text: "Disabled" }),
      el("button", { class: "btn", text: "Toast", onclick: () => toast("A toast message") }), el("button", { class: "btn", text: "Error toast", onclick: () => toast("An error toast", true) })),
    section("Pills", statusPill("todo"), statusPill("active"), statusPill("solved"), statusPill("mastered"), el("span", { class: "pill bad", text: "fail" }), el("span", { class: "pill good live", text: "In meeting" })),
    section("Progress", ring(0, 3), ring(2, 3), ring(3, 3, true), stageDots(card.progress, true), masteryMeter(card.mastery), masteryMeter({ variants: 3, variantsSolved: 3, mastered: true, cleanInLast: 3, last: 4 }),
      el("div", { class: "meter", style: "width:160px" }, el("div", { style: "width:60%" }))),
    section("Keyboard", el("kbd", { class: "kbd", text: "1" }), el("kbd", { class: "kbd", text: "Enter" }), el("kbd", { class: "kbd", text: "Cmd Enter" })),
    section("Callouts", el("div", { class: "callout", text: "A warning callout: something the learner should know before acting." }), el("div", { class: "callout info", text: "An info callout: context that helps." })),
    section("Checks", ["pass", "fail", "skipped", "error"].map((s) => el("span", { class: "chk " + s }, el("span", { text: "check " + s })))),
    section("Assignment row", el("div", { class: "card module", style: "width:100%" }, el("div", { class: "assign-list" }, assignmentRow(card, {}), assignmentRow(Object.assign({}, card, { title: "Locked: needs another first", status: "todo", requires: ["999-missing"] }), { "999-missing": { status: "todo" } }, true)))),
    section("Loading and errors", el("div", { style: "width:100%" }, skeleton(3)), el("div", { style: "width:100%" }, errorState("The message of the failure, as the API returned it.", () => toast("retry")))))));
}
