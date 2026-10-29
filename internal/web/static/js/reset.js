// Progress reset: the confirmation dialog of one assignment or of every
// assignment, the reset and restore calls, and the toast with Undo. A reset
// keeps the history in the journal; it only stops the runs counting.

import { api } from "./api.js";
import { el, toast, plural } from "./dom.js";
import { pushOverlay, trapFocus } from "./keys.js";
import { remember } from "./badge.js";

// CONFIRM_WORD is what the reset of everything asks the learner to type.
export const CONFIRM_WORD = "reset";

// summary is the line that names what changed, as kubedrill progress
// prints it.
export function summary(ch, verb) {
  const scope = ch.all ? "Every assignment" : ch.id;
  const runs = plural(ch.runs, "run") + (verb === "reset" ? (ch.runs === 1 ? " no longer counts" : " no longer count") : (ch.runs === 1 ? " counts again" : " count again"));
  const stages = verb === "reset" ? (ch.stages ? "stage progress cleared" : "no stage progress to clear") : (ch.stages ? "stage progress restored" : "no stage progress to restore");
  return scope + ": " + runs + "; " + stages;
}

// seen stores the badge states after a reset or a restore, so the result
// page does not announce the change as earned or lost.
function seen() {
  return api.get("/api/catalog").then((c) => { remember(c); }).catch(() => {});
}

// refresh draws the current screen again from the server.
function refresh() {
  window.dispatchEvent(new HashChangeEvent("hashchange"));
}

function restore(scope) {
  api.post("/api/progress/restore", scope)
    .then((ch) => seen().then(() => { toast(summary(ch, "restore") + "."); refresh(); }))
    .catch((e) => toast(e.message, true));
}

// openReset shows the reset dialog. a is the assignment ({ id, title }) or
// null for every assignment; opener gets the focus back when the dialog
// closes. After the reset the screen is drawn again and a toast offers
// Undo; with "Show the welcome again" the welcome flow opens instead.
export function openReset(a, opener) {
  const all = !a;
  const back = el("div", { class: "modal-back", role: "dialog", "aria-modal": "true", "aria-labelledby": "reset-title" });
  let release = () => {}, untrap = () => {}, open = true;
  const close = () => {
    if (!open) { return; }
    open = false;
    release();
    untrap();
    back.remove();
    if (opener && document.body.contains(opener)) { opener.focus(); }
  };
  release = pushOverlay(close);
  back.addEventListener("click", (e) => { if (e.target === back) { close(); } });
  const keep = el("button", { class: "btn", type: "button", text: "Keep my progress", onclick: close });
  const go = el("button", { class: "btn danger", type: "button", text: all ? "Reset all progress" : "Reset progress" });
  const word = all ? el("input", { class: "input", type: "text", autocomplete: "off", spellcheck: "false", "aria-describedby": "reset-word-help", "aria-label": "Type " + CONFIRM_WORD + " to confirm" }) : null;
  const welcome = all ? el("input", { type: "checkbox", id: "reset-welcome" }) : null;
  const error = el("p", { class: "reset-error", role: "alert", hidden: true });
  const armed = () => !word || word.value.trim().toLowerCase() === CONFIRM_WORD;
  go.disabled = !armed();
  if (word) {
    word.addEventListener("input", () => { go.disabled = !armed(); });
    word.addEventListener("keydown", (e) => { if (e.key === "Enter" && armed()) { e.preventDefault(); go.click(); } });
  }
  const scope = all ? { all: true } : { id: a.id };
  go.addEventListener("click", () => {
    if (!armed()) { return; }
    go.disabled = true;
    keep.disabled = true;
    api.post("/api/progress/reset", Object.assign({}, scope, welcome && welcome.checked ? { welcome: true } : {}))
      .then((ch) => seen().then(() => {
        close();
        toast(summary(ch, "reset") + ".", false, { label: "Undo", run: () => restore(scope) });
        if (welcome && welcome.checked) { location.hash = "#/welcome"; } else { refresh(); }
      }))
      .catch((e) => {
        keep.disabled = false;
        go.disabled = !armed();
        error.hidden = false;
        error.textContent = e.message;
      });
  });
  const what = el("ul", { class: "reset-list" },
    el("li", { text: (all ? "Every run so far stops" : "Its runs so far stop") + " counting toward clean solves, mastery and badges." }),
    el("li", { text: all ? "Every stage checkmark is cleared." : "Its stage checkmarks are cleared: meeting, quiz, learn, practice, Do it yourself and exam." }),
    el("li", { text: "The history stays in the journal: kubedrill journal and the debriefs still show every run." }));
  back.appendChild(el("div", { class: "modal narrow reset" },
    el("div", { class: "eyebrow", text: all ? "All progress" : "Progress / " + a.id }),
    el("h2", { class: "modal-title", id: "reset-title", text: all ? "Start over from zero?" : "Start this assignment over?" }),
    all ? null : el("p", { class: "reset-subject", text: a.title }),
    what,
    word ? el("label", { class: "field reset-word" }, el("span", { id: "reset-word-help", text: "Type " + CONFIRM_WORD + " to confirm" }), word) : null,
    welcome ? el("label", { class: "check-row" }, welcome, el("span", { text: "Show the welcome again" })) : null,
    el("p", { class: "small muted", text: "Undo stays available right after the reset; kubedrill progress restore " + (all ? "--all" : a.id) + " undoes it later." }),
    error,
    el("div", { class: "modal-actions" }, keep, go)));
  document.body.appendChild(back);
  untrap = trapFocus(back);
  (word || keep).focus();
  return close;
}
