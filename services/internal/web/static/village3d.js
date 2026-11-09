// The village in 3D.
//
// This module is loaded only when the 3D view is opened. It draws the market
// square from /village.json, which the server builds from the same chain
// snapshot and view models as the pages, so the scene can only show what the
// market contract holds. It has no game logic of its own: tapping a stall
// shows that stall's card from the page (its sell, buy and invest forms), and
// tapping a gathering place shows the page's own gather button.

const THREE_URL = new URL("./vendor/three-0.160.1/three.module.min.js", import.meta.url).href;

let T = null;          // the three.js namespace, once loaded
let ui = null;         // DOM of the overlay
let gl = null;         // renderer, scene, camera and the objects we update
let state = null;      // the latest /village.json
let isOpen = false;
let pollTimer = 0;
let frame = 0;
let openedByLink = false;
let loading = null;    // the one promise that loads Three.js and builds the scene
let ffTimer = 0;       // the single fast-forward page refresh
let pendingTimer = 0;  // refresh again while moves are still on their way
const gatherForms = new WeakSet();

const reduceMotion = matchMedia("(prefers-reduced-motion: reduce)").matches;
const isLocalPage = () => !!document.querySelector(".village");     // a player's village page
const isWelcome = () => !!document.querySelector(".hero-form");

const STALL_COLOURS = { ramu: 0x2563EB, meena: 0x9F1239, bashir: 0x4C1D95, lakshmi: 0x0E7490 };
const STALL_CSS = { ramu: "#2563EB", meena: "#9F1239", bashir: "#4C1D95", lakshmi: "#0E7490" };
const ORDER = ["ramu", "meena", "bashir", "lakshmi"];
const C = {
  cream: 0xFFF1DC, terracotta: 0xB4532A, umber: 0x5C1F0B, wood: 0x9A6438, darkwood: 0x5E3B22,
  plaster: 0xF2DDBD, stone: 0xE9D8BA, earth: 0xE3C08E, grass: 0xB5C77A, leaf: 0x6E9A48,
  leafDark: 0x5A8840, mango: 0xF5A524, chilli: 0xD7261E, rice: 0xF4EAD2, fish: 0x9DB4C8, jar: 0xC2611F,
  skin: 0xB97A4F, water: 0x4F86C6, shutter: 0xA3B0C2, brass: 0xD9A441, flame: 0xFFB347,
};

// ---------------------------------------------------------------- public API

// byLink is true when a link on this page opened the view (leaving then goes
// back), false when the page was loaded with #3d (leaving clears the address).
export async function open(byLink) {
  buildUI();
  if (isOpen) { focusFromHash(); return; }
  isOpen = true;
  openedByLink = !!byLink;
  ui.root.hidden = false;
  document.documentElement.classList.add("v3d-on");
  measureTop();
  requestAnimationFrame(() => ui.root.classList.add("is-in"));
  window.haatRefresh = refreshPage;
  addEventListener("keydown", onKey);
  addEventListener("resize", measureTop);

  if (!gl) {
    if (!webglWorks()) { showFallback(); return; }
    ui.loading.hidden = false;
    if (!loading) {
      loading = Promise.all([import(THREE_URL), fetchState(), fontsReady()]).then(([three, data]) => {
        T = three;
        state = data;
        buildScene();
      });
    }
    try {
      await loading;
    } catch (err) {
      console.error(err);
      loading = null;
      gl = null;
      showFallback();
      return;
    }
    ui.loading.hidden = true;
  } else {
    fetchState().then((d) => { state = d; if (isOpen) applyState(d); }).catch(() => {});
  }
  if (!isOpen) return;
  if (state) applyState(state);
  else fetchState().then((d) => { state = d; applyState(d); }).catch(() => {});
  gl.intro = reduceMotion ? 1 : 0;
  startLoop();
  schedulePoll();
  focusFromHash();
}

export function close() {
  if (!ui || !isOpen) return;
  isOpen = false;
  closePanel(true);
  ui.root.classList.remove("is-in");
  document.documentElement.classList.remove("v3d-on");
  window.haatRefresh = null;
  removeEventListener("keydown", onKey);
  removeEventListener("resize", measureTop);
  clearTimeout(pollTimer);
  clearTimeout(ffTimer);
  clearTimeout(pendingTimer);
  cancelAnimationFrame(frame);
  setTimeout(() => { if (!isOpen) ui.root.hidden = true; }, 220);
}

function leave() {
  if (openedByLink && history.length > 1) { history.back(); return; }
  history.replaceState(null, "", location.pathname + location.search);
  close();
}

// ---------------------------------------------------------------- overlay DOM

function el(tag, cls, html) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (html != null) e.innerHTML = html;
  return e;
}

function buildUI() {
  if (ui) return;
  const root = el("div", "v3d");
  root.hidden = true;
  root.setAttribute("role", "region");
  root.setAttribute("aria-label", "The village in 3D");
  root.innerHTML = `
    <div class="v3d-stage"><canvas class="v3d-canvas" aria-hidden="true"></canvas></div>
    <div class="v3d-hud">
      <div class="v3d-card v3d-title">
        <img src="/static/logo.svg" alt="" width="40" height="40">
        <div>
          <p class="v3d-eyebrow">The village in 3D</p>
          <h2 class="v3d-day">The haat square</h2>
          <p class="v3d-line"></p>
          <p class="v3d-ticker" aria-live="polite"></p>
        </div>
      </div>
      <button type="button" class="btn btn-ghost v3d-back"><span class="v3d-long">Back to the</span>2D village</button>
    </div>
    <div class="v3d-dock">
      <div class="v3d-chips" role="toolbar" aria-label="Stalls and places"></div>
      <p class="v3d-help">Drag to look around, scroll or pinch to zoom. Tap a stall to trade.</p>
    </div>
    <div class="v3d-tip" hidden></div>
    <aside class="v3d-panel" hidden aria-label="Stall">
      <button type="button" class="v3d-panel-x" aria-label="Close">&times;</button>
      <div class="v3d-panel-body"></div>
    </aside>
    <div class="v3d-loading" hidden><span class="busy-shell">🐚</span><span>Setting out the stalls…</span></div>
    <div class="v3d-fallback" hidden>
      <div class="v3d-card">
        <h2>This browser cannot draw the 3D village</h2>
        <p class="sub">WebGL is switched off or not available here. Everything in the 3D view is also in the 2D village, with the same stalls, prices and moves.</p>
        <button type="button" class="btn btn-primary v3d-back2">Go to the 2D village</button>
      </div>
    </div>`;
  document.body.appendChild(root);
  ui = {
    root, canvas: root.querySelector(".v3d-canvas"), stage: root.querySelector(".v3d-stage"),
    day: root.querySelector(".v3d-day"), line: root.querySelector(".v3d-line"),
    ticker: root.querySelector(".v3d-ticker"), chips: root.querySelector(".v3d-chips"),
    tip: root.querySelector(".v3d-tip"), panel: root.querySelector(".v3d-panel"),
    panelBody: root.querySelector(".v3d-panel-body"), loading: root.querySelector(".v3d-loading"),
    fallback: root.querySelector(".v3d-fallback"),
  };
  root.querySelector(".v3d-back").addEventListener("click", leave);
  root.querySelector(".v3d-back2").addEventListener("click", leave);
  root.querySelector(".v3d-panel-x").addEventListener("click", () => closePanel());
}

function measureTop() {
  const bar = document.querySelector(".topbar");
  const h = bar ? Math.round(bar.getBoundingClientRect().height) : 0;
  ui.root.style.setProperty("--v3d-top", h + "px");
}

function showFallback() {
  ui.loading.hidden = true;
  ui.fallback.hidden = false;
}

function webglWorks() {
  try {
    const c = document.createElement("canvas");
    return !!(c.getContext("webgl2") || c.getContext("webgl"));
  } catch (e) { return false; }
}

function fontsReady() {
  if (!document.fonts || !document.fonts.load) return Promise.resolve();
  const wait = Promise.all([
    document.fonts.load('800 64px "Baloo 2"'), document.fonts.load('700 40px "Baloo 2"'),
    document.fonts.load('600 32px Inter'), document.fonts.load('700 32px Inter'),
  ]).catch(() => {});
  return Promise.race([wait, new Promise((r) => setTimeout(r, 1800))]);
}

function onKey(e) {
  if (e.key !== "Escape") return;
  if (!ui.panel.hidden) closePanel(); else leave();
}

// ---------------------------------------------------------------- state

async function fetchState() {
  const r = await fetch("/village.json", { credentials: "same-origin", cache: "no-store" });
  if (!r.ok) throw new Error("village.json " + r.status);
  return r.json();
}

function schedulePoll() {
  clearTimeout(pollTimer);
  pollTimer = setTimeout(async () => {
    if (!isOpen) return;
    if (!document.hidden) {
      try { state = await fetchState(); applyState(state); } catch (e) { /* keep the last picture */ }
    }
    schedulePoll();
  }, state && state.fast_forward.running ? 3500 : 6000);
}

// The page refreshes itself in place while the 3D view is open, so a move
// that lands updates both the 2D cards and the scene without a reload.
async function refreshPage() {
  try {
    const r = await fetch(location.pathname + location.search, { credentials: "same-origin", cache: "no-store" });
    if (!r.ok) throw new Error("page " + r.status);
    const doc = new DOMParser().parseFromString(await r.text(), "text/html");
    if (!isOpen) { location.reload(); return; }
    const openKey = gl && gl.panelFor;
    closePanel(true, true);
    for (const sel of [".topbar", "main.main"]) {
      const now = document.querySelector(sel), next = doc.querySelector(sel);
      if (now && next) now.innerHTML = next.innerHTML;
    }
    const ff = document.querySelector(".ffbar"), ffNext = doc.querySelector(".ffbar");
    if (ff) ff.remove();
    if (ffNext) document.querySelector("main.main").before(ffNext);
    document.querySelectorAll("body > .toast:not(.toast-float)").forEach((t) => t.remove());
    const flash = doc.querySelector("body > .toast");
    if (flash) floatToast(flash.className.includes("toast-err") ? "err" : "ok", flash.textContent);
    document.body.className = doc.body.className + " ";
    for (const k of ["pending", "day", "ff"]) {
      if (doc.body.dataset[k] != null) document.body.dataset[k] = doc.body.dataset[k];
      else delete document.body.dataset[k];
    }
    document.querySelectorAll("[data-v3d]").forEach((e) => { e.hidden = false; });
    const onway = document.getElementById("onway");
    if (onway && !(parseInt(document.body.dataset.pending || "0", 10) > 0)) onway.hidden = true;
    measureTop();
    if (openKey) openPanel(openKey, true);
    clearTimeout(ffTimer);
    clearTimeout(pendingTimer);
    if (document.body.dataset.ff) ffTimer = setTimeout(() => (window.haatRefresh || (() => location.reload()))(), 6000);
    else if (parseInt(document.body.dataset.pending || "0", 10) > 0) pendingTimer = setTimeout(() => (window.haatRefresh || (() => location.reload()))(), 1500);
    const next = await fetchState();
    if (!isOpen) return;
    state = next;
    applyState(state);
  } catch (e) {
    location.reload();
  }
}

function floatToast(kind, msg) {
  const t = el("div", "toast toast-" + kind + " toast-float");
  t.textContent = msg.trim();
  document.body.appendChild(t);
  setTimeout(() => t.remove(), 4000);
}

function num(v) { return Number(v).toLocaleString("en-US"); }

function clockWord(f) {
  const h = 6 + f * 18;
  if (h < 8) return "Daybreak";
  if (h < 11.5) return "Morning";
  if (h < 15) return "Midday";
  if (h < 17.5) return "Afternoon";
  if (h < 19.5) return "Evening";
  return "Night";
}

function updateHUD(s) {
  ui.day.textContent = `Day ${s.day}, ${s.season_label}`;
  const bits = [];
  bits.push(s.flood ? `<span class="pill pill-danger">Road flooded, no city cart</span>` : `<span class="pill">Road open, city cart comes</span>`);
  bits.push(`<span class="v3d-clock">${clockWord(gl ? gl.dayFrac : s.clock.day_fraction)}</span>`);
  if (s.fast_forward.running) bits.push(`<span class="pill pill-violet">Fast-forward day ${s.fast_forward.day} of ${s.fast_forward.of}</span>`);
  else if (s.diwali_in > 0) bits.push(`<span class="v3d-soft">Diwali in ${s.diwali_in} ${s.diwali_in === 1 ? "day" : "days"}</span>`);
  else bits.push(`<span class="v3d-soft">Diwali today</span>`);
  ui.line.innerHTML = bits.join("");
  const c = s.chronicle[0];
  ui.ticker.innerHTML = c ? `<span>${c.emoji}</span><span class="chron-d">Day ${c.day}</span> ${escapeHTML(c.text)}` : "";
  ui.ticker.hidden = !c;
  renderChips(s);
}

