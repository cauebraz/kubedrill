// Stages 4 and 5: the guided practice and the DIY run on a live namespace.

import { api, start } from "./api.js";
import { el, clear, store, toast, onLeave, duration, icons } from "./dom.js";
import { bind, pushOverlay } from "./keys.js";

export function renderLab(stage, a, next, stageId) {
  const route = location.hash;
  const examKey = "kd:exam:" + a.id;
  clear(stage, el("p", { class: "loading", text: "Checking the environment..." }));
  api.get("/api/session").then((s) => {
    if (location.hash !== route) { return; }
    const act = s.active;
    if (act && act.scenarioId === a.id && act.stage === stageId) { workspace(stage, a, stageId, act); return; }
    const ended = stageId === "exam" ? store(examKey) : null;
    if (ended && !(act && act.episodeId === ended)) {
      store(examKey, null);
      location.hash = debriefRoute(a, ended);
      return;
    }
    launchPanel(stage, a, stageId, act);
  }).catch((e) => { if (location.hash === route) { clear(stage, el("p", { class: "loading", text: e.message })); } });
}

function debriefRoute(a, episodeId) {
  return "#/a/" + encodeURIComponent(a.id) + "/debrief/" + encodeURIComponent(episodeId);
}

const LAUNCH = {
  practice: {
    eyebrow: "Stage 4 / Guided practice", title: "Fix it step by step on a live cluster", button: "Launch the practice environment",
    points: ["A fresh namespace on your local cluster with the problem planted.", "A terminal in the page, already pointed at the namespace, with kubectl, k9s and helm.", "The steps on the left; the ones that change the cluster turn green on their own."]
  },
  diy: {
    eyebrow: "Stage 5 / Do it yourself", title: "Now fix it on your own", button: "Launch the DIY environment",
    points: ["Same ticket, another variant: the planted cause or its details may differ from the practice run, so observe before you reuse its fix.", "No guide. The mentor gives hints, and the last one waits until you name a cause.", "Validate when you think the customer is happy; the checks grade the end state, so any fix that works passes."]
  },
  exam: {
    eyebrow: "Exam rehearsal", title: "Fix it against the clock", button: "Start the exam",
    points: ["A random variant in a time box of the expected minutes, like a CKA or CKAD task.", "No guide, no hints, no live checks. The terminal keeps only the exam tools (kubectl, helm, yq, jq, vim) and lists the documentation the exam allows.", "Submit grades once and ends the run; at the deadline the page submits for you. The walkthrough opens afterwards."]
  }
};

function launchPanel(stage, a, stageId, act) {
  const copy = LAUNCH[stageId];
  const btn = el("button", { class: "btn primary", text: copy.button });
  const steps = el("ol", { class: "launch-steps", "aria-live": "polite" });
  const other = act ? el("div", { class: "callout" },
    el("div", null, el("b", { text: act.scenarioId === a.id ? "A run of this assignment is in progress. " : act.scenarioId + " is active. " }),
      "Launching ends it and records it as failed. ",
      el("a", { class: "link", href: "#/a/" + encodeURIComponent(act.scenarioId) + "/" + (act.stage || "practice"), text: "Go back to it" }))) : null;
  const small = window.matchMedia("(max-width: 960px)").matches
    ? el("div", { class: "callout info", text: "The lab shows a terminal next to the steps; it needs a window at least 960 pixels wide." }) : null;
  const route = location.hash;
  const launch = () => {
    btn.disabled = true;
    btn.textContent = "Launching...";
    clear(steps);
    let last = null;
    start({ id: a.id, stage: stageId, replace: !!act }, (step) => {
      if (last) { last.classList.add("done"); }
      last = el("li", { class: "running", text: step });
      steps.appendChild(last);
    }).then(() => {
      if (last) { last.classList.add("done"); }
      unbind();
      if (location.hash === route) { renderLab(stage, a, null, stageId); }
    }).catch((e) => {
      if (last) { last.classList.add("failed"); }
      btn.disabled = false;
      btn.textContent = "Try again";
      toast(e.message, true);
    });
  };
  btn.addEventListener("click", launch);
  const unbind = bind("Enter", "Launch the environment", () => { if (btn.isConnected && !btn.disabled) { launch(); } });
  clear(stage, el("div", { class: "card launch" },
    el("div", { class: "eyebrow", text: copy.eyebrow }),
    el("h1", { text: copy.title }),
    stageId === "exam" ? el("p", { class: "lede", text: a.minutes + " minutes from the moment the namespace is ready." }) : null,
    el("ul", { class: "launch-points" }, copy.points.map((t) => el("li", { text: t }))),
    other, small,
    el("div", { class: "launch-actions" }, btn, el("span", { class: "small muted" }, el("kbd", { class: "kbd", text: "Enter" }), " about 20 seconds")),
    steps));
  btn.focus();
}

