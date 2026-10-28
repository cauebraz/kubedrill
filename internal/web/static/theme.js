// Applies the stored or preferred theme before the first paint; loaded as a
// blocking script so the Content-Security-Policy needs no inline script.
try {
  var t = localStorage.getItem("kd:theme");
  if (!t && window.matchMedia && window.matchMedia("(prefers-color-scheme: light)").matches) { t = "light"; }
  if (t === "light") { document.documentElement.setAttribute("data-theme", "light"); }
} catch (e) {}
