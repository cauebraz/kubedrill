// Stage 1: the scripted meeting with the customer.

import { api } from "./api.js";
import { el, clear, initials, store, toast } from "./dom.js";
import { bind } from "./keys.js";

export function stageHead(eyebrow, title, text, action) {
  return el("div", { class: "stage-head" },
    el("div", null, el("div", { class: "eyebrow", text: eyebrow }), el("h1", { text: title }), text ? el("p", { text }) : null),
    action || null);
}

export function renderMeeting(stage, a, next) {
  const sim = a.simulation;
  if (!sim) { next(); return; }
  const key = "kd:meeting:" + a.id;
  const fresh = () => ({ turn: 0, tried: [], firstTry: 0, log: [], facts: [] });
  let st = store(key) || fresh();
  if (st.turn > sim.meeting.turns.length) { st = fresh(); }
  const cust = sim.customer;
  const total = sim.meeting.turns.length;
  const messages = el("div", { class: "messages", "aria-live": "polite", "aria-label": "Conversation" });
  const choices = el("div", { class: "choices" });
  const board = el("ul");
  const meter = el("div", { style: "width:0%" });
  const counter = el("span", { class: "planned" });

  const bubble = (who, text) => el("div", { class: "msg " + (who === "you" ? "you" : "customer") },
    who === "you" ? el("div", { class: "avatar sm you", text: "You" }) : el("div", { class: "avatar sm customer", text: initials(cust.name), "aria-hidden": "true" }),
    el("div", { class: "bubble" }, el("span", { class: "sr-only", text: who === "you" ? "You: " : cust.name + ": " }), text));
  const feedbackBox = (good, text) => el("div", { class: "feedback " + (good ? "good" : "retry") },
    el("div", { class: "avatar sm mentor", text: "M", "aria-hidden": "true" }),
    el("div", null, el("b", { text: good ? "Mentor / good call" : "Mentor / try another question" }), text));

  function drawLog() {
    clear(messages, bubble("customer", sim.meeting.opening));
    for (const m of st.log) { messages.appendChild(m.kind === "feedback" ? feedbackBox(m.good, m.text) : bubble(m.kind, m.text)); }
    if (st.turn >= total) { messages.appendChild(bubble("customer", sim.meeting.closing)); }
    messages.scrollTop = messages.scrollHeight;
  }
  function drawBoard(newFact) {
    clear(board);
    st.facts.forEach((f, i) => board.appendChild(el("li", { class: newFact && i === st.facts.length - 1 ? "new" : "", text: f })));
    for (let i = st.facts.length; i < total; i++) { board.appendChild(el("li", { class: "empty", text: "Requirement " + (i + 1) + " not gathered yet" })); }
    meter.style.width = Math.round(100 * st.facts.length / total) + "%";
    counter.textContent = st.facts.length + "/" + total;
  }
  let choiceButtons = [];
  function drawChoices() {
    clear(choices);
    choiceButtons = [];
    if (st.turn >= total) {
      const btn = el("button", { class: "btn", text: "Continue to the quiz", onclick: finish });
      choices.appendChild(el("div", { class: "prompt", text: "Meeting complete: " + st.firstTry + " of " + total + " best questions on the first try" }));
      choices.appendChild(el("div", { class: "choices-done" }, btn, el("span", { class: "small muted" }, el("kbd", { class: "kbd", text: "Enter" }))));
      btn.focus();
      return;
    }
    const t = sim.meeting.turns[st.turn];
    choices.appendChild(el("div", { class: "prompt", text: (t.prompt || "Your next question") + " (" + (st.turn + 1) + " of " + total + ")" }));
    t.choices.forEach((c, ci) => {
      const b = el("button", { class: "choice", disabled: st.tried.includes(ci), onclick: () => pick(ci) }, el("kbd", { class: "kbd", text: String(ci + 1) }), el("span", { text: c.say }));
      choiceButtons.push(b);
      choices.appendChild(b);
    });
  }
  function pick(ci) {
    const c = sim.meeting.turns[st.turn].choices[ci];
    if (!c || st.tried.includes(ci)) { return; }
    st.log.push({ kind: "you", text: c.say }, { kind: "customer", text: c.reply }, { kind: "feedback", good: !!c.best, text: c.feedback });
    let newFact = false;
    if (c.best) {
      if (!st.tried.length) { st.firstTry++; }
      st.facts.push(c.fact);
      st.turn++;
      st.tried = [];
      newFact = true;
    } else {
      st.tried.push(ci);
    }
    store(key, st);
    drawLog(); drawBoard(newFact); drawChoices();
  }
  function finish() {
    api.post("/api/progress", { id: a.id, stage: "meeting", score: st.firstTry, total }).then(next).catch((e) => toast(e.message, true));
  }
  function restart() { store(key, null); st = fresh(); drawLog(); drawBoard(); drawChoices(); }

  for (let n = 1; n <= 9; n++) { bind(String(n), n === 1 ? "Pick a question (1 to 9)" : "", () => pick(n - 1)); }
  bind("Enter", "Continue when the meeting is over", () => { if (st.turn >= total) { finish(); } });

  stage.appendChild(el("div", { class: "stage-inner" },
    stageHead("Stage 1 / Meet the customer", "Find out what the customer needs", "Pick the questions a good engineer would ask. Each good question adds a requirement to your board; the mentor comments on every choice. Keys 1 to " + Math.max(...sim.meeting.turns.map((t) => t.choices.length)) + " pick an option.",
      el("button", { class: "btn ghost small", text: "Restart meeting", onclick: restart })),
    el("div", { class: "meeting" },
      el("div", { class: "card chat" },
        el("div", { class: "chat-head" },
          el("div", { class: "avatar customer", text: initials(cust.name), "aria-hidden": "true" }),
          el("div", { class: "who" }, el("b", { text: cust.name }), el("span", { text: cust.role + (cust.company ? ", " + cust.company : "") })),
          el("span", { class: "pill good live", text: "In meeting" })),
        messages, choices),
      el("aside", { class: "card board", "aria-label": "Requirements board" },
        el("h3", null, el("span", { text: "Requirements board" }), counter),
        el("div", { class: "meter" }, meter), board))));
  drawLog(); drawBoard(); drawChoices();
}