function workspace(stage, a, stageId, act) {
  const isDIY = stageId === "diy";
  const isExam = stageId === "exam";
  const guided = stageId === "practice" && act.guide && act.guide.length;
  const manualKey = "kd:guide:" + act.episodeId;
  const manual = store(manualKey) || [];
  const tabKey = "kd:tab:" + act.episodeId;
  let tab = store(tabKey) || (guided ? "steps" : "ticket");
  let lastChecks = [];
  stage.classList.add("lab-stage");
  onLeave(() => stage.classList.remove("lab-stage"));

  const sideTabs = el("div", { class: "tabs side-tabs", role: "tablist" });
  const sideBody = el("div", { class: "side-body" });
  const side = el("aside", { class: "lab-side" },
    el("div", { class: "lab-side-head" },
      el("div", { class: "eyebrow", text: LAUNCH[stageId].eyebrow }),
      el("h3", { text: a.title }),
      sideTabs),
    sideBody);
  const termBox = el("div", { class: "term" });
  const termState = el("span", { class: "state", text: "connecting" });
  const timer = isExam
    ? el("span", { class: "timer", role: "timer", "aria-label": "Time left in the exam", title: "Time left; the page submits at zero" })
    : el("span", { class: "timer", role: "timer", "aria-label": "Time since the run started", title: "Time since the run started, against the expected minutes" });
  const reconnect = el("button", { class: "term-btn", hidden: true, title: "Open a new terminal", html: icons.refresh + "<span>Reconnect</span>" });
  const checksBar = el("div", { class: "checks", role: "status", "aria-label": "Live checks" });
  const validateBtn = el("button", { class: "btn primary", title: isExam ? "Grade once and end the exam" : "Grade the namespace now" }, isExam ? "Submit " : "Validate ", el("kbd", { class: "kbd on-accent", text: navigator.platform.includes("Mac") ? "Cmd Enter" : "Ctrl Enter" }));
  const hintPill = el("span", { class: "pill warn", hidden: act.hintsUsed === 0, title: "Hinted attempts still count as solves, not as clean solves for mastery" });
  const mentorBtn = el("button", { class: "btn", hidden: isExam }, "Mentor");
  const updateMentor = () => {
    mentorBtn.textContent = act.hintsTotal ? "Mentor (" + act.hintsUsed + "/" + act.hintsTotal + ")" : "Mentor";
    hintPill.hidden = isExam || act.hintsUsed === 0;
    hintPill.textContent = act.hintsUsed + " hint" + (act.hintsUsed === 1 ? "" : "s") + " used";
  };
  updateMentor();
  clear(stage, el("div", { class: "lab" }, side,
    el("div", { class: "lab-main" },
      el("div", { class: "term-bar" },
        el("span", { class: "lights", "aria-hidden": "true" }, el("i"), el("i"), el("i")),
        el("span", null, "workstation ", el("span", { class: "ns", text: act.namespace })),
        timer, reconnect, termState),
      termBox),
    el("div", { class: "lab-foot" }, checksBar, el("div", { class: "foot-actions" }, hintPill, mentorBtn, validateBtn))));

  let term = startTerminal(termBox, termState, reconnect);
  reconnect.addEventListener("click", () => { term.close(); clear(termBox); term = startTerminal(termBox, termState, reconnect); });
  onLeave(() => term.close());

  const started = new Date(act.startedAt).getTime();
  const deadline = act.deadline ? new Date(act.deadline).getTime() : 0;
  let submitting = false, closeConfirm = null, gone = false;
  const warned = {};
  if (isExam) { store("kd:exam:" + a.id, act.episodeId); }
  const tick = () => {
    if (isExam && deadline) {
      const left = (deadline - Date.now()) / 1000;
      timer.textContent = duration(Math.max(0, left)) + " left";
      timer.classList.toggle("over", left < 120);
      for (const m of [5, 1]) {
        if (left <= m * 60 && left > m * 60 - 30 && !warned[m]) { warned[m] = true; toast(m + (m === 1 ? " minute" : " minutes") + " left; the page submits at zero."); }
      }
      if (left <= 0) { submitExam(true); }
      return;
    }
    const s = (Date.now() - started) / 1000;
    timer.textContent = duration(s) + " / " + act.minutes + " min";
    timer.classList.toggle("over", s > act.minutes * 60);
  };
  tick();
  const clock = setInterval(tick, 1000);
  onLeave(() => clearInterval(clock));

  const beforeUnload = (e) => { if (term.connected()) { e.preventDefault(); e.returnValue = ""; } };
  window.addEventListener("beforeunload", beforeUnload);
  onLeave(() => window.removeEventListener("beforeunload", beforeUnload));

  function drawTabs() {
    const list = [];
    if (guided) { list.push(["steps", "Steps"]); }
    list.push(["ticket", "Ticket"]);
    if (act.story) { list.push(["story", "Story"]); }
    if (!list.some(([id]) => id === tab)) { tab = list[0][0]; }
    clear(sideTabs, list.map(([id, label]) => el("button", { class: "tab" + (id === tab ? " on" : ""), role: "tab", "aria-selected": id === tab ? "true" : "false", text: label, onclick: () => { tab = id; store(tabKey, id); drawTabs(); drawSide(); } })));
  }

  function doneSteps() {
    const passed = Object.fromEntries(lastChecks.map((c) => [c.id, c.status === "pass"]));
    const done = act.guide.map((g, i) => g.check ? !!passed[g.check] : manual.includes(i));
    let later = false;
    for (let k = done.length - 1; k >= 0; k--) {
      if (later) { done[k] = true; }
      if (act.guide[k].check && done[k]) { later = true; }
    }
    return done;
  }

  function drawSide() {
    if (tab === "ticket") { clear(sideBody, el("div", { class: "ticket", html: act.ticket })); return; }
    if (tab === "story") { clear(sideBody, el("div", { class: "ticket", html: act.story })); return; }
    const done = doneSteps();
    const current = done.indexOf(false);
    clear(sideBody,
      el("p", { class: "side-note", text: done.filter(Boolean).length + " of " + act.guide.length + " steps. Type it puts the command at the prompt without running it; read it, then press Enter. Shift-click runs it." }),
      el("ol", { class: "guide" }, act.guide.map((g, i) => {
        const actions = el("div", { class: "gstep-actions" });
        if (g.command) {
          actions.appendChild(el("button", { class: "btn small", title: "Type the command at the prompt; Shift-click also runs it", text: "Type it", onclick: (e) => { term.send(g.command + (e.shiftKey ? "\r" : "")); term.focus(); } }));
        }
        if (!g.check) {
          actions.appendChild(el("button", { class: "btn small" + (done[i] ? " ghost" : ""), text: done[i] ? "Undo" : "Mark done", onclick: () => {
            const at = manual.indexOf(i);
            if (at >= 0) { manual.splice(at, 1); } else { manual.push(i); }
            store(manualKey, manual);
            drawSide();
          } }));
        } else if (!done[i]) {
          actions.prepend(el("span", { class: "planned", text: "green when the cluster shows it" }));
        }
        return el("li", { class: "gstep" + (done[i] ? " done" : "") + (i === current ? " current" : ""), "aria-current": i === current ? "step" : null },
          el("div", { class: "gstep-head" }, el("span", { class: "tick", "aria-hidden": "true", text: done[i] ? "\u2713" : String(i + 1) }), el("span", { text: g.title }), done[i] ? el("span", { class: "sr-only", text: " (done)" }) : null),
          el("div", { class: "body", html: g.body }),
          g.command ? el("div", { class: "cmd" }, el("code", { text: g.command })) : null,
          actions);
      })));
  }

  let closePopover = () => {};
  function showCheck(c, anchor) {
    showPopover(anchor, c.description,
      el("div", { class: "pop-head" }, el("span", { class: "pill " + (c.status === "pass" ? "good" : c.status === "skipped" ? "" : "bad"), text: c.status }), el("b", { text: c.description })),
      c.why ? el("p", { class: "small", text: c.why }) : null,
      c.expected ? el("div", { class: "kv" }, el("span", { text: "expected" }), el("code", { text: c.expected })) : null,
      c.actual ? el("div", { class: "kv" }, el("span", { text: "actual" }), el("code", { text: c.actual })) : null,
      c.message ? el("div", { class: "kv" }, el("span", { text: "note" }), el("span", { text: c.message })) : null);
  }
  function showCheckList(list, anchor) {
    showPopover(anchor, "Live checks",
      el("div", { class: "pop-head" }, el("b", { text: "Live checks" })),
      el("ul", { class: "check-list" }, list.map((c) => el("li", null, el("button", { onclick: () => showCheck(c, anchor) },
        el("i", { class: "dot " + c.status, "aria-hidden": "true" }), el("span", { text: c.description }), el("span", { class: "st", text: c.status }))))));
  }
  function showPopover(anchor, label, ...children) {
    closePopover();
    const node = el("div", { class: "popover", role: "dialog", "aria-label": label }, children);
    let release = () => {};
    const off = (e) => { if (e.target.isConnected && !node.contains(e.target) && !anchor.contains(e.target)) { close(); } };
    const close = () => {
      node.remove();
      release();
      document.removeEventListener("click", off);
      if (closePopover === close) { closePopover = () => {}; }
    };
    closePopover = close;
    document.body.appendChild(node);
    const r = anchor.isConnected ? anchor.getBoundingClientRect() : checksBar.getBoundingClientRect();
    node.style.left = Math.max(12, Math.min(r.left, window.innerWidth - node.offsetWidth - 12)) + "px";
    node.style.top = Math.max(12, r.top - node.offsetHeight - 8) + "px";
    release = pushOverlay(close);
    setTimeout(() => { if (closePopover === close) { document.addEventListener("click", off); } }, 0);
  }
  onLeave(() => closePopover());

  function drawChecks(s) {
    if (isExam) {
      clear(checksBar, el("span", { class: "sum", text: "Exam mode: the checks run once, when you submit" }));
      return;
    }
    if (s.checkError) { clear(checksBar, el("span", { class: "chk error", "aria-hidden": "true" }), el("span", { class: "chk-next", title: s.checkError, text: s.checkError })); return; }
    const list = s.checks || [];
    const ok = list.filter((c) => c.status === "pass").length;
    const open = list.find((c) => c.status !== "pass");
    clear(checksBar,
      el("button", { class: "chk-sum", title: "Every check with its status", onclick: (e) => showCheckList(list, e.currentTarget) }, el("span", { class: "sum", text: "Checks " + ok + "/" + list.length })),
      list.map((c) => el("button", { class: "chk " + c.status, title: c.description + " (" + c.status + ")", "aria-label": c.description + ", " + c.status, onclick: (e) => showCheck(c, e.currentTarget) })),
      el("span", { class: "chk-next", text: open ? "next: " + open.description : "every check passes; Validate to grade" }));
  }

  let delay = 5000, unchanged = 0, lastKey = "", timerID = 0;
  function poll() {
    clearTimeout(timerID);
    if (gone || document.hidden) { return; }
    api.get("/api/session" + (isExam ? "" : "?live=1")).then((s) => {
      if (gone) { return; }
      if (!s.active || s.active.episodeId !== act.episodeId) {
        gone = true;
        if (closeConfirm) { closeConfirm(); }
        location.hash = debriefRoute(a, act.episodeId);
        return;
      }
      const hintsBefore = act.hintsUsed;
      act = s.active;
      updateMentor();
      if (tab === "story" && act.hintsUsed !== hintsBefore) { drawSide(); }
      lastChecks = s.checks || [];
      const key = JSON.stringify(lastChecks.map((c) => c.status));
      unchanged = key === lastKey ? unchanged + 1 : 0;
      lastKey = key;
      delay = unchanged >= 12 ? 15000 : 5000;
      drawChecks(s);
      if (tab === "steps") { drawSide(); }
    }).catch(() => {}).finally(() => { if (!gone) { timerID = setTimeout(poll, delay); } });
  }
  const onVisible = () => { if (!document.hidden) { unchanged = 0; poll(); } };
  document.addEventListener("visibilitychange", onVisible);
  term.onInput(() => { if (unchanged >= 12) { unchanged = 0; delay = 5000; clearTimeout(timerID); timerID = setTimeout(poll, 1500); } });
  onLeave(() => { gone = true; clearTimeout(timerID); document.removeEventListener("visibilitychange", onVisible); });

  drawTabs();
  drawSide();
  poll();

  let closeMentor = null;
  const labEl = stage.querySelector(".lab");
  const openMentorDrawer = () => {
    if (closeMentor) { closeMentor(); return; }
    closeMentor = openMentor(act, () => { updateMentor(); if (tab === "story") { drawSide(); } }, labEl, () => { closeMentor = null; mentorBtn.setAttribute("aria-expanded", "false"); });
    mentorBtn.setAttribute("aria-expanded", "true");
  };
  mentorBtn.addEventListener("click", openMentorDrawer);
  function submitExam(timedOut) {
    if (submitting || gone) { return; }
    submitting = true;
    validateBtn.disabled = true;
    if (closeConfirm) { closeConfirm(); }
    api.post("/api/submit").then((v) => {
      gone = true;
      store("kd:exam:" + a.id, null);
      if (timedOut) { toast("Time is up; the exam was submitted."); }
      location.hash = debriefRoute(a, v.episodeId);
    }).catch((e) => {
      validateBtn.disabled = false;
      toast(e.message, true);
      setTimeout(() => { submitting = false; }, timedOut ? 5000 : 0);
      poll();
    });
  }
  onLeave(() => { if (closeConfirm) { closeConfirm(); } });
  const validate = () => {
    if (validateBtn.disabled || submitting) { return; }
    if (isExam) {
      if (!closeConfirm) { closeConfirm = confirmSubmit(() => submitExam(false), () => { closeConfirm = null; term.focus(); }); }
      return;
    }
    validateBtn.disabled = true;
    api.post("/api/validate").then((v) => {
      if (v.passed) {
        location.hash = debriefRoute(a, v.episodeId);
        return;
      }
      showFailure(v, isDIY);
    }).catch((e) => toast(e.message, true)).finally(() => { validateBtn.disabled = false; });
  };
  validateBtn.addEventListener("click", validate);
  bind("mod+Enter", isExam ? "Submit the exam" : "Validate the namespace", validate);
  if (!isExam) { bind("mod+.", "Open the mentor", openMentorDrawer); }
  bind("mod+=", "Larger terminal text", () => term.zoom(1));
  bind("mod+-", "Smaller terminal text", () => term.zoom(-1));
}

