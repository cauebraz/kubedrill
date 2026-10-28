// The debrief of a passed run and the result page of an assignment.

import { api } from "./api.js";
import { el, clear, toast, store, duration } from "./dom.js";
import { bind } from "./keys.js";
import { masteryMeter, masteryNeed, cleanVariantsText, skeleton, errorState } from "./ui.js";
import { badgeSVG, allBadges, remember, STATE_WORDS, KIND_WORDS } from "./badge.js";

const VERDICTS = {
  "root-cause": ["good", "Root cause", "Your first change hit an object the fix belongs to."],
  "symptom-only": ["warn", "Symptom only", "Your changes worked around the symptom; none touched the object the fix belongs to first."],
  "no-writes": ["", "No changes", "No write reached the API server during the run."],
  "unknown": ["", "Unknown", "The audit log was off, so the trajectory is not available."]
};

export function renderDebrief(stage, a, _next, _stageId, episodeId) {
  clear(stage, el("div", { class: "stage-inner" }, skeleton(6)));
  if (store("kd:exam:" + a.id) === episodeId) { store("kd:exam:" + a.id, null); }
  api.get("/api/debrief/" + encodeURIComponent(episodeId)).then((d) => {
    const after = d.stage === "practice" && !(a.progress.diy && a.progress.diy.done) ? "diy" : "done";
    const solved = d.outcome === "solved";
    const exam = d.stage === "exam";
    const title = solved ? (exam ? "Exam passed" : "The customer is happy") : d.outcome === "timed out" ? "Time is up" : exam ? "Exam not passed" : "Run ended";
    const lead = solved
      ? (exam ? "Passed in " : "Solved in ") + duration(d.seconds) + " (expected " + d.minutes + " min)" + (exam ? "" : ", " + d.hints + " " + (d.hints === 1 ? "hint" : "hints")) + ". Take a minute on how you got there: it is what transfers to the next ticket."
      : "The run ended after " + duration(d.seconds) + " without every check passing. The real cause and the walkthrough are below" + (exam ? "; the next rehearsal draws a variant again." : ".");
    clear(stage, el("div", { class: "stage-inner debrief" },
      el("div", { class: "debrief-hero" },
        el("div", { class: "result-icon " + (solved ? "ok" : "no"), text: solved ? "\u2713" : "!" }),
        el("div", null,
          el("div", { class: "eyebrow", text: exam ? "Exam debrief" : "Debrief" }),
          el("h1", { text: title }),
          el("p", { class: "muted", text: lead }))),
      el("div", { class: "debrief-grid" },
        compareCard(d),
        trajectoryCard(d)),
      d.solution ? el("details", { class: "card pad walkthrough" }, el("summary", { text: "Read the full walkthrough" }), el("div", { class: "prose compact", html: d.solution })) : null,
      reflectionCard(d, () => { location.hash = "#/a/" + encodeURIComponent(a.id) + "/" + after; })));
  }).catch((e) => clear(stage, el("div", { class: "stage-inner" }, errorState("Could not load the debrief: " + e.message))));
}

const NO_DIAGNOSIS = {
  exam: "None recorded; an exam has no mentor. kubedrill diagnose in the terminal records one.",
  practice: "None recorded; in the guided run it is optional. From the Do it yourself run on, name the cause in the mentor panel before you change anything: the last hint waits for it.",
  other: "None recorded. Next time, name the cause in the mentor panel before you change anything: committing to a cause is half the method."
};

function compareCard(d) {
  return el("section", { class: "card pad" },
    el("div", { class: "eyebrow muted", text: "Diagnosis" }),
    el("div", { class: "compare" },
      el("div", null, el("b", { text: "Your diagnosis" }), d.diagnosis || NO_DIAGNOSIS[d.stage === "exam" || d.stage === "practice" ? d.stage : "other"]),
      el("div", null, el("b", { text: "The real cause" }), d.rootCause || "-")));
}

