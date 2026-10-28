// Stage 3: the object diagram, the story from zero, the ticket and the
// concept pages.

import { api } from "./api.js";
import { el, svg, clear, toast } from "./dom.js";
import { bind } from "./keys.js";
import { stageHead } from "./meeting.js";
import { hideFixSection } from "./ui.js";

// diagramSVG draws the object diagram in layers. A node sits one layer below
// its deepest parent; a source with no parent sits one layer above its
// nearest child, so arrows stay short. An arrow that still spans several
// layers passes through virtual points that take a slot in each layer it
// crosses, so it never runs under a box. Nodes keep their column (when set)
// and are otherwise ordered by the position of their neighbors to reduce
// crossings. Ports spread along the box edges and labels sit in the gap
// below the source, pushed apart when two of them would touch.
export function diagramSVG(d) {
  const W = 220, GX = 34, GY = 84, PAD = 22, DUMMY = 24, LINE = 13;
  const ids = d.nodes.map((n) => n.id);
  const edges = d.edges.filter((e) => ids.includes(e.from) && ids.includes(e.to) && e.from !== e.to);
  const parents = Object.fromEntries(ids.map((id) => [id, []]));
  const children = Object.fromEntries(ids.map((id) => [id, []]));
  for (const e of edges) { parents[e.to].push(e.from); children[e.from].push(e.to); }

  const layer = Object.fromEntries(ids.map((id) => [id, 0]));
  for (let pass = 0; pass < ids.length; pass++) {
    for (const e of edges) { if (layer[e.to] < layer[e.from] + 1) { layer[e.to] = Math.min(layer[e.from] + 1, ids.length - 1); } }
  }
  for (let pass = 0; pass < ids.length; pass++) {
    for (const id of ids) {
      if (parents[id].length || !children[id].length) { continue; }
      layer[id] = Math.max(0, Math.min(...children[id].map((c) => layer[c])) - 1);
    }
  }

  const wrap = (text, max) => {
    const lines = [];
    let line = "";
    for (const w of (text || "").split(" ")) {
      if (line && (line + " " + w).length > max) { lines.push(line); line = w; } else { line = line ? line + " " + w : w; }
    }
    if (line) { lines.push(line); }
    return lines.slice(0, 3);
  };
  const notes = Object.fromEntries(d.nodes.map((n) => [n.id, wrap(n.note, Math.floor((W - 34) / 6.1))]));
  const H = 42 + Math.max(1, ...Object.values(notes).map((l) => l.length)) * LINE;

  const layers = [];
  const put = (item, li) => { (layers[li] = layers[li] || []).push(item); };
  d.nodes.forEach((n, i) => put({ id: n.id, node: n, order: i, column: n.column }, layer[n.id]));
  const chains = edges.map((e) => {
    const pts = [e.from];
    for (let li = layer[e.from] + 1; li < layer[e.to]; li++) {
      const id = "~" + e.from + ">" + e.to + "@" + li;
      put({ id, dummy: true, order: 1e6 }, li);
      pts.push(id);
    }
    pts.push(e.to);
    return { e, pts };
  });
  for (let li = 0; li < layers.length; li++) { layers[li] = layers[li] || []; }
  for (const l of layers) { l.sort((x, y) => (x.column ?? 0) - (y.column ?? 0) || x.order - y.order); }

  const neighbors = {};
  for (const c of chains) {
    for (let k = 1; k < c.pts.length; k++) {
      (neighbors[c.pts[k]] = neighbors[c.pts[k]] || { up: [], down: [] }).up.push(c.pts[k - 1]);
      (neighbors[c.pts[k - 1]] = neighbors[c.pts[k - 1]] || { up: [], down: [] }).down.push(c.pts[k]);
    }
  }
  const index = () => Object.fromEntries(layers.flatMap((l) => l.map((it, i) => [it.id, i])));
  for (let sweep = 0; sweep < 6; sweep++) {
    const down = sweep % 2 === 0;
    const idx = index();
    const order = down ? layers.map((_, i) => i).slice(1) : layers.map((_, i) => i).slice(0, -1).reverse();
    for (const li of order) {
      const l = layers[li];
      if (l.some((it) => it.column !== undefined)) { continue; }
      const bary = (it) => {
        const ns = ((neighbors[it.id] || {})[down ? "up" : "down"]) || [];
        return ns.length ? ns.reduce((sum, n) => sum + idx[n], 0) / ns.length : idx[it.id];
      };
      const keyed = l.map((it) => ({ it, k: bary(it) }));
      keyed.sort((x, y) => x.k - y.k);
      layers[li] = keyed.map((x) => x.it);
    }
  }

  const widthOf = (it) => (it.dummy ? DUMMY : W);
  const rowWidth = (l) => l.reduce((sum, it) => sum + widthOf(it), 0) + Math.max(0, l.length - 1) * GX;
  const width = Math.max(...layers.map(rowWidth)) + 2 * PAD;
  const height = layers.length * (H + GY) - GY + 2 * PAD;
  const pos = {};
  layers.forEach((l, li) => {
    let x = (width - rowWidth(l)) / 2;
    for (const it of l) {
      pos[it.id] = { x, y: PAD + li * (H + GY), w: widthOf(it), dummy: !!it.dummy };
      x += widthOf(it) + GX;
    }
  });

  const ports = (list, p) => {
    const out = {};
    const sorted = list.slice().sort((x, y) => pos[x.toward].x - pos[y.toward].x);
    sorted.forEach((item, i) => { out[item.key] = p.x + p.w * (sorted.length === 1 ? 0.5 : 0.25 + 0.5 * i / (sorted.length - 1)); });
    return out;
  };
  const outPorts = {}, inPorts = {};
  for (const id of ids) {
    const outs = chains.filter((c) => c.pts[0] === id).map((c, i) => ({ key: c.e.from + ">" + c.e.to + "#" + i, toward: c.pts[1], c }));
    const ins = chains.filter((c) => c.pts[c.pts.length - 1] === id).map((c, i) => ({ key: c.e.from + ">" + c.e.to + "#" + i, toward: c.pts[c.pts.length - 2], c }));
    const o = ports(outs, pos[id]), n = ports(ins, pos[id]);
    outs.forEach((it) => { outPorts[it.c.e.from + ">" + it.c.e.to] = o[it.key]; });
    ins.forEach((it) => { inPorts[it.c.e.from + ">" + it.c.e.to] = n[it.key]; });
  }

  const root = svg("svg", { viewBox: "0 0 " + width + " " + height, width: "100%", style: "max-width:" + width + "px; min-width:" + Math.round(width * 0.8) + "px; color: var(--fg-3)", role: "img", "aria-label": "Object diagram: " + d.nodes.map((n) => n.label).join(", ") },
    svg("defs", null, svg("marker", { id: "arrow", viewBox: "0 0 10 10", refX: 9, refY: 5, markerWidth: 7, markerHeight: 7, orient: "auto-start-reverse" }, svg("path", { d: "M0 0 L10 5 L0 10 z", fill: "currentColor" }))));
  const labels = [];
  for (const c of chains) {
    const key = c.e.from + ">" + c.e.to;
    const pts = c.pts.map((id, k) => {
      const p = pos[id];
      if (k === 0) { return [{ x: outPorts[key], y: p.y + H }]; }
      if (k === c.pts.length - 1) { return [{ x: inPorts[key], y: p.y }]; }
      return [{ x: p.x + p.w / 2, y: p.y }, { x: p.x + p.w / 2, y: p.y + H }];
    }).flat();
    let dPath = "M" + pts[0].x + " " + pts[0].y;
    for (let k = 1; k < pts.length; k++) {
      const a = pts[k - 1], b = pts[k];
      if (a.x === b.x) { dPath += " L" + b.x + " " + b.y; continue; }
      const my = (a.y + b.y) / 2;
      dPath += " C" + a.x + " " + my + " " + b.x + " " + my + " " + b.x + " " + b.y;
    }
    root.appendChild(svg("path", { class: "edge", d: dPath, fill: "none", "stroke-width": 1.4, "marker-end": "url(#arrow)" }));
    if (c.e.label) {
      const a = pts[0], b = pts[1];
      labels.push({ text: c.e.label, x: (a.x + b.x) / 2, y: (a.y + b.y) / 2, w: c.e.label.length * 6.6 + 16, band: layer[c.e.from] });
    }
  }
  for (const it of layers.flat()) {
    if (it.dummy) { continue; }
    const n = it.node, p = pos[n.id];
    root.appendChild(svg("rect", { class: "node-box", x: p.x, y: p.y, width: W, height: H, rx: 10, "stroke-width": 1 }));
    root.appendChild(svg("rect", { class: "node-accent", x: p.x, y: p.y + 14, width: 3, height: H - 28, rx: 1.5 }));
    const title = n.label.length > 27 ? n.label.slice(0, 26) + "..." : n.label;
    root.appendChild(svg("text", { class: "node-title", x: p.x + 16, y: p.y + 25, "font-size": 13, "font-weight": 600, text: title }, n.label !== title ? svg("title", { text: n.label }) : null));
    notes[n.id].forEach((l, k) => root.appendChild(svg("text", { class: "node-note", x: p.x + 16, y: p.y + 43 + k * LINE, "font-size": 11, text: l })));
  }
  const bands = {};
  for (const lb of labels) { (bands[lb.band] = bands[lb.band] || []).push(lb); }
  for (const band of Object.values(bands)) {
    band.sort((x, y) => x.x - y.x);
    for (let pass = 0; pass < 4; pass++) {
      for (let k = 1; k < band.length; k++) {
        const prev = band[k - 1], cur = band[k];
        const overlap = prev.x + prev.w / 2 + 8 - (cur.x - cur.w / 2);
        if (overlap > 0) { prev.x -= overlap / 2; cur.x += overlap / 2; }
      }
    }
    for (const lb of band) {
      lb.x = Math.min(Math.max(lb.x, lb.w / 2 + 4), width - lb.w / 2 - 4);
      root.appendChild(svg("rect", { class: "edge-lbl-bg", x: lb.x - lb.w / 2, y: lb.y - 10, width: lb.w, height: 20, rx: 10 }));
      root.appendChild(svg("text", { class: "edge-lbl", x: lb.x, y: lb.y + 4, "text-anchor": "middle", "font-size": 10.5, text: lb.text }));
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
    if (pid === "story" && diyOpen) { hideFixSection(view, "Hidden until your Do it yourself run ends: it would name the fix before you look for the cause."); }
  }
  const diyOpen = !!(a.progress.practice && a.progress.practice.done && !(a.progress.diy && a.progress.diy.done));
  const finish = () => api.post("/api/progress", { id: a.id, stage: "learn", score: 1, total: 1 }).then(next).catch((e) => toast(e.message, true));
  bind("Enter", "Done reading: go to the lab", finish);
  stage.appendChild(el("div", { class: "stage-inner" },
    stageHead("Stage 3 / Learn the pieces", "How the pieces fit together", "Read the diagram top to bottom: each arrow is a step that has to work for customers to see the page. The tabs below tell the ticket from zero and explain each concept.",
      el("button", { class: "btn", text: "I am ready for the lab", onclick: finish })),
    a.simulation ? el("div", { class: "card diagram-wrap" }, diagramSVG(a.simulation.diagram)) : null,
    tabs, el("div", { class: "card" }, view)));
  show(pages[0].id);
}