function escapeHTML(t) {
  return String(t).replace(/[&<>"']/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[ch]));
}

function renderChips(s) {
  const key = JSON.stringify([s.shops.map((x) => [x.key, x.open, x.purse, x.mood]), s.places.map((p) => p.barren), !!s.player]);
  if (ui.chips.dataset.k === key) return;
  ui.chips.dataset.k = key;
  ui.chips.innerHTML = "";
  for (const shop of sortedShops(s)) {
    const b = el("button", "v3d-chip" + (shop.open ? "" : " is-closed"));
    b.type = "button";
    b.style.setProperty("--c", STALL_CSS[shop.key] || "#2563EB");
    b.innerHTML = `<span class="v3d-chip-face">${shop.face}</span><span class="v3d-chip-t"><b>${escapeHTML(shop.name)}</b><small>${shop.open ? "🐚 " + num(shop.purse) : "Closed"}</small></span>`;
    b.addEventListener("click", () => openPanel(shop.key));
    ui.chips.appendChild(b);
  }
  const sep = el("span", "v3d-chip-sep");
  ui.chips.appendChild(sep);
  const emoji = { grove: "🌳", field: "🌱", river: "🛶", garden: "🪴" };
  for (const p of s.places) {
    const b = el("button", "v3d-chip v3d-chip-place" + (p.barren ? " is-barren" : ""));
    b.type = "button";
    b.title = p.name + ": " + p.yield;
    b.innerHTML = `<span class="v3d-chip-face">${emoji[p.place] || "📍"}</span><span class="v3d-chip-t"><b>${escapeHTML(p.name)}</b><small>${escapeHTML(p.barren ? "nothing now" : p.yield)}</small></span>`;
    b.addEventListener("click", () => openPanel("place:" + p.place));
    ui.chips.appendChild(b);
  }
}

function sortedShops(s) {
  return [...s.shops].sort((a, b) => rank(a.key) - rank(b.key));
}
function rank(k) { const i = ORDER.indexOf(k); return i < 0 ? 99 : i; }

// ---------------------------------------------------------------- panel

// Cards are moved from the page into the panel and put back when it closes,
// so every button in the panel is the page's own form.
function openPanel(key, quiet) {
  if (!gl || !isOpen) return;
  closePanel(true, true);
  const body = ui.panelBody;
  body.innerHTML = "";
  let card = null;
  if (key.startsWith("place:")) {
    card = placeCard(key.slice(6));
  } else {
    const article = document.getElementById("stall-" + key);
    if (article && isLocalPage()) {
      gl.moved = { node: article, mark: document.createComment("stall") };
      article.before(gl.moved.mark);
      card = article;
    } else {
      card = visitorStallCard(key);
    }
  }
  if (!card) return;
  body.appendChild(card);
  ui.panel.hidden = false;
  ui.panel.dataset.kind = key.startsWith("place:") ? "place" : "stall";
  ui.root.classList.add("has-panel");
  requestAnimationFrame(() => ui.panel.classList.add("is-in"));
  gl.panelFor = key;
  if (!quiet) flyTo(key);
  const want = "#3d-" + key.replace("place:", "");
  if (location.hash !== want) history.replaceState(history.state, "", want);
}

function closePanel(keepView, keepHash) {
  if (!ui || !gl) return;
  if (gl.moved) {
    gl.moved.mark.replaceWith(gl.moved.node);
    gl.moved = null;
  }
  if (gl.movedPlace) {
    gl.movedPlace.mark.replaceWith(gl.movedPlace.node);
    gl.movedPlace = null;
  }
  ui.panelBody.innerHTML = "";
  ui.panel.classList.remove("is-in");
  ui.panel.hidden = true;
  ui.root.classList.remove("has-panel");
  const was = gl.panelFor;
  gl.panelFor = null;
  if (!keepView && was) flyHome();
  if (!keepHash && isOpen && location.hash !== "#3d") history.replaceState(history.state, "", "#3d");
}

function placeCard(place) {
  const p = state && state.places.find((x) => x.place === place);
  if (!p) return null;
  const wrap = el("section", "v3d-place card");
  const emoji = { grove: "🌳", field: "🌱", river: "🛶", garden: "🪴" }[place] || "📍";
  wrap.innerHTML = `<div class="v3d-place-head"><span class="v3d-place-e">${emoji}</span><div><h2>${escapeHTML(p.name)}</h2><p class="sub">${escapeHTML(p.barren ? "Nothing grows here this season." : "Each trip uses one energy and gives " + p.yield + ".")}</p></div></div>`;
  const input = document.querySelector(`form.place input[name=place][value="${place}"]`);
  const form = input && input.closest("form");
  if (form) {
    gl.movedPlace = { node: form, mark: document.createComment("place") };
    form.before(gl.movedPlace.mark);
    wrap.appendChild(form);
    const left = document.querySelectorAll(".topbar .energy i.on").length;
    const note = el("p", "sub v3d-energy", `${left} of 10 trips left today.`);
    wrap.appendChild(note);
    if (!gatherForms.has(form)) {
      gatherForms.add(form);
      form.addEventListener("submit", () => {
        if (!isOpen || !gl || gl.movedPlace?.node !== form) return;
        gatherBurst(place);
        setTimeout(() => {
          const n = ui.panelBody.querySelector(".v3d-energy");
          if (n) n.textContent = `${document.querySelectorAll(".topbar .energy i.on").length} of 10 trips left today.`;
        }, 0);
      });
    }
  } else {
    wrap.appendChild(enterButton("Enter the haat to gather here"));
  }
  return wrap;
}

// A visitor has no stall cards on the page; show the stall's numbers and the
// way in. Trading needs a ledger, which "Enter the haat" makes.
function visitorStallCard(key) {
  const s = state && state.shops.find((x) => x.key === key);
  if (!s) return null;
  const card = el("article", "stall stall-" + key + (s.open ? "" : " is-closed") + " v3d-visit");
  const rows = s.shelves.map((sh) => `
    <div class="shelf">
      <div class="shelf-item"><span class="ico">${sh.emoji}</span><span><b>${escapeHTML(sh.label)}</b><small>${sh.stock} on the shelf</small></span></div>
      <div class="shelf-side"><span class="side-l">${s.open ? "Pays" : "Paid"}</span><span class="price">🐚 ${sh.bid}</span></div>
      <div class="shelf-side"><span class="side-l">Asks</span>${sh.has_ask ? `<span class="price ask">🐚 ${sh.ask}</span>` : `<span class="muted small">sold out</span>`}</div>
    </div>`).join("");
  card.innerHTML = `
    <div class="awning" aria-hidden="true"></div>
    <header class="stall-head">
      <span class="face" aria-hidden="true">${s.face}</span>
      <div class="who"><h2>${escapeHTML(s.name)}</h2><span class="tag">${escapeHTML(s.temperament)}</span></div>
      <div class="till" title="Cash box"><span class="mood" aria-hidden="true">${s.mood}</span><span>🐚 ${num(s.purse)}</span></div>
    </header>
    ${s.note ? `<blockquote class="note">${escapeHTML(s.note)}</blockquote>` : ""}
    <div class="shelves">${rows}</div>
    <footer class="stall-foot v3d-visit-foot"></footer>`;
  if (!s.open) {
    const shut = el("div", "v3d-closed-note", `<b>Closed.</b> ${escapeHTML(s.name)} ran out of money. A player can invest to reopen the shop and earn a share of its profits.`);
    card.querySelector(".shelves").before(shut);
  }
  card.querySelector(".v3d-visit-foot").appendChild(enterButton("Enter the haat to trade"));
  return card;
}

function enterButton(label) {
  const start = document.querySelector('form[action="/start"] button[type=submit]');
  const b = el("button", "btn btn-primary");
  b.type = "button";
  b.textContent = label;
  if (!start) { b.disabled = true; return b; }
  b.addEventListener("click", () => {
    history.replaceState(null, "", location.pathname);
    start.closest("form").requestSubmit(start);
  });
  return b;
}

function focusFromHash() {
  if (!gl) return;
  const k = location.hash.replace(/^#3d-?/, "");
  if (!k) return;
  if (ORDER.includes(k) || (state && state.shops.some((s) => s.key === k))) openPanel(k, false);
  else if (["grove", "field", "river", "garden"].includes(k)) openPanel("place:" + k, false);
}

// ---------------------------------------------------------------- three.js scene

function M(color, extra) {
  return new T.MeshStandardMaterial(Object.assign({ color, roughness: 0.86, metalness: 0, flatShading: true }, extra || {}));
}

function mesh(geo, mat, cast = true, receive = true) {
  const m = new T.Mesh(geo, mat);
  m.castShadow = cast;
  m.receiveShadow = receive;
  return m;
}

function canvasTexture(w, h, draw) {
  const c = document.createElement("canvas");
  c.width = w; c.height = h;
  const ctx = c.getContext("2d");
  draw(ctx, w, h);
  const tex = new T.CanvasTexture(c);
  tex.colorSpace = T.SRGBColorSpace;
  tex.anisotropy = 8;
  return tex;
}

function rr(ctx, x, y, w, h, r) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

// A seeded random so the village looks the same on every visit.
function rng(seed) {
  let s = seed >>> 0;
  return () => { s = (s * 1664525 + 1013904223) >>> 0; return s / 4294967296; };
}

function groundHeight(x, z) {
  const r = Math.hypot(x, z);
  const rise = smooth(48, 95, r);
  const river = smooth(5, 13, Math.abs(z + 34));
  const n = Math.sin(x * 0.07) * Math.cos(z * 0.06) * 3 + Math.sin(x * 0.17 + z * 0.11) * 1.2 + 2.5;
  return rise * river * n * 2.2;
}
function smooth(a, b, x) { const t = Math.min(1, Math.max(0, (x - a) / (b - a))); return t * t * (3 - 2 * t); }

function buildScene() {
  const canvas = ui.canvas;
  const renderer = new T.WebGLRenderer({ canvas, antialias: true, powerPreference: "high-performance" });
  renderer.setPixelRatio(Math.min(devicePixelRatio || 1, 2));
  renderer.outputColorSpace = T.SRGBColorSpace;
  renderer.toneMapping = T.ACESFilmicToneMapping;
  renderer.toneMappingExposure = 1.0;
  renderer.shadowMap.enabled = true;
  renderer.shadowMap.type = T.PCFSoftShadowMap;
  canvas.addEventListener("webglcontextlost", (e) => { e.preventDefault(); showFallback(); });

  const scene = new T.Scene();
  scene.fog = new T.Fog(0xF4E6CF, 70, 190);
  const camera = new T.PerspectiveCamera(34, 1, 0.5, 600);

  const phone = Math.min(innerWidth, innerHeight) < 600;
  gl = {
    renderer, scene, camera, phone, stalls: {}, places: {}, clock: new T.Clock(),
    view: { az: 0, pol: 1.02, r: 44, tx: 0, ty: 1.4, tz: -2 }, goal: null, home: null,
    dayFrac: 0.35, panelFor: null, moved: null, movedPlace: null, intro: 1, slow: 0, frames: 0, ratio: renderer.getPixelRatio(),
    hover: null, bursts: [],
  };

  // Light: hemisphere sky, a warm sun with soft shadows, and lantern lights.
  gl.hemi = new T.HemisphereLight(0xDDE9FF, 0xC89A63, 1.1);
  scene.add(gl.hemi);
  const sun = new T.DirectionalLight(0xFFF1DC, 2.6);
  sun.castShadow = true;
  sun.shadow.mapSize.set(phone ? 1024 : 2048, phone ? 1024 : 2048);
  Object.assign(sun.shadow.camera, { left: -42, right: 42, top: 42, bottom: -42, near: 1, far: 160 });
  sun.shadow.bias = -0.0004;
  sun.shadow.normalBias = 0.04;
  sun.shadow.radius = 3;
  scene.add(sun, sun.target);
  gl.sun = sun;

  buildSky();
  buildGround();
  buildPlaza();
  buildStalls();
  buildLanterns();
  buildGrove();
  buildGarden();
  buildField();
  buildRiver();
  buildRoad();
  buildHuts();
  buildLampStand();
  buildRain();
  buildClouds();

  setupControls();
  resize();
  new ResizeObserver(resize).observe(ui.stage);
}

function buildSky() {
  const uniforms = { top: { value: new T.Color(0x7FB2F0) }, horizon: { value: new T.Color(0xF6E7CF) }, bottom: { value: new T.Color(0xE9D3AF) } };
  const mat = new T.ShaderMaterial({
    uniforms, side: T.BackSide, depthWrite: false, toneMapped: false,
    vertexShader: "varying vec3 vP; void main(){ vP = normalize(position); gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0); }",
    fragmentShader: "uniform vec3 top; uniform vec3 horizon; uniform vec3 bottom; varying vec3 vP; void main(){ float h = vP.y; vec3 c = h > 0.0 ? mix(horizon, top, pow(smoothstep(0.0, 0.55, h), 0.7)) : mix(horizon, bottom, smoothstep(0.0, 0.25, -h)); gl_FragColor = vec4(c, 1.0);\n#include <colorspace_fragment>\n}",
  });
  const sky = new T.Mesh(new T.SphereGeometry(400, 32, 16), mat);
  sky.renderOrder = -1;
  gl.scene.add(sky);
  gl.sky = { mesh: sky, u: uniforms };

  // Stars, faded in at night.
  const r = rng(7), pts = [];
  for (let i = 0; i < 500; i++) {
    const th = r() * Math.PI * 2, ph = Math.acos(0.15 + r() * 0.85);
    pts.push(Math.sin(ph) * Math.cos(th) * 380, Math.cos(ph) * 380, Math.sin(ph) * Math.sin(th) * 380);
  }
  const g = new T.BufferGeometry();
  g.setAttribute("position", new T.Float32BufferAttribute(pts, 3));
  gl.stars = new T.Points(g, new T.PointsMaterial({ color: 0xFFF6E0, size: 1.6, sizeAttenuation: false, transparent: true, opacity: 0, fog: false, depthWrite: false }));
  gl.scene.add(gl.stars);

  // The moon.
  const moonTex = canvasTexture(128, 128, (ctx) => {
    const g2 = ctx.createRadialGradient(64, 64, 10, 64, 64, 64);
    g2.addColorStop(0, "rgba(255,248,228,1)"); g2.addColorStop(0.32, "rgba(255,244,214,.95)"); g2.addColorStop(0.38, "rgba(255,240,210,.25)"); g2.addColorStop(1, "rgba(255,240,210,0)");
    ctx.fillStyle = g2; ctx.fillRect(0, 0, 128, 128);
  });
  gl.moon = new T.Sprite(new T.SpriteMaterial({ map: moonTex, transparent: true, depthWrite: false, fog: false, opacity: 0 }));
  gl.moon.scale.set(40, 40, 1);
  gl.scene.add(gl.moon);
}

function buildGround() {
  const size = 260, seg = 90;
  const g = new T.PlaneGeometry(size, size, seg, seg).rotateX(-Math.PI / 2).toNonIndexed();
  const pos = g.attributes.position;
  for (let i = 0; i < pos.count; i++) pos.setY(i, groundHeight(pos.getX(i), pos.getZ(i)));
  g.computeVertexNormals();
  const colors = new Float32Array(pos.count * 3);
  g.setAttribute("color", new T.BufferAttribute(colors, 3));
  const mat = new T.MeshStandardMaterial({ vertexColors: true, roughness: 0.95, flatShading: true });
  const ground = mesh(g, mat, false, true);
  gl.scene.add(ground);
  gl.ground = ground;
  gl.groundTone = null;
}

// Ground colours follow the season: dry summer, green monsoon, golden festival.
function paintGround(season) {
  if (gl.groundTone === season) return;
  gl.groundTone = season;
  const g = gl.ground.geometry, pos = g.attributes.position, col = g.attributes.color;
  const tone = {
    garmi: [0xE2C08A, 0xCDB46E, 0xB9A15E],
    barsaat: [0xA9C47A, 0x86AE5F, 0x6F9A4E],
    tyohar: [0xD9C27E, 0xB9BE6C, 0xA0A95A],
  }[season] || [0xE2C08A, 0xCDB46E, 0xB9A15E];
  const a = new T.Color(tone[0]), b = new T.Color(tone[1]), c3 = new T.Color(tone[2]), earth = new T.Color(0xE7CC9E), tmp = new T.Color();
  const r = rng(11);
  for (let i = 0; i < pos.count; i += 3) {
    const x = (pos.getX(i) + pos.getX(i + 1) + pos.getX(i + 2)) / 3, z = (pos.getZ(i) + pos.getZ(i + 1) + pos.getZ(i + 2)) / 3;
    const d = Math.hypot(x, z + 1);
    const y = (pos.getY(i) + pos.getY(i + 1) + pos.getY(i + 2)) / 3;
    tmp.copy(a).lerp(b, r());
    if (y > 3) tmp.lerp(c3, Math.min(1, (y - 3) / 8));
    tmp.lerp(earth, 1 - smooth(14, 24, d));
    tmp.multiplyScalar(0.94 + r() * 0.1);
    for (let k = 0; k < 3; k++) col.setXYZ(i + k, tmp.r, tmp.g, tmp.b);
  }
  col.needsUpdate = true;
}

function radialTexture(inner, outer) {
  return canvasTexture(256, 256, (ctx) => {
    const g = ctx.createRadialGradient(128, 128, 0, 128, 128, 128);
    g.addColorStop(0, inner); g.addColorStop(1, outer);
    ctx.fillStyle = g; ctx.fillRect(0, 0, 256, 256);
  });
}

function placed(obj, x, y, z) { obj.position.set(x, y, z); return obj; }

function blobShadow(w, d, opacity = 0.32) {
  if (!gl.blobTex) gl.blobTex = radialTexture("rgba(60,30,10,1)", "rgba(60,30,10,0)");
  const m = new T.Mesh(new T.PlaneGeometry(w, d).rotateX(-Math.PI / 2), new T.MeshBasicMaterial({ map: gl.blobTex, transparent: true, opacity, depthWrite: false }));
  m.position.y = 0.025;
  m.renderOrder = 1;
  return m;
}

function buildPlaza() {
  const g = new T.Group();
  // Packed earth and stone flags, with a terracotta ring.
  const base = mesh(new T.CylinderGeometry(14.5, 14.8, 0.12, 48), M(0xE8D3AE), false, true);
  base.position.y = 0.06;
  const ring = mesh(new T.CylinderGeometry(13.6, 13.6, 0.16, 48, 1, true), M(C.terracotta), false, true);
  ring.position.y = 0.08;
  g.add(base, ring);
  const r = rng(3), flag = new T.BoxGeometry(1, 0.06, 1);
  const flags = new T.InstancedMesh(flag, M(0xF1E1C4), 160);
  flags.receiveShadow = true;
  const m4 = new T.Matrix4(), q = new T.Quaternion(), s = new T.Vector3(), p = new T.Vector3(), tint = new T.Color();
  let n = 0;
  for (let i = 0; i < 400 && n < 160; i++) {
    const a = r() * Math.PI * 2, d = 3.4 + Math.sqrt(r()) * 9.6;
    p.set(Math.cos(a) * d, 0.13, Math.sin(a) * d - 1);
    q.setFromAxisAngle(new T.Vector3(0, 1, 0), r() * Math.PI);
    const sz = 0.9 + r() * 0.8;
    s.set(sz, 1, sz * (0.7 + r() * 0.5));
    m4.compose(p, q, s);
    flags.setMatrixAt(n, m4);
    flags.setColorAt(n, tint.setHSL(0.09 + r() * 0.03, 0.35 + r() * 0.15, 0.82 + r() * 0.08));
    n++;
  }
  flags.count = n;
  g.add(flags);

  // The Haat logo laid out as a rangoli at the heart of the square.
  const logo = new Image();
  logo.src = "/static/logo-512.png";
  const tex = new T.Texture(logo);
  tex.colorSpace = T.SRGBColorSpace;
  logo.onload = () => { tex.needsUpdate = true; };
  const rangoli = new T.Mesh(new T.CircleGeometry(2.9, 48).rotateX(-Math.PI / 2), new T.MeshStandardMaterial({ map: tex, emissiveMap: tex, emissive: 0xFFFFFF, emissiveIntensity: 0.3, transparent: true, roughness: 0.9, polygonOffset: true, polygonOffsetFactor: -2 }));
  rangoli.position.set(0, 0.17, 1.2);
  rangoli.receiveShadow = true;
  g.add(rangoli);
  const petals = new T.InstancedMesh(new T.IcosahedronGeometry(0.13, 0), M(0xF59E0B, { emissive: 0x7A3A00, emissiveIntensity: 0.15 }), 36);
  for (let i = 0; i < 36; i++) {
    const a = (i / 36) * Math.PI * 2;
    m4.compose(new T.Vector3(Math.cos(a) * 3.15, 0.2, Math.sin(a) * 3.15 + 1.2), q.identity(), s.set(1, 0.6, 1));
    petals.setMatrixAt(i, m4);
    petals.setColorAt(i, tint.set(i % 2 ? 0xF59E0B : 0xEA580C));
  }
  petals.castShadow = true;
  g.add(petals);
  gl.scene.add(g);
}

// ---------------------------------------------------------------- stalls

const ARC = { cx: 0, cz: 3, r: 11.5, angles: [-51, -17, 17, 51] };

function stallSpot(i) {
  const a = (ARC.angles[i] * Math.PI) / 180;
  return { x: ARC.cx + ARC.r * Math.sin(a), z: ARC.cz - ARC.r * Math.cos(a), rot: -a, a };
}

function stripeTexture(hex, scallop) {
  return canvasTexture(512, scallop ? 128 : 256, (ctx, w, h) => {
    const n = 8, sw = w / n;
    for (let i = 0; i < n; i++) {
      ctx.fillStyle = i % 2 ? "#FFF1DC" : hex;
      ctx.fillRect(i * sw, 0, sw, h);
    }
    if (scallop) {
      ctx.globalCompositeOperation = "destination-out";
      for (let i = 0; i < n; i++) {
        ctx.beginPath();
        ctx.ellipse(i * sw + sw / 2, h, sw / 2, h * 0.42, 0, 0, Math.PI * 2);
        ctx.fill();
      }
      ctx.globalCompositeOperation = "source-over";
    }
    const sh = ctx.createLinearGradient(0, 0, 0, h);
    sh.addColorStop(0, "rgba(0,0,0,0)"); sh.addColorStop(1, "rgba(60,20,0,.18)");
    ctx.globalCompositeOperation = "source-atop";
    ctx.fillStyle = sh; ctx.fillRect(0, 0, w, h);
  });
}

function buildStalls() {
  ORDER.forEach((key, i) => {
    const spot = stallSpot(i);
    const st = makeStall(key);
    st.group.position.set(spot.x, 0, spot.z);
    st.group.rotation.y = spot.rot;
    st.spot = spot;
    st.board.rotation.y = spot.a * 0.45;
    st.board.parent.children.forEach((c) => { if (c.userData.boardPart) c.rotation.y = spot.a * 0.45; });
    gl.scene.add(st.group);
    gl.stalls[key] = st;
  });
}

function makeStall(key) {
  const colour = STALL_COLOURS[key] || 0x2563EB, css = STALL_CSS[key] || "#2563EB";
  const g = new T.Group();
  g.userData.pick = key;
  const W = 5.4, D = 3.2;
  const wood = M(C.wood), dark = M(C.darkwood), plaster = M(C.plaster);
  // Raised plinth, back wall and side walls of lime-washed plaster.
  const plinth = mesh(new T.BoxGeometry(W + 0.5, 0.35, D + 0.4), M(0xD8BF98));
  plinth.position.y = 0.17;
  const back = mesh(new T.BoxGeometry(W, 3.7, 0.22), plaster);
  back.position.set(0, 2.2, -D / 2 + 0.11);
  const trim = mesh(new T.BoxGeometry(W + 0.02, 0.32, 0.25), M(colour));
  trim.position.set(0, 0.52, -D / 2 + 0.12);
  const sideL = mesh(new T.BoxGeometry(0.2, 3.7, D * 0.55), plaster);
  sideL.position.set(-W / 2 + 0.1, 2.2, -D / 2 + D * 0.275);
  const sideR = sideL.clone();
  sideR.position.x = W / 2 - 0.1;
  g.add(plinth, back, trim, sideL, sideR);
  // Front posts.
  for (const x of [-W / 2 + 0.16, W / 2 - 0.16]) {
    const post = mesh(new T.CylinderGeometry(0.11, 0.13, 3.5, 6), dark);
    post.position.set(x, 2.1, D / 2 - 0.15);
    g.add(post);
  }
  // Counter with a painted front.
  const counter = mesh(new T.BoxGeometry(W - 0.6, 0.95, 1.0), wood);
  counter.position.set(0, 0.82, D / 2 - 0.75);
  const top = mesh(new T.BoxGeometry(W - 0.4, 0.1, 1.15), dark);
  top.position.set(0, 1.33, D / 2 - 0.75);
  const facade = mesh(new T.BoxGeometry(W - 0.9, 0.62, 0.04), M(colour));
  facade.position.set(0, 0.84, D / 2 - 0.24);
  const facadeIn = mesh(new T.BoxGeometry(W - 1.3, 0.38, 0.05), M(C.cream));
  facadeIn.position.set(0, 0.84, D / 2 - 0.22);
  g.add(counter, top, facade, facadeIn);
  // Striped cloth awning with a scalloped valance.
  const awnMat = new T.MeshStandardMaterial({ map: stripeTexture(css, false), roughness: 0.9, side: T.DoubleSide });
  const awning = mesh(new T.PlaneGeometry(W + 0.5, D + 0.6, 1, 1), awnMat);
  awning.rotation.x = -Math.PI / 2 + 0.36;
  awning.position.set(0, 3.95, 0.05);
  const valMat = new T.MeshStandardMaterial({ map: stripeTexture(css, true), transparent: true, alphaTest: 0.5, roughness: 0.9, side: T.DoubleSide });
  const valance = mesh(new T.PlaneGeometry(W + 0.5, 0.5), valMat);
  valance.position.set(0, 3.19, D / 2 + 0.33);
  g.add(awning, valance);
  // Signboard above the awning, drawn from the shop's numbers.
  const boardCanvas = document.createElement("canvas");
  boardCanvas.width = 1024; boardCanvas.height = 560;
  const boardTex = new T.CanvasTexture(boardCanvas);
  boardTex.colorSpace = T.SRGBColorSpace;
  boardTex.anisotropy = 8;
  const boardMat = new T.MeshBasicMaterial({ map: boardTex, toneMapped: false, transparent: true });
  const board = new T.Mesh(new T.PlaneGeometry(5.6, 3.06), boardMat);
  board.position.set(0, 5.95, -0.6);
  board.rotation.x = -0.12;
  const boardBack = mesh(new T.BoxGeometry(5.75, 3.2, 0.12), dark);
  boardBack.position.set(0, 5.95, -0.68);
  boardBack.rotation.x = -0.12;
  boardBack.userData.boardPart = true;
  for (const x of [-2.3, 2.3]) {
    const leg = mesh(new T.BoxGeometry(0.14, 2.4, 0.14), dark);
    leg.position.set(x, 4.2, -0.85);
    g.add(leg);
  }
  g.add(board, boardBack);
  // Goods on the counter: one basket per item, filled from the shelf stock.
  const goods = [makeBasket(-1.15), makeBasket(1.15)];
  goods.forEach((b) => g.add(b.group));
  // The shopkeeper behind the counter.
  const keeper = makeKeeper(key, colour);
  keeper.position.set(0, 0.35, -0.2);
  keeper.scale.setScalar(1.12);
  g.add(keeper);
  // Rolling shutter: down when the shop is closed.
  const shutTex = canvasTexture(256, 256, (ctx, w, h) => {
    for (let y = 0; y < h; y += 16) {
      const gr = ctx.createLinearGradient(0, y, 0, y + 16);
      gr.addColorStop(0, "#C9D2DE"); gr.addColorStop(0.5, "#A9B5C6"); gr.addColorStop(1, "#8794A8");
      ctx.fillStyle = gr; ctx.fillRect(0, y, w, 16);
    }
  });
  shutTex.wrapT = T.RepeatWrapping;
  shutTex.repeat.set(1, 3);
  const shutter = mesh(new T.BoxGeometry(W - 0.25, 2.85, 0.06), new T.MeshStandardMaterial({ map: shutTex, roughness: 0.5, metalness: 0.35 }));
  shutter.position.set(0, 1.75, D / 2 - 0.12);
  const roll = mesh(new T.CylinderGeometry(0.2, 0.2, W - 0.2, 10).rotateZ(Math.PI / 2), M(0x8794A8, { metalness: 0.3, roughness: 0.5 }));
  roll.position.set(0, 3.25, D / 2 - 0.12);
  const signTex = canvasTexture(512, 256, (ctx, w, h) => {
    ctx.fillStyle = "#FFFFFF"; rr(ctx, 8, 8, w - 16, h - 16, 28); ctx.fill();
    ctx.lineWidth = 12; ctx.strokeStyle = "#9F1239"; rr(ctx, 14, 14, w - 28, h - 28, 24); ctx.stroke();
    ctx.fillStyle = "#9F1239"; ctx.textAlign = "center";
    ctx.font = '800 92px "Baloo 2", sans-serif'; ctx.fillText("Closed", w / 2, 122);
    ctx.fillStyle = "#52627A"; ctx.font = '600 34px Inter, sans-serif'; ctx.fillText("Invest to reopen", w / 2, 192);
  });
  const sign = new T.Mesh(new T.PlaneGeometry(2.2, 1.1), new T.MeshBasicMaterial({ map: signTex, toneMapped: false, transparent: true }));
  sign.position.set(0.15, 1.95, D / 2 - 0.05);
  sign.rotation.z = -0.04;
  const shutterGroup = new T.Group();
  shutterGroup.add(shutter, sign);
  g.add(shutterGroup, roll);
  // Marigold garland for a shop that reopened after a rescue.
  const garland = makeGarland(W + 0.3);
  garland.position.set(0, 3.0, D / 2 + 0.42);
  g.add(garland);
  // Lantern light inside the stall, for the evening.
  const lamp = new T.PointLight(0xFFB86B, 0, 7, 1.6);
  lamp.position.set(0, 2.4, 0.4);
  g.add(lamp);
  const shadow = blobShadow(W + 2.4, D + 2.6, 0.38);
  g.add(shadow);
  return {
    key, group: g, board, boardCanvas, boardTex, boardKey: "", goods, keeper,
    shutterGroup, shutter: 0, shutterGoal: 0, sign, garland, lamp, awnMat, valMat, colour, css,
  };
}

function makeBasket(x) {
  const group = new T.Group();
  group.position.set(x, 1.38, 0.85);
  const basket = mesh(new T.CylinderGeometry(0.78, 0.6, 0.38, 9, 1, true), new T.MeshStandardMaterial({ color: 0xB8834A, roughness: 0.95, flatShading: true, side: T.DoubleSide }));
  basket.position.y = 0.19;
  const bottom = mesh(new T.CircleGeometry(0.6, 9).rotateX(-Math.PI / 2), M(0x9B6B3A));
  bottom.position.y = 0.02;
  const rim = mesh(new T.TorusGeometry(0.78, 0.06, 4, 12).rotateX(Math.PI / 2), M(0x8D5E33));
  rim.position.y = 0.38;
  group.add(basket, bottom, rim);
  return { group, token: null, items: null, heap: null, count: -1 };
}

const ITEM_LOOK = {
  mango: { geo: () => new T.IcosahedronGeometry(0.17, 1).scale(1, 0.9, 1.25), color: C.mango, per: 2, max: 30 },
  fish: { geo: () => new T.OctahedronGeometry(0.2, 0).scale(1.9, 0.45, 0.7), color: C.fish, per: 2, max: 24, metal: 0.35 },
  chilli: { geo: () => new T.ConeGeometry(0.06, 0.34, 5).rotateZ(Math.PI / 2), color: C.chilli, per: 1, max: 48 },
  rice: { geo: () => new T.IcosahedronGeometry(0.1, 0), color: C.rice, per: 1, max: 60, heap: true },
  pickle: { geo: () => new T.CylinderGeometry(0.15, 0.17, 0.36, 8), color: C.jar, per: 1, max: 14, jar: true },
};

// Fill a basket with as many pieces as the shelf holds, a few units per piece.
function fillBasket(b, token, stock) {
  const look = ITEM_LOOK[token] || ITEM_LOOK.mango;
  if (b.token !== token) {
    if (b.items) b.group.remove(b.items);
    if (b.lids) b.group.remove(b.lids);
    if (b.heap) b.group.remove(b.heap);
    const mat = M(look.color, { roughness: look.metal ? 0.45 : 0.7, metalness: look.metal || 0 });
    b.items = new T.InstancedMesh(look.geo(), mat, look.max);
    b.items.castShadow = true;
    b.group.add(b.items);
    if (look.jar) {
      b.lids = new T.InstancedMesh(new T.CylinderGeometry(0.11, 0.11, 0.08, 8), M(0xF6E7C8), look.max);
      b.group.add(b.lids);
    }
    if (look.heap) {
      b.heap = mesh(new T.ConeGeometry(0.72, 0.6, 9), M(C.rice));
      b.group.add(b.heap);
    }
    b.token = token;
    b.count = -1;
    b.layout = null;
  }
  const n = Math.min(look.max, Math.ceil(stock / look.per));
  if (n === b.count) return;
  b.count = n;
  const m4 = new T.Matrix4(), q = new T.Quaternion(), e = new T.Euler(), r = rng(token.length * 97 + 5);
  const s1 = new T.Vector3(1, 1, 1);
  if (look.jar) {
    for (let i = 0; i < n; i++) {
      const row = Math.floor(i / 5), col = i % 5;
      const p = new T.Vector3((col - 2) * 0.3, 0.2 + row * 0.38, (row % 2 ? 0.1 : -0.1));
      m4.compose(p, q.identity(), s1);
      b.items.setMatrixAt(i, m4);
      m4.compose(p.clone().setY(p.y + 0.21), q, s1);
      b.lids.setMatrixAt(i, m4);
    }
    b.items.count = n;
    b.lids.count = n;
    b.items.instanceMatrix.needsUpdate = true;
    b.lids.instanceMatrix.needsUpdate = true;
    return;
  }
  if (look.heap) {
    const f = Math.min(1, stock / 40);
    b.heap.visible = stock > 0;
    b.heap.scale.set(0.55 + f * 0.45, 0.25 + f * 1.1, 0.55 + f * 0.45);
    b.heap.position.y = 0.06 + b.heap.scale.y * 0.3;
    b.items.count = 0;
    return;
  }
  for (let i = 0; i < n; i++) {
    const layer = Math.floor(i / 9), k = i % 9;
    const rad = k === 0 ? 0 : 0.23 + (k % 2) * 0.22 - layer * 0.07;
    const a = (k / 8) * Math.PI * 2 + layer * 0.6;
    const p = new T.Vector3(Math.cos(a) * rad, 0.14 + layer * 0.17, Math.sin(a) * rad);
    e.set(r() * 3, r() * 3, r() * 3);
    m4.compose(p, q.setFromEuler(e), s1);
    b.items.setMatrixAt(i, m4);
  }
  b.items.count = n;
  b.items.instanceMatrix.needsUpdate = true;
}

function makeKeeper(key, colour) {
  const g = new T.Group();
  const body = mesh(new T.CylinderGeometry(0.36, 0.58, 1.75, 8), M(colour));
  body.position.y = 0.88;
  const shawl = mesh(new T.CylinderGeometry(0.4, 0.42, 0.4, 8), M(key === "lakshmi" ? 0xFFF1DC : key === "meena" ? 0xF59E0B : 0xFFF1DC));
  shawl.position.y = 1.62;
  const head = mesh(new T.IcosahedronGeometry(0.33, 1), M(C.skin));
  head.position.y = 2.08;
  g.add(body, shawl, head);
  if (key === "ramu") {
    const turban = mesh(new T.CylinderGeometry(0.36, 0.33, 0.28, 10), M(0xFFFFFF));
    turban.position.y = 2.32;
    const tail = mesh(new T.IcosahedronGeometry(0.1, 0), M(0xFFFFFF));
    tail.position.set(0.3, 2.28, -0.12);
    const mous = mesh(new T.BoxGeometry(0.3, 0.06, 0.08), M(0xEDEDED));
    mous.position.set(0, 1.98, 0.3);
    g.add(turban, tail, mous);
  } else if (key === "bashir") {
    const cap = mesh(new T.CylinderGeometry(0.3, 0.32, 0.18, 10), M(0xF5F5F4));
    cap.position.y = 2.36;
    const beard = mesh(new T.ConeGeometry(0.24, 0.42, 6).rotateX(Math.PI), M(0x3B2A20));
    beard.position.set(0, 1.86, 0.15);
    g.add(cap, beard);
  } else {
    const hair = mesh(new T.IcosahedronGeometry(0.35, 1), M(key === "lakshmi" ? 0xBFBFBF : 0x1F1410));
    hair.scale.set(1, 0.8, 1);
    hair.position.set(0, 2.18, -0.06);
    const bun = mesh(new T.IcosahedronGeometry(0.17, 0), hair.material);
    bun.position.set(0, 2.12, -0.38);
    const bindi = mesh(new T.CircleGeometry(0.035, 6), new T.MeshBasicMaterial({ color: 0xB91C1C }));
    bindi.position.set(0, 2.17, 0.325);
    g.add(hair, bun, bindi);
    if (key === "meena") {
      const dupatta = mesh(new T.ConeGeometry(0.42, 0.5, 8, 1, true), M(0xF59E0B, { side: T.DoubleSide }));
      dupatta.position.set(0, 2.2, -0.08);
      g.add(dupatta);
    }
  }
  return g;
}

function makeGarland(width) {
  const g = new T.Group();
  const n = 22, geo = new T.IcosahedronGeometry(0.16, 0);
  const inst = new T.InstancedMesh(geo, M(0xF59E0B, { emissive: 0x7A3A00, emissiveIntensity: 0.25 }), n * 2);
  const m4 = new T.Matrix4(), q = new T.Quaternion(), s = new T.Vector3(1, 1, 1), c = new T.Color();
  let k = 0;
  for (let loop = 0; loop < 2; loop++) {
    for (let i = 0; i < n; i++) {
      const t = i / (n - 1);
      const sag = Math.sin(t * Math.PI) * (loop ? 0.25 : 0.45);
      m4.compose(new T.Vector3((t - 0.5) * width, -sag - (loop ? 0.12 : 0), loop ? 0.04 : 0), q, s);
      inst.setMatrixAt(k, m4);
      inst.setColorAt(k, c.set(i % 3 === 0 ? 0xEA580C : loop ? 0xFBBF24 : 0xF59E0B));
      k++;
    }
  }
  inst.castShadow = true;
  g.add(inst);
  g.visible = false;
  return g;
}

function drawBoard(st, shop) {
  const c = st.boardCanvas, ctx = c.getContext("2d"), W = c.width, H = c.height;
  ctx.clearRect(0, 0, W, H);
  // Board and painted band.
  ctx.fillStyle = "#FFF8EC";
  rr(ctx, 6, 6, W - 12, H - 12, 34); ctx.fill();
  ctx.save();
  rr(ctx, 6, 6, W - 12, H - 12, 34); ctx.clip();
  ctx.fillStyle = st.css; ctx.fillRect(0, 0, W, 150);
  ctx.fillStyle = "rgba(255,255,255,.12)";
  for (let x = -40; x < W; x += 56) { ctx.beginPath(); ctx.moveTo(x, 0); ctx.lineTo(x + 28, 0); ctx.lineTo(x - 22, 150); ctx.lineTo(x - 50, 150); ctx.fill(); }
  ctx.restore();
  ctx.lineWidth = 10; ctx.strokeStyle = "#5C1F0B";
  rr(ctx, 9, 9, W - 18, H - 18, 32); ctx.stroke();
  // Name, temperament and cash box.
  ctx.fillStyle = "#FFF1DC"; ctx.textBaseline = "alphabetic"; ctx.textAlign = "left";
  ctx.font = '800 76px "Baloo 2", sans-serif';
  ctx.fillText(shop.name, 44, 96);
  ctx.font = '700 34px Inter, sans-serif';
  ctx.fillStyle = "rgba(255,241,220,.9)";
  ctx.fillText(shop.temperament + (shop.reopened ? "  ·  reopened" : ""), 46, 134);
  ctx.textAlign = "right";
  ctx.font = '800 64px "Baloo 2", sans-serif';
  ctx.fillStyle = "#FFF1DC";
  const purse = "🐚 " + num(shop.purse);
  ctx.fillText(purse, W - 44, 98);
  const pw = ctx.measureText(purse).width;
  ctx.font = '54px "Apple Color Emoji", "Segoe UI Emoji", "Noto Color Emoji", sans-serif';
  ctx.fillText(shop.mood, W - 64 - pw, 94);
  // One row per item.
  const rowH = 172;
  shop.shelves.slice(0, 2).forEach((sh, i) => {
    const y = 170 + i * (rowH + 10);
    ctx.fillStyle = i % 2 ? "#FFF1DC" : "#FBEBD3";
    rr(ctx, 30, y, W - 60, rowH, 22); ctx.fill();
    ctx.textAlign = "left";
    ctx.font = '84px "Apple Color Emoji", "Segoe UI Emoji", "Noto Color Emoji", sans-serif';
    ctx.fillText(sh.emoji, 52, y + 116);
    ctx.fillStyle = "#0B1B33";
    ctx.font = '800 54px "Baloo 2", sans-serif';
    ctx.fillText(sh.label, 160, y + 80);
    ctx.fillStyle = "#52627A";
    ctx.font = '700 38px Inter, sans-serif';
    ctx.fillText(sh.stock === 0 ? "none left" : sh.stock + " on shelf", 162, y + 132);
    // Prices.
    const px = 590, ax = 800;
    ctx.fillStyle = "#52627A"; ctx.font = '800 30px Inter, sans-serif';
    ctx.fillText(shop.open ? "PAYS" : "PAID", px, y + 54);
    ctx.fillText("ASKS", ax, y + 54);
    ctx.fillStyle = "#0B1B33"; ctx.font = '800 76px "Baloo 2", sans-serif';
    ctx.fillText(String(sh.bid), px, y + 134);
    if (sh.has_ask) { ctx.fillStyle = "#4C1D95"; ctx.fillText(String(sh.ask), ax, y + 134); }
    else { ctx.fillStyle = "#9F1239"; ctx.font = '700 32px Inter, sans-serif'; ctx.fillText("sold out", ax, y + 116); }
  });
  if (!shop.open) {
    ctx.fillStyle = "rgba(244,236,226,.55)";
    ctx.fillRect(20, 160, W - 40, H - 180);
    ctx.save();
    ctx.translate(W / 2, H / 2 + 80);
    ctx.rotate(-0.08);
    ctx.lineWidth = 10; ctx.strokeStyle = "#9F1239"; ctx.fillStyle = "rgba(255,255,255,.92)";
    rr(ctx, -250, -70, 500, 140, 22); ctx.fill(); ctx.stroke();
    ctx.fillStyle = "#9F1239"; ctx.textAlign = "center"; ctx.font = '800 88px "Baloo 2", sans-serif';
    ctx.fillText("CLOSED", 0, 30);
    ctx.restore();
  }
  st.boardTex.needsUpdate = true;
}

// ---------------------------------------------------------------- lanterns

function glowSprite(color, size) {
  if (!gl.glowTex) gl.glowTex = radialTexture("rgba(255,255,255,1)", "rgba(255,255,255,0)");
  const s = new T.Sprite(new T.SpriteMaterial({ map: gl.glowTex, color, transparent: true, blending: T.AdditiveBlending, depthWrite: false, opacity: 0 }));
  s.scale.set(size, size, 1);
  return s;
}

function buildLanterns() {
  gl.lanterns = [];
  gl.glows = [];
  const poleMat = M(C.darkwood);
  const poleAngles = [-68, -34, 0, 34, 68];
  const poles = poleAngles.map((deg) => {
    const a = (deg * Math.PI) / 180, R = ARC.r + 2.6;
    const x = ARC.cx + R * Math.sin(a), z = ARC.cz - R * Math.cos(a);
    const pole = mesh(new T.CylinderGeometry(0.1, 0.14, 9.2, 6), poleMat);
    pole.position.set(x, 4.6, z);
    gl.scene.add(pole);
    const cap = mesh(new T.ConeGeometry(0.24, 0.4, 6), M(C.brass, { metalness: 0.5, roughness: 0.4 }));
    cap.position.set(x, 9.35, z);
    gl.scene.add(cap);
    return new T.Vector3(x, 9.0, z);
  });
  const colours = [0xF97316, 0xDB2777, 0xFDE68A, 0xF59E0B, 0xE11D48];
  const lanternGeo = new T.CylinderGeometry(0.22, 0.22, 0.5, 6);
  const capGeo = new T.CylinderGeometry(0.12, 0.24, 0.12, 6);
  let k = 0;
  for (let i = 0; i < poles.length - 1; i++) {
    const a = poles[i], b = poles[i + 1];
    const mid = a.clone().lerp(b, 0.5); mid.y -= 0.9;
    const curve = new T.QuadraticBezierCurve3(a, mid, b);
    const pts = curve.getPoints(24);
    const line = new T.Line(new T.BufferGeometry().setFromPoints(pts), new T.LineBasicMaterial({ color: 0x3B2A20 }));
    gl.scene.add(line);
    for (let j = 1; j <= 4; j++) {
      const p = curve.getPoint(j / 5);
      const col = colours[k++ % colours.length];
      const mat = new T.MeshStandardMaterial({ color: col, emissive: col, emissiveIntensity: 0.15, roughness: 0.6, flatShading: true });
      const lan = mesh(lanternGeo, mat, true, false);
      lan.position.copy(p).add(new T.Vector3(0, -0.45, 0));
      const capT = mesh(capGeo, M(C.umber), false, false);
      capT.position.copy(lan.position).add(new T.Vector3(0, 0.3, 0));
      const capB = capT.clone(); capB.rotation.x = Math.PI; capB.position.y = lan.position.y - 0.3;
      gl.scene.add(lan, capT, capB);
      const glow = glowSprite(col, 2.4);
      glow.position.copy(lan.position);
      gl.scene.add(glow);
      gl.lanterns.push(mat);
      gl.glows.push(glow);
    }
  }
  // Warm light over the square for the evening.
  gl.squareLights = [-6, 6].map((x) => {
    const l = new T.PointLight(0xFFAA55, 0, 26, 1.4);
    l.position.set(x, 6.5, -3);
    gl.scene.add(l);
    return l;
  });
}

// ---------------------------------------------------------------- places

function placeLabel(text, sub) {
  const tex = canvasTexture(640, 200, (ctx, w, h) => {
    ctx.fillStyle = "rgba(255,255,255,.94)";
    rr(ctx, 8, 8, w - 16, h - 46, 40); ctx.fill();
    ctx.beginPath(); ctx.moveTo(w / 2 - 20, h - 40); ctx.lineTo(w / 2, h - 12); ctx.lineTo(w / 2 + 20, h - 40); ctx.fill();
    ctx.textAlign = "center";
    ctx.fillStyle = "#0B1B33"; ctx.font = '800 58px "Baloo 2", sans-serif';
    ctx.fillText(text, w / 2, 78);
    ctx.fillStyle = "#52627A"; ctx.font = '600 36px Inter, sans-serif';
    ctx.fillText(sub, w / 2, 128);
  });
  const s = new T.Sprite(new T.SpriteMaterial({ map: tex, transparent: true, depthWrite: false, toneMapped: false }));
  s.scale.set(5.6, 1.75, 1);
  s.renderOrder = 5;
  return s;
}

function setPlaceLabel(pl, text, sub) {
  const k = text + "|" + sub;
  if (pl.labelKey === k) return;
  pl.labelKey = k;
  if (pl.label) { pl.group.remove(pl.label); pl.label.material.map.dispose(); pl.label.material.dispose(); }
  pl.label = placeLabel(text, sub);
  pl.label.position.copy(pl.labelAt);
  pl.group.add(pl.label);
}

function tree(r, fruit, scale = 1) {
  const g = new T.Group();
  const trunk = mesh(new T.CylinderGeometry(0.22 * scale, 0.34 * scale, 2.2 * scale, 6), M(0x6B4423));
  trunk.position.y = 1.1 * scale;
  g.add(trunk);
  const leaf = M(r() > 0.5 ? C.leaf : C.leafDark);
  for (let i = 0; i < 4; i++) {
    const blob = mesh(new T.IcosahedronGeometry((1.1 + r() * 0.5) * scale, 0), leaf);
    blob.position.set((r() - 0.5) * 1.6 * scale, (2.6 + r() * 1.0) * scale, (r() - 0.5) * 1.6 * scale);
    blob.rotation.set(r() * 3, r() * 3, r() * 3);
    g.add(blob);
  }
  if (fruit) {
    const fr = new T.InstancedMesh(new T.IcosahedronGeometry(0.15 * scale, 0).scale(1, 1.25, 1), M(C.mango, { emissive: 0x6B3A00, emissiveIntensity: 0.15 }), 14);
    const m4 = new T.Matrix4(), q = new T.Quaternion(), s = new T.Vector3(1, 1, 1);
    for (let i = 0; i < 14; i++) {
      const a = r() * Math.PI * 2, h = (2.0 + r() * 1.4) * scale, rad = (1.1 + r() * 0.5) * scale;
      m4.compose(new T.Vector3(Math.cos(a) * rad, h, Math.sin(a) * rad), q, s);
      fr.setMatrixAt(i, m4);
    }
    fr.castShadow = true;
    g.add(fr);
    g.userData.fruit = fr;
  }
  return g;
}

function registerPlace(place, group, labelAt) {
  group.userData.pick = "place:" + place;
  gl.places[place] = { group, labelAt, label: null, labelKey: "" };
  gl.scene.add(group);
}

function buildGrove() {
  const g = new T.Group(), r = rng(21);
  g.position.set(-20, 0, -10);
  const spots = [[0, 0], [3.6, -2.6], [-3.4, -2.2], [2.8, 2.8], [-2.8, 3.0], [0.2, -5.4], [5.6, 0.6], [-5.8, 0.2]];
  g.userData.fruits = [];
  for (const [x, z] of spots) {
    const t = tree(r, true, 0.9 + r() * 0.25);
    t.position.set(x, 0, z);
    t.rotation.y = r() * 6;
    g.add(t);
    g.userData.fruits.push(t.userData.fruit);
    g.add(placed(blobShadow(4.2, 4.2, 0.3), x, 0.03, z));
  }
  const crate = mesh(new T.BoxGeometry(1, 0.6, 0.8), M(C.wood));
  crate.position.set(1.8, 0.3, 4.6);
  g.add(crate);
  registerPlace("grove", g, new T.Vector3(0, 7.2, 0));
}

function buildGarden() {
  const g = new T.Group(), r = rng(31);
  g.position.set(21, 0, -11);
  const bed = mesh(new T.BoxGeometry(9, 0.25, 7), M(0x9B6B43));
  bed.position.y = 0.12;
  g.add(bed);
  const bushGeo = new T.IcosahedronGeometry(0.5, 0);
  const bushes = new T.InstancedMesh(bushGeo, M(0x5E8C3A), 30);
  const pods = new T.InstancedMesh(new T.ConeGeometry(0.06, 0.3, 5), M(C.chilli, { emissive: 0x5A0000, emissiveIntensity: 0.2 }), 30 * 5);
  const m4 = new T.Matrix4(), q = new T.Quaternion(), s = new T.Vector3(), e = new T.Euler();
  let k = 0, pk = 0;
  for (let row = 0; row < 5; row++) {
    for (let col = 0; col < 6; col++) {
      const p = new T.Vector3(-3.5 + col * 1.4, 0.6, -2.6 + row * 1.3);
      s.setScalar(0.85 + r() * 0.35);
      m4.compose(p, q.setFromEuler(e.set(r(), r() * 3, r())), s);
      bushes.setMatrixAt(k++, m4);
      for (let j = 0; j < 5; j++) {
        const a = r() * Math.PI * 2;
        m4.compose(new T.Vector3(p.x + Math.cos(a) * 0.42, 0.5 + r() * 0.35, p.z + Math.sin(a) * 0.42), q.setFromEuler(e.set(Math.PI, 0, r() - 0.5)), s.setScalar(1));
        pods.setMatrixAt(pk++, m4);
      }
    }
  }
  bushes.castShadow = true; pods.castShadow = true;
  g.add(bushes, pods);
  // A low bamboo fence.
  const fence = M(0xC9A86A);
  for (let i = 0; i <= 12; i++) {
    for (const z of [-3.7, 3.7]) {
      const post = mesh(new T.CylinderGeometry(0.06, 0.06, 0.9, 5), fence);
      post.position.set(-4.6 + i * 0.77, 0.45, z);
      g.add(post);
    }
  }
  for (const z of [-3.7, 3.7]) {
    const rail = mesh(new T.BoxGeometry(9.3, 0.07, 0.07), fence);
    rail.position.set(0, 0.7, z);
    g.add(rail);
  }
  g.userData.bushes = bushes;
  g.userData.pods = pods;
  registerPlace("garden", g, new T.Vector3(0, 4.2, 0));
}

function buildField() {
  const g = new T.Group(), r = rng(41);
  g.position.set(-19, 0, -22);
  const paddies = [];
  const blade = new T.ConeGeometry(0.08, 0.6, 4);
  for (let i = 0; i < 3; i++) {
    for (let j = 0; j < 2; j++) {
      const w = mesh(new T.BoxGeometry(6, 0.3, 4.2), M(0x8F6A42));
      w.position.set(-6.4 + i * 6.4, 0.15 + j * 0.2, -2.3 + j * 4.6);
      g.add(w);
      const water = mesh(new T.BoxGeometry(5.4, 0.05, 3.6), new T.MeshStandardMaterial({ color: 0x86A88E, roughness: 0.2, metalness: 0.05 }), false, true);
      water.position.set(w.position.x, w.position.y + 0.16, w.position.z);
      g.add(water);
      const rice = new T.InstancedMesh(blade, M(0x7DB04B), 70);
      const m4 = new T.Matrix4(), q = new T.Quaternion(), s = new T.Vector3(1, 1, 1);
      let n = 0;
      for (let a = 0; a < 10; a++) for (let b = 0; b < 7; b++) {
        m4.compose(new T.Vector3(w.position.x - 2.4 + a * 0.53 + r() * 0.1, w.position.y + 0.45, w.position.z - 1.5 + b * 0.5 + r() * 0.1), q, s.set(1, 0.8 + r() * 0.5, 1));
        rice.setMatrixAt(n++, m4);
      }
      rice.castShadow = true;
      g.add(rice);
      paddies.push(rice);
    }
  }
  g.userData.paddies = paddies;
  registerPlace("field", g, new T.Vector3(0, 4.0, 0));
}

function buildRiver() {
  const g = new T.Group();
  const geo = new T.PlaneGeometry(300, 12, 120, 6).rotateX(-Math.PI / 2);
  const water = new T.Mesh(geo, new T.MeshStandardMaterial({ color: C.water, roughness: 0.12, metalness: 0.15, transparent: true, opacity: 0.92, flatShading: true }));
  water.position.set(0, 0.12, -34);
  water.receiveShadow = true;
  g.add(water);
  const bankMat = M(0xC8A779);
  for (const z of [-27.6, -40.4]) {
    const bank = mesh(new T.BoxGeometry(300, 0.18, 1.4), bankMat, false, true);
    bank.position.set(0, 0.09, z);
    g.add(bank);
  }
  gl.river = { water, base: geo.attributes.position.array.slice() };
  // The ghat: stone steps down to the water, and a small shrine.
  const ghat = new T.Group();
  ghat.position.set(8, 0, -27);
  const stone = M(0xE5D3B3);
  for (let i = 0; i < 4; i++) {
    const step = mesh(new T.BoxGeometry(7 - i * 0.4, 0.3, 1.0), stone);
    step.position.set(0, 0.6 - i * 0.18, -0.2 - i * 0.9);
    ghat.add(step);
  }
  const plat = mesh(new T.BoxGeometry(7.6, 0.8, 2.6), stone);
  plat.position.set(0, 0.4, 1.6);
  const shrineBase = mesh(new T.BoxGeometry(1.6, 1.2, 1.6), M(0xF2DDBD));
  shrineBase.position.set(-2.4, 1.4, 1.8);
  const shrineTop = mesh(new T.ConeGeometry(1.0, 1.8, 4), M(C.terracotta));
  shrineTop.position.set(-2.4, 2.9, 1.8);
  shrineTop.rotation.y = Math.PI / 4;
  const flag = mesh(new T.PlaneGeometry(0.6, 0.35), M(0xF97316, { side: T.DoubleSide }));
  flag.position.set(-2.1, 4.0, 1.8);
  ghat.add(plat, shrineBase, shrineTop, flag);
  // A boat at the water's edge.
  const hull = mesh(new T.CylinderGeometry(0.6, 0.6, 4.2, 6, 1, false, 0, Math.PI).rotateZ(Math.PI / 2).rotateX(Math.PI), M(0x7C4A23));
  hull.scale.set(1, 0.6, 1);
  hull.position.set(4.2, 0.35, -4.6);
  const thatch = mesh(new T.CylinderGeometry(0.62, 0.62, 1.6, 8, 1, true, 0, Math.PI).rotateZ(Math.PI / 2), M(0xC9A86A, { side: T.DoubleSide }));
  thatch.position.set(4.2, 0.55, -4.6);
  ghat.add(hull, thatch);
  g.add(ghat);
  gl.scene.add(g);
  gl.boat = hull;
  registerPlace("river", ghat, new T.Vector3(0, 5.0, 0));
}

function buildRoad() {
  const pts = [new T.Vector3(11, 0, 5), new T.Vector3(19, 0, 2), new T.Vector3(28, 0, -5), new T.Vector3(37, 0, -16), new T.Vector3(55, 0, -22), new T.Vector3(95, 0, -22)];
  const curve = new T.CatmullRomCurve3(pts);
  const ribbon = (width, lift, segs = 120) => {
    const pos = [], idx = [];
    for (let i = 0; i <= segs; i++) {
      const t = i / segs, p = curve.getPoint(t), tg = curve.getTangent(t);
      const n = new T.Vector3(-tg.z, 0, tg.x).normalize().multiplyScalar(width / 2);
      for (const side of [1, -1]) {
        const x = p.x + n.x * side, z = p.z + n.z * side;
        pos.push(x, groundHeight(x, z) + lift, z);
      }
      if (i < segs) { const a = i * 2; idx.push(a, a + 2, a + 1, a + 1, a + 2, a + 3); }
    }
    const g = new T.BufferGeometry();
    g.setAttribute("position", new T.Float32BufferAttribute(pos, 3));
    g.setIndex(idx);
    g.computeVertexNormals();
    return g;
  };
  const road = mesh(ribbon(4.2, 0.06), M(0xC79A64), false, true);
  const ruts = mesh(ribbon(2.4, 0.075), M(0xB4875A), false, true);
  gl.scene.add(road, ruts);
  const flood = new T.Mesh(ribbon(7.5, 0.22), new T.MeshStandardMaterial({ color: 0x4F86C6, roughness: 0.08, metalness: 0.2, transparent: true, opacity: 0.85 }));
  flood.visible = false;
  gl.scene.add(flood);
  gl.road = { curve, flood };
  // Signpost.
  const sign = new T.Group();
  const post = mesh(new T.CylinderGeometry(0.09, 0.09, 2.6, 6), M(C.darkwood));
  post.position.y = 1.3;
  sign.add(post);
  sign.position.set(18.5, 0, 4.6);
  gl.scene.add(sign);
  gl.road.sign = sign;
  gl.road.labelAt = new T.Vector3(13.5, 5.6, 7.5);
  gl.road.label = null;
  gl.road.labelKey = "";
  // The city cart: a bullock cart on its way, on days the road is open.
  const cart = new T.Group();
  const bed = mesh(new T.BoxGeometry(1.6, 0.25, 2.6), M(C.wood));
  bed.position.y = 1.05;
  const load = mesh(new T.BoxGeometry(1.3, 0.7, 2.0), M(0xE8D7B0));
  load.position.y = 1.5;
  const canopy = mesh(new T.CylinderGeometry(0.95, 0.95, 2.4, 10, 1, true, 0, Math.PI).rotateX(Math.PI / 2).rotateY(Math.PI / 2), M(0xB4532A, { side: T.DoubleSide }));
  canopy.rotation.set(Math.PI / 2, 0, Math.PI / 2);
  canopy.position.y = 1.7;
  for (const x of [-0.95, 0.95]) {
    const wheel = mesh(new T.TorusGeometry(0.7, 0.1, 5, 12).rotateY(Math.PI / 2), M(C.darkwood));
    wheel.position.set(x, 0.75, -0.2);
    cart.add(wheel);
  }
  const yoke = mesh(new T.BoxGeometry(0.15, 0.15, 2.2), M(C.darkwood));
  yoke.position.set(0, 0.95, 2.2);
  const ox = new T.Group();
  const oxBody = mesh(new T.BoxGeometry(0.9, 0.9, 1.9), M(0xF3EEE4));
  oxBody.position.y = 1.15;
  const oxHead = mesh(new T.BoxGeometry(0.55, 0.55, 0.6), M(0xF3EEE4));
  oxHead.position.set(0, 1.4, 1.15);
  const hump = mesh(new T.IcosahedronGeometry(0.3, 0), M(0xEDE6D8));
  hump.position.set(0, 1.7, 0.6);
  const hornL = mesh(new T.ConeGeometry(0.06, 0.4, 4), M(0x9F1239));
  hornL.position.set(-0.2, 1.8, 1.15);
  const hornR = hornL.clone(); hornR.position.x = 0.2;
  for (const [x, z] of [[-0.3, -0.7], [0.3, -0.7], [-0.3, 0.7], [0.3, 0.7]]) {
    const leg = mesh(new T.BoxGeometry(0.2, 0.75, 0.2), M(0xE7E1D5));
    leg.position.set(x, 0.38, z);
    ox.add(leg);
  }
  ox.add(oxBody, oxHead, hump, hornL, hornR);
  ox.position.set(0, 0, 3.6);
  cart.add(bed, load, canopy, yoke, ox);
  cart.add(placed(blobShadow(3, 7, 0.3), 0, 0.03, 1.4));
  const p = curve.getPoint(0.2), tg = curve.getTangent(0.2);
  cart.position.set(p.x, groundHeight(p.x, p.z) + 0.06, p.z);
  cart.rotation.y = Math.atan2(tg.x, tg.z);
  gl.scene.add(cart);
  gl.road.cart = cart;
}

function setRoadLabel(text, sub) {
  const k = text + "|" + sub;
  if (gl.road.labelKey === k) return;
  gl.road.labelKey = k;
  if (gl.road.label) { gl.scene.remove(gl.road.label); gl.road.label.material.map.dispose(); gl.road.label.material.dispose(); }
  gl.road.label = placeLabel(text, sub);
  gl.road.label.position.copy(gl.road.labelAt);
  gl.road.label.scale.multiplyScalar(0.9);
  gl.scene.add(gl.road.label);
}

function buildHuts() {
  const r = rng(51);
  const spots = [[-3, -17], [8, -17], [-33, -12], [33, -2], [-32, 6], [35, 12], [-24, 18], [24, 14], [-36, -26]];
  for (const [x, z] of spots) {
    const g = new T.Group();
    const wall = mesh(new T.CylinderGeometry(1.6, 1.7, 1.9, 8), M(r() > 0.5 ? 0xE9CFA3 : 0xDDBB8A));
    wall.position.y = 0.95;
    const roof = mesh(new T.ConeGeometry(2.3, 1.9, 8), M(0xB98B4E));
    roof.position.y = 2.8;
    const door = mesh(new T.BoxGeometry(0.7, 1.2, 0.1), M(C.umber));
    door.position.set(0, 0.6, 1.62);
    const band = mesh(new T.CylinderGeometry(1.72, 1.72, 0.18, 8), M(C.terracotta));
    band.position.y = 0.25;
    g.add(wall, roof, door, band, blobShadow(5, 5, 0.3));
    g.position.set(x, 0, z);
    g.lookAt(0, 0, -1);
    g.rotation.y += (r() - 0.5) * 0.4;
    const s = 0.85 + r() * 0.35;
    g.scale.setScalar(s);
    gl.scene.add(g);
  }
  // A peepal tree beside the square, and a few more trees at the edges.
  const rr2 = rng(61);
  const big = tree(rr2, false, 1.7);
  big.position.set(-24, 0, 8);
  const chab = mesh(new T.CylinderGeometry(2.4, 2.6, 0.6, 12), M(0xE5D3B3));
  chab.position.set(-24, 0.3, 8);
  gl.scene.add(big, chab);
  buildProps();
  for (const [x, z, s] of [[13, -16, 1.2], [-40, -2, 1.2], [38, 2, 1.4], [-8, -30, 1.1], [28, -30, 1.2], [-40, 24, 1.3], [42, -20, 1.2], [-26, -32, 1.2], [0, -44, 1.5], [-52, -6, 1.6], [54, -8, 1.5]]) {
    const t = tree(rr2, false, s);
    t.position.set(x, groundHeight(x, z), z);
    gl.scene.add(t);
  }
}

// A well, clay pots and a cow resting under the peepal tree.
function buildProps() {
  const well = new T.Group();
  const wall = mesh(new T.CylinderGeometry(1.2, 1.3, 0.9, 12, 1, true), M(0xD9C3A0, { side: T.DoubleSide }));
  wall.position.y = 0.45;
  const rim = mesh(new T.TorusGeometry(1.25, 0.12, 5, 14).rotateX(Math.PI / 2), M(0xE8D7B8));
  rim.position.y = 0.9;
  const water = mesh(new T.CircleGeometry(1.15, 12).rotateX(-Math.PI / 2), M(0x3F6E9E, { roughness: 0.2 }), false, false);
  water.position.y = 0.5;
  for (const x of [-1.1, 1.1]) {
    const post = mesh(new T.BoxGeometry(0.16, 1.9, 0.16), M(C.darkwood));
    post.position.set(x, 1.5, 0);
    well.add(post);
  }
  const beam = mesh(new T.CylinderGeometry(0.07, 0.07, 2.4, 6).rotateZ(Math.PI / 2), M(C.darkwood));
  beam.position.y = 2.35;
  const pail = mesh(new T.CylinderGeometry(0.2, 0.16, 0.3, 8), M(C.brass, { metalness: 0.5, roughness: 0.4 }));
  pail.position.set(0.3, 1.6, 0);
  well.add(wall, rim, water, beam, pail, blobShadow(4, 4, 0.3));
  well.position.set(-11, 0.12, 8);
  gl.scene.add(well);
  const pot = M(C.terracotta);
  const r = rng(91);
  for (const [x, z] of [[9.5, 8.5], [10.3, 9.1], [9.2, 9.6], [10.1, 10.2], [-13.5, 2.2], [-12.9, 1.6]]) {
    const m = mesh(new T.SphereGeometry(0.42, 8, 6), pot);
    m.scale.set(1, 0.9 + r() * 0.2, 1);
    m.position.set(x, 0.5, z);
    const neck = mesh(new T.CylinderGeometry(0.18, 0.24, 0.22, 8), pot);
    neck.position.set(x, 0.98, z);
    gl.scene.add(m, neck);
  }
  const cow = new T.Group();
  const white = M(0xF4EFE6);
  const body = mesh(new T.BoxGeometry(0.95, 0.8, 1.9), white);
  body.position.y = 0.55;
  const head = mesh(new T.BoxGeometry(0.5, 0.5, 0.6), white);
  head.position.set(0, 0.75, 1.15);
  const hump = mesh(new T.IcosahedronGeometry(0.3, 0), white);
  hump.position.set(0, 1.02, 0.6);
  for (const x of [-0.18, 0.18]) {
    const horn = mesh(new T.ConeGeometry(0.06, 0.35, 4), M(0xB45309));
    horn.position.set(x, 1.1, 1.15);
    cow.add(horn);
  }
  cow.add(body, head, hump, blobShadow(2.4, 3.2, 0.35));
  cow.position.set(-19.5, 0, 12);
  cow.rotation.y = 0.9;
  gl.scene.add(cow);
}

// The player's lamps: one diya lit for every shop they rescued (as on the leaderboard).
function buildLampStand() {
  const g = new T.Group();
  g.position.set(0, 0.17, 1.2);
  const base = mesh(new T.CylinderGeometry(0.75, 0.95, 0.3, 10), M(C.brass, { metalness: 0.55, roughness: 0.35 }));
  base.position.y = 0.15;
  const stem = mesh(new T.CylinderGeometry(0.1, 0.16, 1.7, 8), base.material);
  stem.position.y = 1.1;
  const knot = mesh(new T.SphereGeometry(0.22, 8, 6), base.material);
  knot.position.y = 1.2;
  const tray = mesh(new T.CylinderGeometry(0.82, 0.7, 0.1, 12), base.material);
  tray.position.y = 1.98;
  const finial = mesh(new T.ConeGeometry(0.14, 0.5, 8), base.material);
  finial.position.y = 2.28;
  g.add(base, stem, knot, tray, finial);
  const diyas = [], flames = [], glows = [];
  for (let i = 0; i < 5; i++) {
    const a = (i / 5) * Math.PI * 2;
    const d = mesh(new T.SphereGeometry(0.18, 8, 4, 0, Math.PI * 2, Math.PI / 2, Math.PI / 2), M(C.terracotta));
    d.position.set(Math.cos(a) * 0.68, 2.12, Math.sin(a) * 0.68);
    d.rotation.x = Math.PI;
    const f = new T.Mesh(new T.ConeGeometry(0.07, 0.22, 6), new T.MeshBasicMaterial({ color: C.flame, toneMapped: false }));
    f.position.set(d.position.x, 2.24, d.position.z);
    const gw = glowSprite(0xFFB347, 1.4);
    gw.position.copy(f.position);
    g.add(d, f, gw);
    diyas.push(d); flames.push(f); glows.push(gw);
  }
  const light = new T.PointLight(0xFFB347, 0, 9, 1.5);
  light.position.set(0, 2.6, 0);
  g.add(light);
  g.userData.pick = "lamp";
  g.visible = false;
  gl.scene.add(g);
  gl.lamp = { group: g, flames, glows, light, lit: 0 };
}

function buildRain() {
  const n = 2600, pos = new Float32Array(n * 6), r = rng(71);
  for (let i = 0; i < n; i++) {
    const x = (r() - 0.5) * 90, z = (r() - 0.5) * 80 - 4, y = r() * 40;
    pos.set([x, y, z, x + 0.12, y - 0.9, z + 0.05], i * 6);
  }
  const g = new T.BufferGeometry();
  g.setAttribute("position", new T.BufferAttribute(pos, 3));
  const rain = new T.LineSegments(g, new T.LineBasicMaterial({ color: 0xC9DAF2, transparent: true, opacity: 0.55, depthWrite: false }));
  rain.visible = false;
  rain.frustumCulled = false;
  gl.scene.add(rain);
  gl.rain = { mesh: rain, n, speed: 34 };
}

function buildClouds() {
  const r = rng(81);
  gl.clouds = [];
  const mat = new T.MeshStandardMaterial({ color: 0xFFFFFF, roughness: 1, flatShading: true, transparent: true, opacity: 0.95 });
  for (let i = 0; i < 9; i++) {
    const g = new T.Group();
    for (let j = 0; j < 4; j++) {
      const b = mesh(new T.IcosahedronGeometry(2.4 + r() * 2, 0), mat, false, false);
      b.position.set(j * 3 - 4.5, r() * 1.2, (r() - 0.5) * 2.5);
      b.scale.y = 0.6;
      g.add(b);
    }
    g.position.set((r() - 0.5) * 220, 34 + r() * 14, -60 - r() * 90);
    g.userData.speed = 0.6 + r() * 0.8;
    gl.scene.add(g);
    gl.clouds.push(g);
  }
  gl.cloudMat = mat;
}

// ---------------------------------------------------------------- bind state to the scene

let SHUT_GREY = null;
function applyState(s) {
  if (!SHUT_GREY && T) SHUT_GREY = new T.Color(0x9CA3AF);
  if (!gl) return;
  gl.dayFrac = s.clock.day_fraction;
  gl.dayRate = s.clock.awake && s.clock.day_seconds > 0 && !s.fast_forward.running ? 1 / s.clock.day_seconds : 0;
  updateHUD(s);
  const monsoon = s.season === "barsaat";
  paintGround(s.season);
  gl.weather = { monsoon, flood: s.flood, season: s.season };
  gl.rain.mesh.visible = monsoon;
  gl.rain.mesh.geometry.setDrawRange(0, (s.flood ? gl.rain.n : Math.round(gl.rain.n * 0.4)) * 2);
  gl.rain.mesh.material.opacity = s.flood ? 0.62 : 0.42;
  gl.cloudMat.color.set(monsoon ? (s.flood ? 0x8C97A8 : 0xB9C2CF) : 0xFFFFFF);
  // Road, cart and river.
  gl.road.flood.visible = s.flood;
  gl.road.cart.visible = !s.flood;
  gl.river.flood = s.flood;
  gl.river.water.scale.z = s.flood ? 1.45 : monsoon ? 1.15 : 1;
  setRoadLabel(s.flood ? "Road flooded" : "Road to the city", s.flood ? "the city cart cannot come today" : "open, the city cart comes");
  // Gathering places.
  for (const p of s.places) {
    const pl = gl.places[p.place];
    if (!pl) continue;
    setPlaceLabel(pl, p.name, p.barren ? "nothing this season" : p.yield);
    if (p.place === "grove") pl.group.userData.fruits.forEach((f) => { f.visible = !p.barren; });
    if (p.place === "garden") {
      pl.group.userData.pods.visible = !p.barren;
      pl.group.userData.bushes.material.color.set(p.barren ? 0x8A8A4A : 0x5E8C3A);
    }
  }
  const field = gl.places.field;
  if (field) field.group.userData.paddies.forEach((m) => m.material.color.set(s.season === "tyohar" ? 0xD9B54A : s.season === "barsaat" ? 0x5FA83E : 0x8DB85A));
  // Stalls.
  for (const shop of s.shops) {
    const st = gl.stalls[shop.key];
    if (!st) continue;
    st.shop = shop;
    const bk = JSON.stringify([shop.name, shop.temperament, shop.mood, shop.purse, shop.open, shop.reopened, shop.shelves]);
    if (bk !== st.boardKey) { st.boardKey = bk; drawBoard(st, shop); }
    shop.shelves.slice(0, 2).forEach((sh, i) => fillBasket(st.goods[i], sh.token, shop.open ? sh.stock : 0));
    st.shutterGoal = shop.open ? 0 : 1;
    st.keeper.visible = true;
    st.garland.visible = shop.open && shop.reopened;
    st.awnMat.color.setHex(0xFFFFFF).lerp(SHUT_GREY, shop.open ? 0 : 0.55);
    st.valMat.color.copy(st.awnMat.color);
  }
  // The player's lamps.
  const lamp = gl.lamp;
  lamp.group.visible = !!s.player;
  lamp.lit = s.player ? s.player.lamps : 0;
  lamp.flames.forEach((f, i) => { f.visible = i < lamp.lit; });
}

// ---------------------------------------------------------------- light by time of day

const KEYS = [
  // hour, sky top, horizon, sun colour, sun strength, hemi strength, night
  [6, 0x8FA9DA, 0xF7C9A0, 0xFFB27A, 1.3, 0.75, 0.2],
  [9, 0x76A8EC, 0xF4ECDD, 0xFFF0D8, 2.5, 1.05, 0],
  [13, 0x6FA2EA, 0xF2EEE4, 0xFFF6E8, 2.9, 1.15, 0],
  [16.5, 0x7EA2DE, 0xF8DDB6, 0xFFD9A8, 2.4, 1.0, 0],
  [18.3, 0x5D6FB2, 0xF6A774, 0xFF9A5C, 1.4, 0.75, 0.35],
  [19.6, 0x2A3770, 0xC77A6A, 0xD9785A, 0.5, 0.5, 0.75],
  [21, 0x101C44, 0x35416F, 0x9FB4FF, 0.55, 0.42, 1],
  [24, 0x0B1533, 0x2A3460, 0x9FB4FF, 0.5, 0.4, 1],
];

// Scratch colours, so lighting allocates nothing per frame.
let LC = null;
function lightAt(hour) {
  if (!LC) {
    LC = { top: new T.Color(), horizon: new T.Color(), sun: new T.Color(), tmp: new T.Color(),
      grey: new T.Color(0x9AA6B8), mist: new T.Color(0xC9D0D8), white: new T.Color(0xFFFFFF), out: {} };
  }
  let i = 0;
  while (i < KEYS.length - 2 && hour > KEYS[i + 1][0]) i++;
  const a = KEYS[i], b = KEYS[i + 1];
  const t = Math.min(1, Math.max(0, (hour - a[0]) / (b[0] - a[0])));
  const col = (c, x, y) => c.setHex(x).lerp(LC.tmp.setHex(y), t);
  const L = LC.out;
  L.top = col(LC.top, a[1], b[1]); L.horizon = col(LC.horizon, a[2], b[2]); L.sun = col(LC.sun, a[3], b[3]);
  L.sunI = a[4] + (b[4] - a[4]) * t; L.hemiI = a[5] + (b[5] - a[5]) * t; L.night = a[6] + (b[6] - a[6]) * t;
  return L;
}

function applyLight(now) {
  const hour = 6 + gl.dayFrac * 18;
  const L = lightAt(hour);
  const w = gl.weather || {};
  const overcast = w.flood ? 0.62 : w.monsoon ? 0.4 : 0;
  L.top.lerp(LC.grey, overcast * (1 - L.night * 0.6));
  L.horizon.lerp(LC.mist, overcast * (1 - L.night * 0.7));
  L.sunI *= 1 - overcast * 0.6;
  gl.sky.u.top.value.copy(L.top);
  gl.sky.u.horizon.value.copy(L.horizon);
  gl.sky.u.bottom.value.copy(L.horizon).multiplyScalar(0.9);
  gl.scene.fog.color.copy(L.horizon);
  // The sun crosses from east to west; at night the same light is the moon.
  const night = L.night;
  const dayT = Math.min(1, Math.max(0, (hour - 6) / 13.5));
  const az = -1.2 + dayT * 2.4;
  const el = night > 0.6 ? 0.9 : 0.25 + Math.sin(dayT * Math.PI) * 0.95;
  gl.sun.position.set(Math.sin(az) * 60, Math.sin(el) * 70 + 8, Math.cos(az) * 30 + 20);
  gl.sun.target.position.set(0, 0, -4);
  gl.sun.color.copy(L.sun);
  gl.sun.intensity = L.sunI;
  gl.hemi.intensity = L.hemiI * (1 - overcast * 0.15);
  gl.hemi.color.copy(L.top).lerp(LC.white, 0.55);
  gl.hemi.groundColor.setHex(night > 0.5 ? 0x3A3350 : 0xC89A63);
  gl.stars.material.opacity = Math.max(0, night - 0.4) * (1 - overcast) * 1.5;
  gl.moon.material.opacity = Math.max(0, night - 0.5) * 2 * (1 - overcast * 0.8);
  gl.moon.position.set(gl.camera.position.x * 0.2 - 90, 150, -260);
  // Lanterns glow from the evening on; diyas and stall lamps too.
  const glow = Math.min(1, Math.max(0, (hour - 17.2) / 2.2));
  const flicker = (k) => 0.92 + Math.sin(now * 0.008 + k * 1.7) * 0.05 + Math.sin(now * 0.021 + k) * 0.03;
  gl.lanterns.forEach((m, k) => { m.emissiveIntensity = 0.15 + glow * 1.6 * flicker(k); });
  gl.glows.forEach((g, k) => { g.material.opacity = glow * 0.55 * flicker(k); });
  gl.squareLights.forEach((l) => { l.intensity = glow * 34; });
  for (const key in gl.stalls) {
    const st = gl.stalls[key];
    st.lamp.intensity = glow * (st.shop && st.shop.open ? 14 : 2);
    st.board.material.color.setScalar(1 - night * 0.18);
  }
  const lamp = gl.lamp;
  const lampGlow = 0.35 + glow * 0.65;
  lamp.glows.forEach((g, i) => { g.material.opacity = i < lamp.lit ? lampGlow * 0.8 * flicker(i + 9) : 0; });
  lamp.flames.forEach((f, i) => { f.scale.y = 0.85 + flicker(i + 3) * 0.3; });
  lamp.light.intensity = lamp.lit ? lampGlow * 10 : 0;
  gl.renderer.toneMappingExposure = 1.0 + night * 0.15;
}

// ---------------------------------------------------------------- camera and controls

function setupControls() {
  const c = ui.canvas;
  const pointers = new Map();
  let downAt = null, pinch = 0;
  c.addEventListener("pointerdown", (e) => {
    c.setPointerCapture(e.pointerId);
    pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
    downAt = { x: e.clientX, y: e.clientY, t: performance.now(), moved: 0 };
    if (pointers.size === 2) pinch = pinchDist(pointers);
    gl.goal = null;
    gl.intro = 1;
  });
  c.addEventListener("pointermove", (e) => {
    const p = pointers.get(e.pointerId);
    if (!p) { hoverAt(e.clientX, e.clientY); return; }
    const dx = e.clientX - p.x, dy = e.clientY - p.y;
    p.x = e.clientX; p.y = e.clientY;
    if (downAt) downAt.moved += Math.abs(dx) + Math.abs(dy);
    if (pointers.size === 1) {
      gl.view.az -= dx * 0.0055;
      gl.view.pol -= dy * 0.004;
    } else if (pointers.size === 2) {
      const d = pinchDist(pointers);
      if (pinch > 0) gl.view.r *= pinch / d;
      pinch = d;
    }
    clampView();
  });
  const up = (e) => {
    const wasClick = downAt && downAt.moved < 8 && pointers.size === 1 && performance.now() - downAt.t < 600;
    pointers.delete(e.pointerId);
    if (pointers.size < 2) pinch = 0;
    if (wasClick) clickAt(e.clientX, e.clientY);
    if (!pointers.size) downAt = null;
  };
  c.addEventListener("pointerup", up);
  c.addEventListener("pointercancel", (e) => { pointers.delete(e.pointerId); downAt = null; });
  c.addEventListener("pointerleave", () => { if (!pointers.size) setHover(null); });
  c.addEventListener("wheel", (e) => {
    e.preventDefault();
    gl.goal = null;
    gl.view.r *= Math.exp(e.deltaY * 0.0012);
    clampView();
  }, { passive: false });
}

function pinchDist(map) {
  const [a, b] = [...map.values()];
  return Math.hypot(a.x - b.x, a.y - b.y) || 1;
}

function clampView() {
  const v = gl.view;
  v.az = Math.max(-1.25, Math.min(1.25, v.az));
  v.pol = Math.max(0.55, Math.min(1.32, v.pol));
  v.r = Math.max(12, Math.min(gl.home ? gl.home.r * 1.6 : 90, v.r));
}

function homeView() {
  const w = ui.stage.clientWidth || innerWidth, h = ui.stage.clientHeight || innerHeight;
  const aspect = w / h;
  const half = aspect < 0.8 ? 8.5 : 24;
  const vfov = (gl.camera.fov * Math.PI) / 360;
  const hfov = Math.atan(Math.tan(vfov) * aspect);
  const r = Math.max(30, Math.min(96, half / Math.tan(hfov)));
  return { az: 0, pol: 1.06, r, tx: 0, ty: aspect < 0.8 ? 2.6 : 1.2, tz: -7.5 };
}

function flyHome() { gl.goal = Object.assign({}, gl.home); }

function flyTo(key) {
  if (key.startsWith("place:")) {
    const pl = gl.places[key.slice(6)];
    if (!pl) return;
    const p = new T.Vector3();
    pl.group.getWorldPosition(p);
    const az = Math.max(-1.2, Math.min(1.2, Math.atan2(p.x, 30)));
    gl.goal = { az, pol: 1.0, r: gl.phone ? 44 : 36, tx: p.x, ty: 1.5, tz: p.z };
    return;
  }
  const st = gl.stalls[key];
  if (!st) return;
  const { x, z, a } = st.spot;
  gl.goal = { az: a * 0.45, pol: 1.22, r: gl.phone ? 26 : 21, tx: x, ty: 2.8, tz: z + 1.2 };
}

function resize() {
  if (!gl || !isOpen) return;
  const w = ui.stage.clientWidth || innerWidth, h = ui.stage.clientHeight || innerHeight;
  gl.renderer.setSize(w, h, false);
  gl.camera.aspect = w / h;
  gl.camera.updateProjectionMatrix();
  const home = homeView();
  const wasHome = !gl.home || (!gl.goal && !gl.panelFor && Math.abs(gl.view.r - gl.home.r) < 0.5);
  gl.home = home;
  if (wasHome && !gl.panelFor) {
    Object.assign(gl.view, home);
    if (gl.intro < 1) { gl.view.r = home.r * 1.35; gl.view.pol = 0.7; gl.view.az = -0.55; gl.goal = Object.assign({}, home); }
  }
}

// Shift the picture away from the panel so the chosen stall stays in view.
function viewOffset() {
  const w = ui.stage.clientWidth, h = ui.stage.clientHeight;
  let ox = 0, oy = 0;
  if (gl.panelFor && !ui.panel.hidden) {
    const r = ui.panel.getBoundingClientRect();
    if (r.width < w * 0.9) ox = r.width / 2;
    else oy = Math.min(r.height, h * 0.6) / 2;
  }
  gl.offset = gl.offset || { x: 0, y: 0 };
  gl.offset.x += (ox - gl.offset.x) * 0.12;
  gl.offset.y += (oy - gl.offset.y) * 0.12;
  if (Math.abs(gl.offset.x) < 0.5 && Math.abs(gl.offset.y) < 0.5) gl.camera.clearViewOffset();
  else gl.camera.setViewOffset(w, h, gl.offset.x, gl.offset.y, w, h);
}

function placeCamera(dt) {
  const v = gl.view;
  if (gl.goal) {
    const k = 1 - Math.pow(0.0015, dt);
    let done = true;
    for (const f of ["az", "pol", "r", "tx", "ty", "tz"]) {
      v[f] += (gl.goal[f] - v[f]) * k;
      if (Math.abs(gl.goal[f] - v[f]) > 0.002) done = false;
    }
    if (done) gl.goal = null;
  }
  const sp = Math.sin(v.pol);
  gl.camera.position.set(v.tx + v.r * sp * Math.sin(v.az), v.ty + v.r * Math.cos(v.pol), v.tz + v.r * sp * Math.cos(v.az));
  gl.camera.lookAt(v.tx, v.ty, v.tz);
  viewOffset();
  gl.sky.mesh.position.copy(gl.camera.position);
}

// ---------------------------------------------------------------- picking

const ray = { caster: null, v: null };

function pick(x, y) {
  if (!ray.caster) { ray.caster = new T.Raycaster(); ray.v = new T.Vector2(); }
  const r = ui.canvas.getBoundingClientRect();
  ray.v.set(((x - r.left) / r.width) * 2 - 1, -((y - r.top) / r.height) * 2 + 1);
  ray.caster.setFromCamera(ray.v, gl.camera);
  const roots = [...Object.values(gl.stalls).map((s) => s.group), ...Object.values(gl.places).map((p) => p.group), gl.lamp.group];
  const hits = ray.caster.intersectObjects(roots, true);
  for (const h of hits) {
    let o = h.object;
    while (o && !o.userData.pick) o = o.parent;
    if (o && o.visible !== false) return o.userData.pick;
  }
  return null;
}

function clickAt(x, y) {
  const k = pick(x, y);
  if (!k) { if (gl.panelFor) closePanel(); return; }
  if (k === "lamp") {
    showTip(x, y, lampText());
    clearTimeout(gl.tipTimer);
    gl.tipTimer = setTimeout(hideTip, 3500);
    return;
  }
  openPanel(k);
}

function lampText() {
  const p = state && state.player;
  if (!p) return "";
  if (!p.rescues) return "Your lamp. Rescue a closed shop by investing in it, and a diya is lit here for you.";
  return `Your lamps: ${p.rescues} ${p.rescues === 1 ? "shop" : "shops"} rescued.` + (p.rank ? ` You are #${p.rank} of ${p.of} on the leaderboard.` : "");
}

let hoverRaf = 0;
function hoverAt(x, y) {
  if (gl.phone || hoverRaf) return;
  hoverRaf = requestAnimationFrame(() => {
    hoverRaf = 0;
    const k = pick(x, y);
    setHover(k);
    if (k) showTip(x, y, tipText(k)); else hideTip();
  });
}

function tipText(k) {
  if (k === "lamp") return lampText();
  if (k.startsWith("place:")) {
    const p = state.places.find((q) => q.place === k.slice(6));
    return p ? `<b>${escapeHTML(p.name)}</b>${p.barren ? "Nothing this season" : escapeHTML(p.yield)}. Tap to gather.` : "";
  }
  const s = state.shops.find((q) => q.key === k);
  if (!s) return "";
  return `<b>${escapeHTML(s.name)}</b>${s.open ? "Tap to trade" : "Closed. Tap to see how to reopen it"}.`;
}

function setHover(k) {
  if (gl.hover === k) return;
  if (gl.hover && gl.stalls[gl.hover]) gl.stalls[gl.hover].awnMat.emissive.setScalar(0);
  gl.hover = k;
  if (k && gl.stalls[k]) gl.stalls[k].awnMat.emissive.setScalar(0.12);
  ui.canvas.style.cursor = k ? "pointer" : "";
}

function showTip(x, y, html) {
  if (!html) return hideTip();
  const r = ui.root.getBoundingClientRect();
  ui.tip.innerHTML = html;
  ui.tip.hidden = false;
  ui.tip.style.left = Math.min(x - r.left + 14, r.width - 260) + "px";
  ui.tip.style.top = (y - r.top + 16) + "px";
}

function hideTip() { ui.tip.hidden = true; }

// A little burst of the gathered item rising from the place.
function gatherBurst(place) {
  const pl = gl.places[place];
  const p = state && state.places.find((q) => q.place === place);
  if (!pl || !p || p.barren) return;
  const emoji = { mango: "🥭", rice: "🌾", fish: "🐟", chilli: "🌶️" }[p.item] || "✨";
  const tex = canvasTexture(128, 128, (ctx) => {
    ctx.font = '96px "Apple Color Emoji", "Segoe UI Emoji", "Noto Color Emoji", sans-serif';
    ctx.textAlign = "center"; ctx.textBaseline = "middle"; ctx.fillText(emoji, 64, 70);
  });
  for (let i = 0; i < 3; i++) {
    const s = new T.Sprite(new T.SpriteMaterial({ map: tex, transparent: true, depthWrite: false }));
    s.scale.set(1.4, 1.4, 1);
    const at = new T.Vector3();
    pl.group.getWorldPosition(at);
    s.position.set(at.x + (i - 1) * 1.2, 3 + i * 0.3, at.z + 1);
    gl.scene.add(s);
    gl.bursts.push({ s, t: -i * 0.12, last: i === 2 });
  }
}

// ---------------------------------------------------------------- loop

function startLoop() {
  cancelAnimationFrame(frame);
  gl.clock.getDelta();
  const tick = () => {
    if (!isOpen) return;
    frame = requestAnimationFrame(tick);
    if (document.hidden) return;
    const dt = Math.min(0.05, gl.clock.getDelta());
    const now = performance.now();
    if (gl.dayRate) {
      gl.dayFrac = Math.min(1, gl.dayFrac + dt * gl.dayRate);
      const word = clockWord(gl.dayFrac), el = ui.line.querySelector(".v3d-clock");
      if (el && el.textContent !== word) el.textContent = word;
    }
    if (gl.intro < 1) gl.intro = Math.min(1, gl.intro + dt / 2);
    placeCamera(dt);
    animate(dt, now);
    applyLight(now);
    gl.renderer.render(gl.scene, gl.camera);
    adaptQuality(dt);
  };
  tick();
}

function animate(dt, now) {
  // Shutters roll down on a closed shop and up when it reopens.
  for (const key in gl.stalls) {
    const st = gl.stalls[key];
    st.shutter += (st.shutterGoal - st.shutter) * Math.min(1, dt * 3);
    const s = Math.max(0.001, st.shutter);
    st.shutterGroup.scale.y = s;
    st.shutterGroup.position.y = 3.2 * (1 - s);
    st.shutterGroup.visible = st.shutter > 0.01;
    st.sign.visible = st.shutter > 0.9;
    st.keeper.position.y = 0.35 + Math.sin(now * 0.002 + key.length) * 0.03;
    st.keeper.rotation.y = Math.sin(now * 0.0007 + key.length * 2) * 0.25;
  }
  // Rain.
  if (gl.rain.mesh.visible && !reduceMotion) {
    const a = gl.rain.mesh.geometry.attributes.position, arr = a.array, fall = gl.rain.speed * dt * (gl.weather.flood ? 1.25 : 1);
    for (let i = 0; i < arr.length; i += 6) {
      arr[i + 1] -= fall; arr[i + 4] -= fall;
      if (arr[i + 4] < 0) { arr[i + 1] += 40; arr[i + 4] += 40; }
    }
    a.needsUpdate = true;
  }
  // The river ripples.
  const w = gl.river.water.geometry.attributes.position, base = gl.river.base;
  for (let i = 0; i < w.count; i++) {
    const x = base[i * 3], z = base[i * 3 + 2];
    w.array[i * 3 + 1] = Math.sin(x * 0.35 + now * 0.0012) * 0.06 + Math.cos(z * 0.9 + x * 0.1 + now * 0.0017) * 0.05;
  }
  w.needsUpdate = true;
  gl.river.water.geometry.computeVertexNormals();
  gl.river.water.position.y = gl.river.flood ? 0.32 : 0.12;
  if (gl.boat) gl.boat.rotation.z = Math.sin(now * 0.0013) * 0.04;
  for (const c of gl.clouds) {
    c.position.x += c.userData.speed * dt;
    if (c.position.x > 130) c.position.x = -130;
  }
  // Gather bursts.
  gl.bursts = gl.bursts.filter((b) => {
    b.t += dt;
    if (b.t < 0) { b.s.visible = false; return true; }
    b.s.visible = true;
    b.s.position.y += dt * 2.2;
    b.s.material.opacity = Math.max(0, 1 - b.t / 1.2);
    if (b.t > 1.2) { gl.scene.remove(b.s); if (b.last) b.s.material.map.dispose(); b.s.material.dispose(); return false; }
    return true;
  });
}

// Drop the resolution a little on slow machines.
function adaptQuality(dt) {
  gl.frames++;
  gl.slow = gl.slow * 0.95 + dt * 0.05;
  if (gl.frames > 90 && gl.slow > 0.03 && gl.ratio > 1) {
    gl.ratio = Math.max(1, gl.ratio - 0.5);
    gl.renderer.setPixelRatio(gl.ratio);
    resize();
    gl.frames = 0;
  }
}
