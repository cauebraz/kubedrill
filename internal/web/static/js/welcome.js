// The welcome flow: the five steps of the troubleshooting loop, each on a
// worked ticket with one pick, shown once before the workspace opens and
// reachable again from Home.

import { api } from "./api.js";
import { el, clear, store, toast } from "./dom.js";
import { bind } from "./keys.js";
import { REPO } from "./ui.js";

const TICKET = "From: Priya (payments team)\nPriority: high\n\nSince the 09:10 deploy the checkout page shows \"payment unavailable\". The gateway logs 502 for /api/pay. Nothing changed on our side, can platform take a look?";

const STEPS = [
  {
    title: "Describe the symptom as a fact",
    why: ["The first error you read becomes the cause you defend. Studies of novice troubleshooters (Vessey 1985; Gugerty and Olson 1986) found they form a cause from the first message, then collect evidence for it and add new faults while they hunt.",
      "A symptom written as a fact, with an object, a number and a time, is what you verify against at the end. The ticket text is not it."],
    example: { kind: "ticket", text: TICKET },
    prompt: "Which line is the symptom?",
    choices: [
      { say: "The gateway is broken again.", feedback: "A judgment, not an observation: no object, no number, nothing a command can confirm or deny." },
      { say: "deployment payments: 0/2 ready since 09:10; GET /api/pay returns 502 at the gateway", right: true, feedback: "Two facts with an object, a number and a time. At the end of the ticket, these two lines are what has to change." },
      { say: "Someone changed the Secret in the 09:10 deploy.", feedback: "A cause guessed before any observation. It may even be right, but it belongs to the next step, next to a second candidate." }
    ],
    where: "The ticket's Done when list is the symptom written as facts, and the checks grade exactly those facts."
  },
  {
    title: "Write down two causes before the second command",
    why: ["With one cause in mind, every command becomes a search for confirmation. With two, the next command has to tell them apart.",
      "The layers give you the candidates: Deployment, ReplicaSet, scheduler, kubelet, runtime, Service. A failure stops the chain at one layer, so \"how far did it get\" comes before \"why\"."],
    example: { kind: "shell", text: "$ kubectl get pods\nNAME                        READY   STATUS             RESTARTS   AGE\npayments-7d9f6c4b8-x2k9q    0/1     CrashLoopBackOff   6          9m\npayments-7d9f6c4b8-m4tz1    0/1     CrashLoopBackOff   6          9m" },
    prompt: "The pods restart in a loop. Which pair do you write down?",
    choices: [
      { say: "The process exits on start (runtime) / the liveness probe fails and the kubelet restarts a working process (kubelet)", right: true, feedback: "Two causes on two layers. Both produce CrashLoopBackOff, and the next command only has to say which one it is." },
      { say: "The process exits on start.", feedback: "One cause. The next command can only confirm it, and if it is wrong you will not notice for a while." },
      { say: "The deploy broke it / it worked yesterday", feedback: "Neither names an object or a layer, so no command can test them. Both are true and neither helps." }
    ],
    where: "kubedrill diagnose records the cause you commit to, and the debrief puts it next to the real one."
  },
  {
    title: "Pick the observation that splits them",
    why: ["Most of the time on call goes to commands that do not change what you believe. The useful command is the one whose output differs between your two causes.",
      "Restarting first destroys the evidence: a deleted pod takes its Last State and its previous logs with it."],
    prompt: "Which single command tells \"exits on start\" from \"killed by the probe\"?",
    choices: [
      { say: "kubectl describe pod payments-7d9f6c4b8-x2k9q, then read Last State and Events", right: true, feedback: "Exit code 1 and no \"Liveness probe failed\" event: the process exits on its own. One command removed one cause; kubectl logs --previous now reads what it printed before dying.",
        reveal: "Last State:   Terminated\n  Reason:     Error\n  Exit Code:  1\nEvents:\n  Warning  BackOff  kubelet  Back-off restarting failed container\n\n$ kubectl logs payments-7d9f6c4b8-x2k9q --previous\nError: key DB_URL not found in secret payments-db" },
      { say: "kubectl rollout restart deployment/payments", feedback: "A change before an observation. It discards Last State and the previous logs, and if the symptom goes away you learn nothing about why." },
      { say: "kubectl get pods again, a few more times", feedback: "The same output, more often. It does not differ between the two causes, so it cannot tell them apart." }
    ],
    where: "The hints follow this order: where to look, what to observe, what it means. None of them contains a change."
  },
  {
    title: "Change one thing",
    why: ["Two changes at once remove the symptom and leave the cause unknown: the rollout history shows two revisions minutes apart, and next week the page is down again.",
      "On call, stopping the impact comes first (a rollback is one change too), the root cause after it (Google SRE Workbook, incident response)."],
    example: { kind: "shell", text: "$ kubectl get secret payments-db -o jsonpath='{.data}' | jq keys\n[\n  \"DATABASE_URL\"\n]" },
    prompt: "The Secret has DATABASE_URL; the app reads DB_URL. What do you change?",
    choices: [
      { say: "Add the DB_URL key to the Secret, or roll the Deployment back to the previous revision. Nothing else.", right: true, feedback: "One change that removes one cause. If the symptom stays, you know the cause was elsewhere; if it goes, you know what fixed it." },
      { say: "Add the key, raise replicas to 4 and bump the memory limit while you are there", feedback: "Three changes. If it works you cannot say which one did it; if the new limit breaks something you cannot say which change broke it." },
      { say: "Delete the pods so they pick up whatever is there now", feedback: "The Secret still lacks the key, so the new pods fail the same way, and the previous logs are gone." }
    ],
    where: "The debrief shows whether your first change hit an object the fix belongs to. It comes from the audit log and never touches the grade."
  },
  {
    title: "Verify against the symptom, not the change",
    why: ["\"The Secret is updated\" is a fact about your change. The symptom was 0/2 ready and 502 at the gateway.",
      "A Deployment does not restart its pods when a Secret changes, so the change can be correct and the outage continue until something restarts them."],
    prompt: "Which observation closes the ticket?",
    choices: [
      { say: "kubectl get deployment payments shows READY 2/2, and a request through the gateway returns 200 for /api/pay", right: true, feedback: "The two lines from step one, observed again. That is the only check that means the customer is served." },
      { say: "kubectl get secret payments-db -o yaml shows the DB_URL key", feedback: "It verifies the change. The pods that crashed may still be running the old environment." },
      { say: "The pods restarted after the change", feedback: "Restarting is not serving. A pod can restart into the same crash." }
    ],
    where: "The checks grade the end state the ticket asked for and nothing about how you got there. Several fixes pass."
  }
];

