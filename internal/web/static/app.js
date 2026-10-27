// Entry point of the workspace: routes, top bar, theme, and the banners for
// a preview run and a stopped server. Each screen lives in js/.

import { api, onConnection } from "./js/api.js";
import { el, clear, leave } from "./js/dom.js";
import { showHome } from "./js/home.js";
import { showCatalog } from "./js/catalog.js";
import { showExams } from "./js/exams.js";
import { showAssignment } from "./js/assignment.js";
import { showKit } from "./js/kit.js";
import { showWelcome } from "./js/welcome.js";

const app = document.getElementById("app");
const crumbsEl = document.getElementById("crumbs");
const pill = document.getElementById("session-pill");
const banner = document.getElementById("banner");
const nav = document.getElementById("nav");
let meta = { preview: "", dev: false, onboarded: true };

const ctx = {
  crumbs(parts) {
    clear(crumbsEl, parts.map((p, i) => [
      i > 0 ? el("span", { class: "sep", "aria-hidden": "true", text: "/" }) : null,
      p.href ? el("a", { href: p.href, text: p.text }) : el("span", { class: "here", "aria-current": "page", text: p.text })
    ]));
    document.title = parts[parts.length - 1].text + " / kubedrill";
  }
};

document.getElementById("theme-toggle").addEventListener("click", () => {
  const light = document.documentElement.getAttribute("data-theme") !== "light";
  if (light) { document.documentElement.setAttribute("data-theme", "light"); } else { document.documentElement.removeAttribute("data-theme"); }
  try { localStorage.setItem("kd:theme", light ? "light" : "dark"); } catch (e) { /* private window */ }
});

document.getElementById("help-toggle").addEventListener("click", () => {
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "?", bubbles: true }));
});

function refreshPill() {
  api.get("/api/session").then((s) => {
    if (!s.active) { pill.hidden = true; return; }
    const a = s.active;
    pill.hidden = false;
    pill.href = "#/a/" + encodeURIComponent(a.scenarioId) + "/" + (a.stage || "practice");
    clear(pill, el("span", { text: "Live" }), el("b", { text: a.scenarioId }), a.stage ? el("span", { text: { diy: "DIY", exam: "exam" }[a.stage] || "practice" }) : null);
  }).catch(() => { pill.hidden = true; });
}

function setBanner(kind, text, action) {
  if (!kind) { banner.hidden = true; return; }
  banner.hidden = false;
  banner.className = "banner " + kind;
  clear(banner, el("span", { text }), action || null);
}

function showMetaBanner() {
  if (meta.preview) {
    setBanner("info", "Preview of " + meta.preview + ": the files reload on every request. The practice and DIY runs need the cluster (kubedrill up).", el("a", { class: "link", href: "#/a/" + meta.preview + "/meeting", text: "Back to the start" }));
  } else {
    setBanner(null);
  }
}

onConnection((ok) => {
  if (ok) { showMetaBanner(); refreshPill(); return; }
  setBanner("error", "kubedrill web stopped answering. Run ./start.sh again; your progress is kept.", el("button", { class: "link", text: "Retry", onclick: () => route() }));
});

function navLinks(current) {
  const links = [["#/", "Home", "home"], ["#/catalog", "Catalog", "catalog"], ["#/exams", "Exams", "exams"]];
  if (meta.dev) { links.push(["#/kit", "Kit", "kit"]); }
  clear(nav, links.map(([href, text, id]) => el("a", { href, class: id === current ? "on" : "", "aria-current": id === current ? "page" : null, text })));
}

function route() {
  leave();
  const hash = location.hash.replace(/^#/, "").split("?")[0] || "/";
  const parts = hash.split("/").filter(Boolean);
  if (!meta.onboarded && parts[0] !== "welcome") {
    location.hash = "#/welcome";
    return;
  }
  document.body.classList.toggle("onboarding", parts[0] === "welcome");
  refreshPill();
  window.scrollTo(0, 0);
  switch (parts[0]) {
    case "welcome":
      navLinks("");
      return showWelcome(app, ctx, () => { meta.onboarded = true; location.hash = "#/"; });
    case "a":
      navLinks("catalog");
      if (parts[1]) { return showAssignment(app, ctx, decodeURIComponent(parts[1]), parts[2], parts[3] && decodeURIComponent(parts[3])); }
      break;
    case "catalog":
      navLinks("catalog");
      return showCatalog(app, ctx);
    case "exams":
      navLinks("exams");
      return showExams(app, ctx);
    case "kit":
      navLinks("kit");
      return showKit(app, ctx);
  }
  navLinks("home");
  return showHome(app, ctx);
}

window.addEventListener("hashchange", route);
api.get("/api/meta").then((m) => { meta = m; }).catch(() => {}).finally(() => { showMetaBanner(); route(); });
