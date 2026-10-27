// Stage 3: the object diagram, the story from zero, the ticket and the
// concept pages.

import { api } from "./api.js";
import { el, svg, clear, toast } from "./dom.js";
import { bind } from "./keys.js";
import { stageHead } from "./meeting.js";

// diagramSVG lays the nodes out in layers by their longest path from a
// source; inside a layer, nodes keep their column (when set) then their
// order in the file.
export function diagramSVG(d) {
  const depth = Object.fromEntries(d.nodes.map((n) => [n.id, 0]));
  for (let pass = 0; pass < d.nodes.length; pass++) {
    for (const e of d.edges) {
      if (depth[e.to] < depth[e.from] + 1) { depth[e.to] = depth[e.from] + 1; }
    }
  }
  const layers = [];
  d.nodes.forEach((n, i) => { (layers[depth[n.id]] = layers[depth[n.id]] || []).push(Object.assign({ order: i }, n)); });
  for (const l of layers) { l.sort((x, y) => (x.column || 0) - (y.column || 0) || x.order - y.order); }
  const W = 210, H = 76, GX = 36, GY = 70, PAD = 20;
  const width = Math.max(...layers.map((l) => l.length)) * (W + GX) - GX + 2 * PAD;
  const height = layers.length * (H + GY) - GY + 2 * PAD;
  const pos = {};
  layers.forEach((l, li) => {
    const rowW = l.length * (W + GX) - GX;
    l.forEach((n, ni) => { pos[n.id] = { x: (width - rowW) / 2 + ni * (W + GX), y: PAD + li * (H + GY) }; });
  });
  const root = svg("svg", { width, height, viewBox: "0 0 " + width + " " + height, role: "img", "aria-label": "Object diagram: " + d.nodes.map((n) => n.label).join(", "), style: "color: var(--fg-3)" },
    svg("defs", null, svg("marker", { id: "arrow", viewBox: "0 0 10 10", refX: 9, refY: 5, markerWidth: 7, markerHeight: 7, orient: "auto-start-reverse" }, svg("path", { d: "M0 0 L10 5 L0 10 z", fill: "currentColor" }))));
  for (const e of d.edges) {
    const a = pos[e.from], b = pos[e.to];
    if (!a || !b) { continue; }
    let x1 = a.x + W / 2, y1 = a.y + H, x2 = b.x + W / 2, y2 = b.y;
    if (a.y === b.y) { y1 = a.y + H / 2; y2 = b.y + H / 2; x1 = a.x + (a.x < b.x ? W : 0); x2 = b.x + (a.x < b.x ? 0 : W); }
    const my = (y1 + y2) / 2;
    root.appendChild(svg("path", { class: "edge", d: `M${x1} ${y1} C${x1} ${my} ${x2} ${my} ${x2} ${y2}`, fill: "none", "stroke-width": 1.4, "marker-end": "url(#arrow)" }));
    if (e.label) {
      const lx = (x1 + x2) / 2;
      root.appendChild(svg("rect", { class: "edge-lbl-bg", x: lx - e.label.length * 3.4 - 7, y: my - 10, width: e.label.length * 6.8 + 14, height: 19, rx: 9.5 }));
      root.appendChild(svg("text", { class: "edge-lbl", x: lx, y: my + 3.5, "text-anchor": "middle", "font-size": 10.5, text: e.label }));
    }
  }
  for (const n of d.nodes) {
    const p = pos[n.id];
    root.appendChild(svg("rect", { class: "node-box", x: p.x, y: p.y, width: W, height: H, rx: 10, "stroke-width": 1 }));
    root.appendChild(svg("rect", { class: "node-accent", x: p.x, y: p.y + 14, width: 3, height: H - 28, rx: 1.5 }));
    root.appendChild(svg("text", { class: "node-title", x: p.x + 16, y: p.y + 24, "font-size": 13, "font-weight": 600, text: n.label }));
    if (n.note) {
      const lines = [];
      let line = "";
      for (const w of n.note.split(" ")) {
        if ((line + " " + w).length > 31) { lines.push(line); line = w; } else { line = line ? line + " " + w : w; }
      }
      lines.push(line);
      lines.slice(0, 3).forEach((l, k) => root.appendChild(svg("text", { class: "node-note", x: p.x + 16, y: p.y + 41 + k * 13, "font-size": 11, text: l })));
    }
  }
  return root;
}

export function renderLearn(stage, a, next) {
  const view = el("div", { class: "prose" });
  const tabs = el("div", { class: "tabs", role: "tablist" });
  const pages = [];
  if (a.story) { pages.push({ id: "story", label: "The story from zero", html: a.story }); }
  pages.push({ id: "ticket", label: "The ticket", html: a.ticket });
  for (const c of a.concepts) {
    if (c.body) { pages.push({ id: "concept-" + c.id, label: c.title, html: c.body }); }
  }
  function show(pid) {
    clear(tabs, pages.map((p) => el("button", { class: "tab" + (p.id === pid ? " on" : ""), role: "tab", "aria-selected": p.id === pid ? "true" : "false", text: p.label, onclick: () => show(p.id) })));
    view.innerHTML = pages.find((x) => x.id === pid).html;
  }
  const finish = () => api.post("/api/progress", { id: a.id, stage: "learn", score: 1, total: 1 }).then(next).catch((e) => toast(e.message, true));
  bind("Enter", "Done reading: go to the lab", finish);
  stage.appendChild(el("div", { class: "stage-inner" },
    stageHead("Stage 3 / Learn the pieces", "How the pieces fit together", "Read the diagram top to bottom: each arrow is a step that has to work for customers to see the page. The tabs below tell the ticket from zero and explain each concept.",
      el("button", { class: "btn", text: "I am ready for the lab", onclick: finish })),
    a.simulation ? el("div", { class: "card diagram-wrap" }, diagramSVG(a.simulation.diagram)) : null,
    tabs, el("div", { class: "card" }, view)));
  show(pages[0].id);
}