// confirmSubmit asks before the one grade of an exam, with the focus on
// Keep working so a stray Enter does not end the exam. It returns close.
function confirmSubmit(onYes, onClose) {
  const back = el("div", { class: "modal-back", role: "dialog", "aria-modal": "true", "aria-label": "Submit the exam" });
  let release = () => {}, open = true;
  const close = () => { if (!open) { return; } open = false; release(); back.remove(); onClose(); };
  release = pushOverlay(close);
  back.addEventListener("click", (e) => { if (e.target === back) { close(); } });
  const keep = el("button", { class: "btn", text: "Keep working", onclick: close });
  const yes = el("button", { class: "btn primary", text: "Submit and end the exam", onclick: () => { close(); onYes(); } });
  back.appendChild(el("div", { class: "modal narrow" },
    el("div", { class: "eyebrow", text: "Exam rehearsal" }),
    el("h2", { class: "modal-title", text: "Submit now?" }),
    el("p", { class: "muted", text: "The checks run once and the exam ends; there is no second try on this run." }),
    el("div", { class: "modal-actions" }, keep, yes)));
  document.body.appendChild(back);
  keep.focus();
  return close;
}

function startTerminal(box, stateEl, reconnect) {
  const size = store("kd:term-font") || 13;
  const term = new window.Terminal({
    cursorBlink: true,
    fontFamily: '"JetBrains Mono", "SF Mono", ui-monospace, Menlo, Consolas, monospace',
    fontSize: size,
    theme: { background: "#050507", foreground: "#e4e4e7", cursor: "#2dd4bf", cursorAccent: "#050507", selectionBackground: "#1f3d3a", black: "#18181b", brightBlack: "#52525b", blue: "#60a5fa", green: "#34d399", yellow: "#fbbf24", red: "#f87171", cyan: "#2dd4bf", magenta: "#c084fc" },
    scrollback: 5000
  });
  const fit = new window.FitAddon.FitAddon();
  term.loadAddon(fit);
  term.open(box);
  try { fit.fit(); } catch (e) { /* not laid out yet */ }
  const ws = new WebSocket((location.protocol === "https:" ? "wss:" : "ws:") + "//" + location.host + "/ws/terminal?cols=" + term.cols + "&rows=" + term.rows);
  ws.binaryType = "arraybuffer";
  const decoder = new TextDecoder();
  let connected = false;
  const inputListeners = [];
  term.writeln("\x1b[90mStarting the workstation container...\x1b[0m");
  stateEl.textContent = "connecting";
  stateEl.className = "state";
  reconnect.hidden = true;
  ws.onopen = () => { connected = true; stateEl.textContent = "connected"; stateEl.className = "state on"; };
  ws.onmessage = (ev) => term.write(typeof ev.data === "string" ? ev.data : decoder.decode(new Uint8Array(ev.data), { stream: true }));
  ws.onclose = () => {
    connected = false;
    stateEl.textContent = "disconnected";
    stateEl.className = "state off";
    reconnect.hidden = false;
    term.writeln("\r\n\x1b[90m[the terminal session ended; Reconnect opens a new one, the namespace is unchanged]\x1b[0m");
  };
  const send = (obj) => { if (ws.readyState === 1) { ws.send(JSON.stringify(obj)); } };
  term.onData((d) => { send({ t: "i", d }); inputListeners.forEach((f) => f()); });
  const resize = () => {
    try { fit.fit(); } catch (e) { return; }
    send({ t: "r", c: term.cols, r: term.rows });
  };
  const ro = new ResizeObserver(resize);
  ro.observe(box);
  setTimeout(() => { resize(); term.focus(); }, 50);
  return {
    send: (text) => send({ t: "i", d: text }),
    focus: () => term.focus(),
    connected: () => connected,
    onInput: (f) => inputListeners.push(f),
    zoom: (d) => {
      const s = Math.max(10, Math.min(22, term.options.fontSize + d));
      term.options.fontSize = s;
      store("kd:term-font", s);
      resize();
    },
    close: () => { ro.disconnect(); try { ws.close(); } catch (e) { /* closed */ } term.dispose(); }
  };
}