const MAP = [
  ["Symptom", "the ticket's Done when list and the checks"],
  ["Two causes", "kubedrill diagnose, and the mentor before the last hint"],
  ["Splitting observation", "hints 1 to 3: where to look, what to observe, what it means"],
  ["One change", "the trajectory verdict in the debrief, from the audit log"],
  ["Verify", "Validate, which runs the same checks as kubedrill check"]
];

export function showWelcome(app, ctx, onDone) {
  ctx.crumbs([{ text: "Welcome" }]);
  const key = "kd:welcome";
  let i = Math.min(Math.max(store(key) || 0, 0), STEPS.length + 1);
  let tried = [];
  let solved = false;
  const page = el("div", { class: "page welcome" });
  const inner = el("div", { class: "welcome-inner" });
  page.appendChild(inner);
  clear(app, page);

  const finish = () => {
    store(key, null);
    api.post("/api/onboarded").then(onDone).catch((e) => toast(e.message, true));
  };
  const go = (n) => { i = n; tried = []; solved = false; store(key, i); draw(); window.scrollTo(0, 0); };

  function dots() {
    const n = i >= 1 && i <= STEPS.length ? i : 0;
    return el("ol", { class: "dots", "aria-label": n ? "Step " + n + " of " + STEPS.length : "Five steps" },
      STEPS.map((_, k) => el("li", { class: k + 1 < i ? "done" : k + 1 === i ? "on" : "", "aria-current": k + 1 === i ? "step" : null })));
  }
  function top() {
    return el("div", { class: "welcome-top" },
      el("div", { class: "eyebrow", text: i === 0 ? "Welcome to kubedrill" : i > STEPS.length ? "The method, in the product" : "Before your first ticket" }),
      dots(),
      i > STEPS.length ? null : el("button", { class: "link small skip-flow", text: "Skip to the workspace", onclick: finish }));
  }
  function example(ex) {
    if (!ex) { return null; }
    if (ex.kind === "ticket") { return el("blockquote", { class: "ticket-quote", text: ex.text }); }
    return el("pre", { class: "shell" }, el("code", { text: ex.text }));
  }

  function drawIntro() {
    const start = el("button", { class: "btn primary", text: "Start", onclick: () => go(1) });
    clear(inner, top(),
      el("h1", null, "You will get tickets, ", el("em", { text: "not lessons." })),
      el("p", { class: "why", text: "Each assignment plants a real problem in a Kubernetes cluster on your machine. A customer opens a ticket, you fix it with kubectl in a terminal on this page, and kubedrill grades the end state: any fix that works passes. Hints never lower the grade." }),
      el("p", { class: "why", text: "Every ticket here follows one method, five steps. The next five screens show each step on a worked ticket and why it exists. About three minutes, then the workspace opens." }),
      el("p", { class: "evidence", text: "Technicians trained in a fixed loop like this one solved about twice as many faults, in less time, than those trained the usual way (Schaafstal, Schraagen and van Berlo, 2000)." }),
      el("div", { class: "welcome-actions" }, start, el("span", { class: "small muted" }, el("kbd", { class: "kbd", text: "Enter" }))));
    start.focus();
  }

  function drawStep() {
    const s = STEPS[i - 1];
    const feedback = el("div", { class: "feedback-slot", "aria-live": "polite" });
    const reveal = el("div", { class: "reveal" });
    const cont = el("button", { class: "btn primary", text: i < STEPS.length ? "Next step" : "See where this lives", hidden: true, onclick: () => go(i + 1) });
    const buttons = s.choices.map((c, ci) => el("button", { class: "choice", onclick: () => pick(ci) }, el("kbd", { class: "kbd", text: String(ci + 1) }), el("span", { text: c.say })));
    function pick(ci) {
      const c = s.choices[ci];
      if (solved || tried.includes(ci)) { return; }
      tried.push(ci);
      if (c.right) {
        solved = true;
        buttons.forEach((b, k) => { b.disabled = k !== ci; b.classList.toggle("right", k === ci); });
        clear(feedback, el("div", { class: "feedback good" }, el("div", null, el("b", { text: "Why" }), c.feedback)));
        if (c.reveal) { clear(reveal, el("pre", { class: "shell" }, el("code", { text: c.reveal }))); }
        cont.hidden = false;
        cont.focus({ preventScroll: true });
        feedback.scrollIntoView({ block: "nearest" });
      } else {
        buttons[ci].disabled = true;
        buttons[ci].classList.add("wrong");
        clear(feedback, el("div", { class: "feedback retry" }, el("div", null, el("b", { text: "Not this one" }), c.feedback)));
      }
    }
    current = pick;
    const title = el("h1", { text: s.title, tabindex: "-1" });
    clear(inner, top(),
      el("div", { class: "step-num", text: "Step " + i + " of " + STEPS.length }),
      title,
      s.why.map((t) => el("p", { class: "why", text: t })),
      example(s.example),
      el("div", { class: "pick", role: "group", "aria-label": s.prompt },
        el("div", { class: "prompt", text: s.prompt }),
        buttons),
      feedback, reveal,
      el("div", { class: "where" }, el("b", { text: "In kubedrill" }), s.where),
      el("div", { class: "welcome-actions" },
        cont,
        el("span", { class: "small muted hint-key" }, el("kbd", { class: "kbd", text: "1" }), " to ", el("kbd", { class: "kbd", text: "3" }), " pick"),
        el("button", { class: "btn ghost small", text: "Back", onclick: () => go(i - 1) })));
    title.focus({ preventScroll: true });
  }

  function drawClose() {
    const open = el("button", { class: "btn primary", text: "Open kubedrill", onclick: finish });
    clear(inner, top(),
      el("h1", null, "That is the whole loop."),
      el("p", { class: "why", text: "Every screen in the workspace is one of the five steps, so you will meet them again on the first ticket:" }),
      el("ol", { class: "map" }, MAP.map(([name, where], k) => el("li", null, el("span", { class: "n", text: String(k + 1) }), el("span", null, el("b", { text: name + ": " }), where)))),
      el("p", { class: "why", text: "Each assignment starts with a meeting with the customer and a quiz, so the loop begins before you touch the cluster. After the guided run you solve a second variant with no guide, then an exam rehearsal against the clock." }),
      el("p", { class: "evidence" }, "kubedrill learn troubleshooting-loop, in the terminal, reads the evidence behind the method. kubedrill is open source: the scenarios, the grader and this page live at ", el("a", { class: "link", href: REPO, target: "_blank", rel: "noopener", text: "github.com/cauebraz/kubedrill" }), "."),
      el("div", { class: "welcome-actions" }, open, el("span", { class: "small muted" }, el("kbd", { class: "kbd", text: "Enter" })), el("button", { class: "btn ghost small", text: "Back", onclick: () => go(STEPS.length) })));
    open.focus();
  }

  let current = null;
  function draw() {
    current = null;
    if (i === 0) { drawIntro(); } else if (i > STEPS.length) { drawClose(); } else { drawStep(); }
  }

  for (let n = 1; n <= 3; n++) { bind(String(n), n === 1 ? "Pick an answer (1 to 3)" : "", () => { if (current) { current(n - 1); } }); }
  bind("Enter", "Continue", () => {
    if (i === 0) { go(1); } else if (i > STEPS.length) { finish(); } else if (solved) { go(i + 1); }
  });
  draw();
}