function trajectoryCard(d) {
  const [cls, label, text] = VERDICTS[d.verdict] || VERDICTS.unknown;
  const first = !d.firstWrite ? "none recorded"
    : d.firstWriteKind === "fix-target" ? d.firstWrite + " (a fix target)"
      : d.firstWriteKind === "metadata-only" ? d.firstWrite + " (a fix target, but only labels or annotations changed)"
        : d.firstWrite + " (not a fix target)";
  return el("section", { class: "card pad" },
    el("div", { class: "eyebrow muted", text: "Trajectory" }),
    d.telemetry === "none"
      ? el("p", { class: "small muted", text: "No telemetry for this run: the audit log and the shell log were not available." })
      : el("dl", { class: "facts" },
        el("dt", { text: "Verdict" }), el("dd", null, el("span", { class: "pill " + cls, text: label }), el("span", { class: "small muted", text: " " + text })),
        el("dt", { text: "API reads before the first change" }), el("dd", null, el("b", { text: String(d.readsBeforeWrite) }), el("span", { class: "small muted", text: d.readsBeforeWrite < 2 ? "  few: observe before you change" : "" })),
        el("dt", { text: "First change" }), el("dd", { text: first }),
        el("dt", { text: "Fix targets" }), el("dd", { class: "mono", text: (d.fixTargets || []).join(", ") || "-" })));
}

function reflectionCard(d, onDone) {
  const ans = d.answers || {};
  const matched = el("div", { class: "segmented", role: "radiogroup", "aria-label": "Did your diagnosis match?" });
  let value = ans.matched === undefined ? null : ans.matched;
  const drawMatched = () => clear(matched, [[true, "Yes"], [false, "No"]].map(([v, t]) => el("button", { class: "seg" + (value === v ? " on" : ""), role: "radio", "aria-checked": value === v ? "true" : "false", text: t, onclick: () => { value = v; drawMatched(); } })));
  drawMatched();
  const q1 = el("textarea", { maxlength: "500", placeholder: "For example: the Failed to pull image event named the registry host", "aria-label": "Which observation would have ruled out your first hypothesis earlier?" });
  q1.value = ans.ruledOutEarlier || "";
  const q2 = el("textarea", { maxlength: "500", placeholder: "For example: kubectl get events before anything else", "aria-label": "Next time I check first" });
  q2.value = ans.nextTimeFirst || "";
  const save = el("button", { class: "btn primary", text: "Save and continue" });
  const skip = el("button", { class: "btn ghost", text: "Skip for now" });
  const submit = () => {
    save.disabled = true;
    api.post("/api/debrief/" + encodeURIComponent(d.episodeId), { matched: d.diagnosis ? value : null, ruledOutEarlier: q1.value, nextTimeFirst: q2.value })
      .then(() => { toast("Debrief recorded; kubedrill debrief shows it in the terminal too."); onDone(); })
      .catch((e) => { save.disabled = false; toast(e.message, true); });
  };
  save.addEventListener("click", submit);
  skip.addEventListener("click", onDone);
  bind("mod+Enter", "Save the debrief", submit);
  return el("section", { class: "card pad reflection" },
    el("div", { class: "eyebrow muted", text: "Reflection" }),
    d.diagnosis ? el("label", { class: "q" }, el("span", { text: "Did your diagnosis match the real cause?" }), matched) : null,
    el("label", { class: "q" }, el("span", { text: "Which observation would have ruled out your first hypothesis earlier?" }), q1),
    el("label", { class: "q" }, el("span", { text: "Next time I check first:" }), q2),
    el("div", { class: "modal-actions" }, skip, save));
}