function openMentor(act, onChange, lab, onClose) {
  const docked = !!lab && window.matchMedia("(min-width: 961px)").matches;
  const back = docked ? null : el("div", { class: "drawer-back" });
  const body = el("div", { class: "drawer-body", "aria-live": "polite" });
  const msg = (kind, html, cls) => { body.appendChild(el("div", { class: "mentor-msg" + (cls ? " " + cls : "") }, el("div", { class: "k", text: kind }), el("div", { html }))); body.scrollTop = body.scrollHeight; };
  if (act.mentor) { msg("Mentor", act.mentor); }
  if (!act.hintsUsed) {
    msg("Before you ask", "<p>A hint never lowers the grade. It does take this attempt out of the clean solves that mastery counts, so try one more observation first if you have one.</p>", "note");
  }
  (act.hints || []).forEach((h, i) => msg("Hint " + (i + 1) + " of " + act.hintsTotal, h));
  const hintBtn = el("button", { class: "btn primary", text: act.hintsUsed >= act.hintsTotal ? "No more hints" : "Ask for hint " + (act.hintsUsed + 1), disabled: act.hintsUsed >= act.hintsTotal });
  hintBtn.addEventListener("click", () => {
    hintBtn.disabled = true;
    api.post("/api/hint").then((h) => {
      msg("Hint " + h.index + " of " + h.total, h.html);
      act.hints = (act.hints || []).concat([h.html]);
      act.hintsUsed = h.index;
      onChange();
      hintBtn.textContent = h.index >= h.total ? "No more hints" : "Ask for hint " + (h.index + 1);
      hintBtn.disabled = h.index >= h.total;
    }).catch((e) => { hintBtn.disabled = false; msg("Mentor", "<p>" + escapeHTML(e.message) + "</p>", "note"); });
  });
  const area = el("textarea", { placeholder: "Name the cause in one line: which object is wrong, and the observation that shows it", maxlength: "300", "aria-label": "Your diagnosis" });
  if (act.diagnosis) { area.value = act.diagnosis; }
  const diagBtn = el("button", { class: "btn", text: act.diagnosis ? "Update my diagnosis" : "Record my diagnosis" });
  diagBtn.addEventListener("click", () => {
    api.post("/api/diagnose", { text: area.value }).then(() => {
      act.diagnosis = area.value;
      onChange();
      toast("Diagnosis recorded; the debrief compares it with the real cause.");
      diagBtn.textContent = "Update my diagnosis";
    }).catch((e) => toast(e.message, true));
  });
  let release = () => {}, open = true;
  const aside = el("aside", { class: "drawer" + (docked ? " docked" : ""), role: docked ? "complementary" : "dialog", "aria-modal": docked ? null : "true", "aria-label": "Mentor" });
  const close = () => {
    if (!open) { return; }
    open = false;
    release();
    if (docked) { aside.remove(); lab.classList.remove("mentor-open"); } else { back.remove(); }
    onClose();
  };
  if (docked) {
    aside.addEventListener("keydown", (e) => { if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); close(); } });
  } else {
    release = pushOverlay(close);
  }
  const closeBtn = el("button", { class: "btn ghost small x", text: "Close", onclick: close });
  aside.append(
    el("div", { class: "drawer-head" }, el("div", { class: "avatar mentor", text: "M", "aria-hidden": "true" }), el("div", null, el("b", { text: "Mentor" }), el("div", { class: "small muted", text: "Each hint goes one step further" })), closeBtn),
    body,
    el("div", { class: "drawer-foot" }, el("label", { text: "Your diagnosis / the last hint waits for it" }), area, el("div", { class: "row" }, diagBtn, hintBtn)));
  if (docked) {
    lab.classList.add("mentor-open");
    lab.appendChild(aside);
  } else {
    back.addEventListener("click", (e) => { if (e.target === back) { close(); } });
    back.appendChild(aside);
    document.body.appendChild(back);
  }
  onLeave(close);
  (act.diagnosis ? hintBtn : area).focus();
  return close;
}

