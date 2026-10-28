// Stage 2: the mentor's quiz, one question at a time, resumable.

import { api } from "./api.js";
import { el, clear, store, toast, shuffledOrder } from "./dom.js";
import { bind } from "./keys.js";
import { stageHead } from "./meeting.js";

export function renderQuiz(stage, a, next) {
  const sim = a.simulation;
  if (!sim) { next(); return; }
  const key = "kd:quiz:" + a.id;
  let st = store(key) || { i: 0, firstTry: 0, wrong: [] };
  if (st.i > sim.quiz.length) { st = { i: 0, firstTry: 0, wrong: [] }; }
  const card = el("div", { class: "card quiz-card", "aria-live": "polite" });
  let buttons = [];
  let advance = null;
  stage.appendChild(el("div", { class: "stage-inner" },
    stageHead("Stage 2 / Check understanding", "Before you touch the cluster", "The mentor asks what the symptoms mean. A wrong answer explains why; try again until you find the right one.",
      el("button", { class: "btn ghost small", text: "Restart quiz", onclick: () => { st = { i: 0, firstTry: 0, wrong: [] }; store(key, null); draw(); } })),
    card));

  function finish() {
    api.post("/api/progress", { id: a.id, stage: "quiz", score: st.firstTry, total: sim.quiz.length }).then(next).catch((e) => toast(e.message, true));
  }
  function draw() {
    clear(card);
    buttons = [];
    draw.answer = null;
    if (st.i >= sim.quiz.length) {
      const btn = el("button", { class: "btn", text: "Continue to Learn", onclick: finish });
      advance = finish;
      card.append(el("div", { class: "eyebrow", text: "Quiz complete" }),
        el("div", { class: "q", text: st.firstTry + " of " + sim.quiz.length + " right on the first try." }),
        el("div", { class: "quiz-actions" }, btn));
      btn.focus();
      return;
    }
    advance = null;
    const q = sim.quiz[st.i];
    const explain = el("div", { class: "explain" });
    const nextBtn = el("button", { class: "btn", text: st.i + 1 < sim.quiz.length ? "Next question" : "See the result", hidden: true, onclick: goNext });
    card.append(
      el("div", { class: "progress-row", role: "img", "aria-label": "Question " + (st.i + 1) + " of " + sim.quiz.length }, sim.quiz.map((_, k) => el("i", { class: k <= st.i ? "on" : "" }))),
      el("div", { class: "eyebrow muted", text: "Question " + (st.i + 1) + " of " + sim.quiz.length }),
      el("div", { class: "q", text: q.question }));
    const wrongHere = st.wrong[st.i] || [];
    const shown = shuffledOrder(q.choices.length, a.id + "\n" + q.question + "\n" + q.choices.map((c) => c.text).join("\n"));
    buttons = new Array(q.choices.length);
    shown.forEach((ci, pos) => {
      const c = q.choices[ci];
      const b = el("button", { class: "choice" + (wrongHere.includes(ci) ? " wrong" : ""), disabled: wrongHere.includes(ci), onclick: () => answer(ci) }, el("kbd", { class: "kbd", text: String(pos + 1) }), el("span", { text: c.text }));
      buttons[ci] = b;
      card.appendChild(b);
    });
    card.append(explain, el("div", { class: "quiz-actions" }, nextBtn));
    if (st.ok === st.i) {
      const ci = q.choices.findIndex((c) => c.correct);
      buttons[ci].classList.add("right");
      buttons.forEach((x, k) => { if (k !== ci) { x.disabled = true; } });
      explain.textContent = q.choices[ci].explain;
      nextBtn.hidden = false;
      advance = goNext;
    }
    function answer(ci) {
      const c = q.choices[ci];
      if (!c || buttons[ci].disabled || !nextBtn.hidden) { return; }
      explain.textContent = c.explain;
      if (c.correct) {
        buttons[ci].classList.add("right");
        if (!(st.wrong[st.i] || []).length) { st.firstTry++; }
        st.ok = st.i;
        buttons.forEach((x, k) => { if (k !== ci) { x.disabled = true; } });
        nextBtn.hidden = false;
        advance = goNext;
        nextBtn.focus();
      } else {
        st.wrong[st.i] = (st.wrong[st.i] || []).concat(ci);
        buttons[ci].classList.add("wrong");
        buttons[ci].disabled = true;
      }
      store(key, st);
    }
    function goNext() { st.i++; st.ok = -1; store(key, st); draw(); }
    draw.answer = (pos) => { if (pos < shown.length) { answer(shown[pos]); } };
  }
  for (let n = 1; n <= 9; n++) { bind(String(n), n === 1 ? "Pick an answer (1 to 9)" : "", () => draw.answer && draw.answer(n - 1)); }
  bind("Enter", "Next question or continue", () => { if (advance) { advance(); } });
  draw();
}