export function renderDone(stage, a) {
  const p = a.progress;
  const earned = p.diy && p.diy.done;
  const mb = a.moduleBadge && a.moduleBadge.code ? a.moduleBadge : null;
  const badge = el("a", { class: "result-badge", href: "#/badges" }, mb ? badgeSVG("module", mb, { size: 132, level: a.level }) : null);
  const badgeLine = el("div", { class: "badge-line" }, mb ? [el("b", { text: mb.code + " " + a.moduleTitle + ": " + STATE_WORDS[mb.state].toLowerCase() }), el("span", { class: "small muted", text: mb.caption })] : null);
  const changes = el("ul", { class: "badge-changes", hidden: true });
  api.get("/api/catalog").then((c) => {
    const before = remember(c);
    const changed = allBadges(c).filter((x) => before[x.key] && before[x.key] !== x.badge.state);
    const mine = mb ? changed.find((x) => x.key === "module:" + mb.code) : null;
    if (mine) {
      clear(badge, badgeSVG("module", mine.badge, { size: 132, level: mine.level }));
      clear(badgeLine,
        el("span", { class: "eyebrow", text: "Badge " + STATE_WORDS[mine.badge.state].toLowerCase() }),
        el("b", { text: mine.badge.code + " " + mine.title + ": " + STATE_WORDS[before[mine.key]].toLowerCase() + " -> " + STATE_WORDS[mine.badge.state].toLowerCase() }),
        el("span", { class: "small muted", text: mine.badge.caption }));
    }
    const others = changed.filter((x) => x !== mine);
    if (others.length) {
      changes.hidden = false;
      clear(changes, others.map((x) => el("li", null,
        badgeSVG(x.kind, x.badge, { size: 40, level: x.level }),
        el("span", null, el("b", { text: KIND_WORDS[x.kind] + " " + (x.kind === "track" ? x.title : x.badge.code + " " + x.title) + ": " + STATE_WORDS[x.badge.state].toLowerCase() }), " ", el("span", { class: "muted", text: x.badge.caption })))));
    }
  }).catch(() => {});
  const row = (label, r, href) => el("li", null,
    el("span", { text: label }),
    el("span", { class: "row-end" },
      el("b", { class: r && r.done ? "" : "todo", text: r && r.done ? (r.total ? r.score + "/" + r.total : "done") : "not yet" }),
      href ? el("a", { class: "link small", href, text: r && r.done ? "redo" : "open" }) : null));
  const base = "#/a/" + encodeURIComponent(a.id) + "/";
  const redoMeeting = () => { store("kd:meeting:" + a.id, null); location.hash = base + "meeting"; };
  const redoQuiz = () => { store("kd:quiz:" + a.id, null); location.hash = base + "quiz"; };
  const m = a.mastery;
  stage.appendChild(el("div", { class: "stage-inner" },
    el("div", { class: "card done-card" },
      badge, badgeLine, changes,
      el("div", { class: "eyebrow", text: earned ? "Assignment complete" : "Not complete yet" }),
      el("h1", { text: a.title }),
      el("p", { text: earned ? "Fixed with the guide, then again on your own on another variant. That is the whole loop." : "Pass the Do it yourself run to complete this assignment." }),
      el("ul", { class: "stage-list" },
        a.simulation ? [row("Meeting, best questions first try", p.meeting, null), row("Quiz, right first try", p.quiz, null)] : null,
        row("Learn", p.learn, base + "learn"), row("Guided practice", p.practice, base + "practice"), row("Do it yourself", p.diy, base + "diy"),
        row("Exam rehearsal passed", p.exam, base + "exam")),
      a.simulation ? el("div", { class: "redo" }, el("button", { class: "btn small", text: "Redo the meeting", onclick: redoMeeting }), el("button", { class: "btn small", text: "Redo the quiz", onclick: redoQuiz })) : null),
    el("section", { class: "card pad mastery-card" },
      el("div", { class: "mastery-head" },
        el("div", null, el("div", { class: "eyebrow muted", text: "Mastery" }), el("h3", { text: m.mastered ? "Mastered" : "Not mastered yet" })),
        masteryMeter(m)),
      el("p", { class: "small muted", text: "Complete marks that you finished the assignment once. The module badge counts clean solves: no guide, no hint, within " + a.minutes + " min. Mastery is the long game: 3 clean solves (no hint, within " + a.minutes + " min) in your last 4 attempts, across " + masteryNeed(m) + " different variants and at least 2 sessions. kubedrill then brings it back for spaced review." }),
      el("dl", { class: "facts" },
        el("dt", { text: "Clean solves in the last attempts" }), el("dd", { text: m.cleanInLast + " of " + (m.last || 0) + " (3 needed)" }),
        el("dt", { text: "Variants among the clean solves" }), el("dd", { text: cleanVariantsText(m) }),
        el("dt", { text: "Variants solved, any run" }), el("dd", { text: m.variantsSolved + " of " + m.variants + "; mastery needs " + masteryNeed(m) + " different variants among the clean solves" }),
        el("dt", { text: "Attempts" }), el("dd", { text: m.attempts + ", " + m.solved + " solved" })),
      el("div", { class: "done-actions" },
        el("a", { class: "btn", href: base + "diy", text: "Run DIY again on another variant" }),
        el("a", { class: "btn primary", href: "#/", text: "Back home" })))));
  bind("Enter", "Back home", () => { location.hash = "#/"; });
}
