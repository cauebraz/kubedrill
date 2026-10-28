// Mermaid diagrams in scenario markdown: a ```mermaid block arrives as
// <pre><code class="language-mermaid"> and is replaced by the rendered SVG.
// The library loads on first use, runs with securityLevel strict and takes
// its colors from the design tokens; a theme switch redraws every diagram.

let loading = null;
let seq = 0;

function load() {
  if (window.mermaid) { return Promise.resolve(window.mermaid); }
  if (!loading) {
    loading = new Promise((resolve, reject) => {
      const s = document.createElement("script");
      s.src = "/vendor/mermaid/mermaid.min.js";
      s.onload = () => resolve(window.mermaid);
      s.onerror = () => { loading = null; reject(new Error("could not load the diagram library")); };
      document.head.appendChild(s);
    });
  }
  return loading;
}

function token(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

function configure(m) {
  const font = token("--font-sans");
  m.initialize({
    startOnLoad: false,
    securityLevel: "strict",
    theme: "base",
    fontFamily: font,
    flowchart: { curve: "basis", padding: 16, nodeSpacing: 40, rankSpacing: 40, htmlLabels: true, useMaxWidth: true, wrappingWidth: 360 },
    themeVariables: {
      fontFamily: font,
      fontSize: "13px",
      background: "transparent",
      primaryColor: token("--bg-2"),
      primaryBorderColor: token("--line-2"),
      primaryTextColor: token("--fg"),
      secondaryColor: token("--bg-3"),
      tertiaryColor: token("--bg-1"),
      lineColor: token("--fg-3"),
      textColor: token("--fg-1"),
      edgeLabelBackground: token("--bg-1"),
      clusterBkg: token("--bg-1"),
      clusterBorder: token("--line-1")
    }
  });
}

async function draw(m, host) {
  const id = "kd-mermaid-" + (++seq);
  try {
    const { svg } = await m.render(id, host.dataset.source);
    host.innerHTML = svg;
    host.classList.remove("failed");
  } catch (e) {
    host.textContent = host.dataset.source;
    host.classList.add("failed");
    document.getElementById(id)?.remove();
  }
}

// renderMermaid replaces every mermaid code block under root.
export async function renderMermaid(root) {
  const blocks = [...root.querySelectorAll("pre > code.language-mermaid")];
  if (!blocks.length) { return; }
  const m = await load();
  configure(m);
  for (const code of blocks) {
    const host = document.createElement("div");
    host.className = "mermaid-diagram";
    host.dataset.source = code.textContent;
    code.parentElement.replaceWith(host);
    await draw(m, host);
  }
}

async function redrawAll() {
  const hosts = [...document.querySelectorAll(".mermaid-diagram")];
  if (!hosts.length || !window.mermaid) { return; }
  configure(window.mermaid);
  for (const host of hosts) { await draw(window.mermaid, host); }
}

new MutationObserver(() => { redrawAll(); }).observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
new MutationObserver((records) => {
  for (const r of records) {
    for (const n of r.addedNodes) {
      if (n.nodeType === 1 && (n.matches?.("pre") || n.querySelector?.("code.language-mermaid"))) { renderMermaid(n.parentElement || n); return; }
    }
  }
}).observe(document.body, { childList: true, subtree: true });
