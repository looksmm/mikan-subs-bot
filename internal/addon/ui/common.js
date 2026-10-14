// The page's helpers: calls through the panel, small helpers, the toast and the start. A page runs in a sandboxed frame with an opaque origin: everything it
// needs it asks the panel for over postMessage, and the panel forwards the call to the addon
// of that page (POST /v2/ui/rpc) with the admin's session.
"use strict";

window.K = (() => {
  const $ = (id) => document.getElementById(id);

  let seq = 0;
  const waiting = new Map();
  window.addEventListener("message", (ev) => {
    if (ev.source !== window.parent) return;
    const m = ev.data;
    if (!m || m.type !== "rpc-result" || !waiting.has(m.id)) return;
    const { resolve, timer } = waiting.get(m.id);
    clearTimeout(timer);
    waiting.delete(m.id);
    resolve({ status: m.status, body: m.body });
  });
  // rpc asks the panel and waits for its answer. The panel drops answers to calls made
  // before its frame finished loading, so a call that gets none within `wait` is asked
  // again, up to `tries` times; the first answer wins.
  function rpc(method, path, body, wait = 45000, tries = 1) {
    return new Promise((resolve) => {
      let done = false;
      const ids = [];
      const finish = (r) => {
        if (done) return;
        done = true;
        for (const id of ids) {
          const w = waiting.get(id);
          if (w) clearTimeout(w.timer);
          waiting.delete(id);
        }
        resolve(r);
      };
      const ask = (left) => {
        const id = "c" + ++seq;
        ids.push(id);
        const timer = setTimeout(() => (left > 1 ? ask(left - 1) : finish({ status: 0, body: { code: "timeout" } })), wait);
        waiting.set(id, { resolve: finish, timer });
        window.parent.postMessage({ type: "rpc", id, method, path, body }, "*");
      };
      ask(tries);
    });
  }
  const failText = (r) =>
    (r.body && r.body.message) ||
    (r.status === 0 ? "Панель не ответила." : r.status === 502 ? "Аддон не отвечает панели." : "Ошибка " + r.status + (r.body && r.body.code ? " (" + r.body.code + ")" : ""));

  function el(tag, attrs, ...kids) {
    const n = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v == null || v === false) continue;
      if (k === "class") n.className = v;
      else if (k === "text") n.textContent = v;
      else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
      else n.setAttribute(k, v === true ? "" : v);
    }
    for (const k of kids.flat()) if (k != null) n.append(k.nodeType ? k : document.createTextNode(String(k)));
    return n;
  }
  function ago(unix) {
    if (!unix) return "";
    const s = Math.max(0, Math.round(Date.now() / 1000 - unix));
    if (s < 60) return "только что";
    const m = Math.round(s / 60);
    if (m < 60) return m + " мин назад";
    const h = Math.round(m / 60);
    if (h < 48) return h + " ч назад";
    return Math.round(h / 24) + " дн назад";
  }
  function showError(text) {
    $("error").textContent = text || "";
    $("error").hidden = !text;
  }
  let toastTimer = null;
  function toast() {
    $("toast").hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => ($("toast").hidden = true), 1800);
  }
  const normText = (t) => t.replace(/\r\n/g, "\n").split("\n").map((l) => l.replace(/[ \t]+$/, "")).join("\n").replace(/^\n+|\n+$/g, "");

  // start loads the page once the frame finished loading (the panel counts loads and drops
  // the answers to calls made before), then every 15 s while it is shown; one load at a time.
  function start(load) {
    let busy = false;
    const run = async (first) => {
      if (busy) return;
      busy = true;
      try { await load(first); } finally { busy = false; }
    };
    const go = () => setTimeout(() => run(true), 150);
    if (document.readyState === "complete") go();
    else window.addEventListener("load", go, { once: true });
    setInterval(() => { if (!document.hidden) run(false); }, 15000);
  }
  async function status(first) {
    return first ? rpc("GET", "/status", undefined, 2500, 8) : rpc("GET", "/status", undefined, 20000);
  }
  // save sends the page's own settings; the addon keeps the other pages' as they are.
  function save(body) {
    return rpc("PUT", "/settings", body, 20000, 2);
  }

  return { $, rpc, failText, el, ago, showError, toast, normText, start, status, save };
})();