function escapeHTML(s) {
  return s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}

function showFailure(v, isDIY) {
  const back = el("div", { class: "modal-back", role: "dialog", "aria-modal": "true", "aria-label": "Validation result" });
  let release = () => {};
  const close = () => { release(); back.remove(); };
  release = pushOverlay(close);
  back.addEventListener("click", (e) => { if (e.target === back) { close(); } });
  const keep = el("button", { class: "btn primary", text: "Keep working", onclick: close });
  back.appendChild(el("div", { class: "modal" },
    el("div", { class: "result-head" },
      el("div", { class: "result-icon no", text: "!" }),
      el("div", null, el("h2", { text: "Not there yet" }), el("div", { class: "small muted", text: "score " + Math.round(v.score * 100) + "%; nothing was recorded against you, validate as often as you like" }))),
    isDIY ? el("div", { class: "callout info" }, el("div", null, el("b", { text: "The cause may differ this time. " }), "If you repeated the practice fix, go back to the observations: the events and the object the ticket names tell you which part is wrong now.")) : null,
    checkList(v.checks),
    el("div", { class: "modal-actions" }, keep)));
  document.body.appendChild(back);
  keep.focus();
}

export function checkList(checks) {
  return el("ul", { class: "result-list" }, checks.map((c) => el("li", { class: c.status },
    el("span", null, el("span", { class: "st", text: c.status }), c.description),
    c.status !== "pass" && (c.expected || c.actual || c.message) ? el("span", { class: "ea", text: [c.expected ? "expected: " + c.expected : "", c.actual ? "actual: " + c.actual : "", c.message || ""].filter(Boolean).join("   ") }) : null,
    c.status !== "pass" && c.why ? el("span", { class: "why", text: c.why }) : null)));
}
