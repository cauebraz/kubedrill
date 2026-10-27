// The JSON API of kubedrill web. Every call carries the X-Kubedrill header;
// a network failure (the server stopped) is reported to the listener set
// with onConnection, so the page can say so instead of freezing.

let connectionListener = () => {};
let failures = 0;

export function onConnection(fn) { connectionListener = fn; }

function reached() {
  if (failures > 0) { failures = 0; connectionListener(true); }
}

function unreachable(err) {
  failures++;
  if (failures >= 2) { connectionListener(false); }
  throw err;
}

async function request(method, path, body) {
  const opts = { method, headers: { "X-Kubedrill": "1" }, credentials: "same-origin" };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  let res;
  try { res = await fetch(path, opts); } catch (e) { return unreachable(new Error("kubedrill web is not answering")); }
  reached();
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const err = new Error(data.error || "HTTP " + res.status);
    err.status = res.status;
    throw err;
  }
  return data;
}

export const api = {
  get: (p) => request("GET", p),
  post: (p, b) => request("POST", p, b === undefined ? {} : b)
};

// start posts /api/start and calls onStep for each step line of the NDJSON
// stream; it resolves with the session or rejects with the error line.
export async function start(body, onStep) {
  let res;
  try {
    res = await fetch("/api/start", { method: "POST", headers: { "X-Kubedrill": "1", "Content-Type": "application/json" }, credentials: "same-origin", body: JSON.stringify(body) });
  } catch (e) { return unreachable(new Error("kubedrill web is not answering")); }
  reached();
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  let session = null;
  for (;;) {
    const { value, done } = await reader.read();
    if (value) { buf += decoder.decode(value, { stream: true }); }
    let i;
    while ((i = buf.indexOf("\n")) >= 0) {
      const line = buf.slice(0, i).trim();
      buf = buf.slice(i + 1);
      if (!line) { continue; }
      const msg = JSON.parse(line);
      if (msg.step) { onStep(msg.step); }
      if (msg.error) {
        const err = new Error(msg.error);
        err.status = msg.status;
        throw err;
      }
      if (msg.session) { session = msg.session; }
    }
    if (done) { break; }
  }
  if (!session) { throw new Error("the start ended without a session"); }
  return session;
}
