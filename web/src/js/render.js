import {
  KEY_TONE,
  NOTE_TONE,
  STATUS_META,
  adaptLogs,
  adaptTraffic,
  esc,
  fmtDur,
  fmtInt,
  providerAgg,
  providerModelRows,
} from "./data.js";
import { api } from "./api.js";
import {
  getLogsPage,
  getLogFilters,
  getMetrics,
  getRecentFailures,
  getState,
  getStatus,
  getUptimeSeconds,
  loadLogsPage,
  moveLeg,
  reloadConfig,
  removeLeg,
  setLogFilter,
  subscribe,
  toggleKey,
  toggleLeg,
  toggleModel,
  toggleProvider,
} from "./state.js";

export const $ = (s) => document.querySelector(s);

export const $$ = (s) => document.querySelectorAll(s);

// Smooth out state-driven re-renders where the browser supports it.
export function withTransition(fn) {
  if (document.startViewTransition)
    document.startViewTransition(fn).ready.catch(() => {});
  else fn();
}

// ----- providers grid --------------------------------------------------------

function keyLine(p) {
  if (!p.keyList?.length) return p.keys;
  const on = p.keyList.filter((k) => k.on).length;
  if (on === 0) return `0/${p.keyList.length} · all disabled`;
  const bad = p.keyList.filter((k) => k.on && k.status !== "healthy");
  return `${on}/${p.keyList.length} · ${bad.length ? bad.length + " " + bad[0].status : "all healthy"}`;
}

export function providerCard(p, agg) {
  const s = STATUS_META[p.status];
  const p50 = agg[p.name];
  return `
    <div data-provider="${esc(p.name)}" title="Click to see TTFT by model" class="cursor-pointer rounded-lg border bg-card p-4 shadow-sm transition-all hover:shadow-md${p.on ? "" : " opacity-60"}">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-2"><span class="dot ${s.dot}"></span><p class="text-sm font-semibold">${esc(p.name)}</p><span class="badge ${s.badge}">${s.label}</span></div>
        <input type="checkbox" class="sw" data-action="provider-toggle" data-name="${esc(p.name)}" ${p.on ? "checked" : ""} />
      </div>
      <dl class="mt-3 flex flex-col gap-1.5 text-xs">
        <div class="flex justify-between"><dt class="text-muted-foreground">Keys</dt><dd class="font-mono">${keyLine(p)}</dd></div>
        <div class="flex justify-between"><dt class="text-muted-foreground">Requests 24h</dt><dd class="font-mono">${p.req}</dd></div>
        <div class="flex justify-between"><dt class="text-muted-foreground">TTFT p50 · weighted</dt><dd class="font-mono">${p50 ? fmtDur(p50) : "—"}</dd></div>
        <div class="flex justify-between"><dt class="text-muted-foreground">Errors 24h</dt><dd class="font-mono">${p.err}</dd></div>
        <div class="flex justify-between"><dt class="text-muted-foreground">Uptime 24h</dt><dd class="font-mono">${p.up}</dd></div>
      </dl>
    </div>`;
}

// ----- models & fallback chains ----------------------------------------------

export function modelBlock(m) {
  const legs = m.legs
    .map(
      (leg, i) => `
      <li class="flex flex-wrap items-center gap-x-3 gap-y-1.5 py-2.5 transition-opacity${leg.on === false ? " opacity-50" : ""}">
        <span class="badge tone-info font-mono">${i + 1}</span>
        <span class="min-w-0 truncate font-mono text-xs">${esc(leg.route)}</span>
        <span class="badge ${NOTE_TONE[leg.tone]}">${esc(leg.note)}</span>
        ${leg.effort ? `<span class="badge tone-info font-mono">effort: ${esc(leg.effort)}</span>` : ""}
        <span class="ml-auto flex items-center gap-1.5">
          <button data-action="move-up" data-model="${esc(m.name)}" data-index="${i}" ${i === 0 ? "disabled" : ""} class="rounded border p-1 text-muted-foreground hover:bg-accent disabled:opacity-30"><i data-lucide="arrow-up" class="size-3"></i></button>
          <button data-action="move-down" data-model="${esc(m.name)}" data-index="${i}" ${i === m.legs.length - 1 ? "disabled" : ""} class="rounded border p-1 text-muted-foreground hover:bg-accent disabled:opacity-30"><i data-lucide="arrow-down" class="size-3"></i></button>
          <input type="checkbox" class="sw" data-action="leg-toggle" data-model="${esc(m.name)}" data-index="${i}" ${leg.on === false ? "" : "checked"} />
        </span>
      </li>`,
    )
    .join("");
  return `
    <div data-model-block="${esc(m.name)}" class="rounded-lg border bg-card shadow-sm transition-opacity${m.on ? "" : " opacity-60"}">
      <div class="flex flex-wrap items-center gap-3 border-b px-5 py-3">
        <span class="font-mono text-sm font-medium">${esc(m.name)}</span>
        <span class="ml-auto flex items-center gap-4 text-xs text-muted-foreground">
          <span>req <span class="font-mono text-foreground">${m.req}</span></span>
          <span>success <span class="font-mono text-foreground">${m.ok}</span></span>
          <span>ttft <span class="font-mono text-foreground">${fmtDur(m.ttft)}</span></span>
          <input type="checkbox" class="sw" data-action="model-toggle" data-model="${esc(m.name)}" ${m.on ? "checked" : ""} />
        </span>
      </div>
      <ul class="divide-y px-5 py-1 text-sm">${legs}
        <li class="py-2.5">
          <button data-action="add-leg" data-model="${esc(m.name)}" class="flex w-full items-center justify-center gap-2 rounded-md border border-dashed py-2 text-xs text-muted-foreground hover:bg-accent"><i data-lucide="plus" class="size-3"></i>Add failover leg</button>
        </li>
      </ul>
    </div>`;
}

export function renderProviders() {
  const agg = providerAgg(getMetrics());
  const providers = getState().providers;
  $("#provider-grid").innerHTML = providers.length
    ? providers.map((p) => providerCard(p, agg)).join("")
    : `<p class="col-span-full rounded-lg border border-dashed p-6 text-center text-xs text-muted-foreground">No providers configured.</p>`;
}

export function renderModels() {
  const models = getState().models;
  $("#model-list").innerHTML = models.length
    ? models.map(modelBlock).join("")
    : `<p class="rounded-lg border border-dashed p-6 text-center text-xs text-muted-foreground">No models configured.</p>`;
}

export function renderTtftList() {
  const models = getState().models;
  const topModels = [...models]
    .sort((a, b) => b.reqNum - a.reqNum)
    .slice(0, 10);
  const caption = $("#ttft-caption");
  if (caption)
    caption.textContent = `Time to first token · p50 · 24h · top ${topModels.length} of ${models.length} models, by requests`;
  if (!topModels.length) {
    $("#ttft-list").innerHTML =
      `<p class="text-xs text-muted-foreground">No traffic recorded yet.</p>`;
    return;
  }
  const maxTtft = Math.max(...topModels.map((m) => m.ttft), 1);
  $("#ttft-list").innerHTML = topModels
    .map(
      (m) => `
    <div>
      <div class="mb-1 flex justify-between"><span class="font-mono">${esc(m.name)}</span><span class="font-mono text-muted-foreground">${fmtDur(m.ttft)}</span></div>
      <div class="h-2 rounded-full bg-muted"><div class="h-2 rounded-full bg-primary" style="width:${Math.round((m.ttft / maxTtft) * 100)}%"></div></div>
    </div>`,
    )
    .join("");
}

// ----- dashboard ----------------------------------------------------------------

function fmtUptime(sec) {
  if (sec == null) return "—";
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (d > 0) return `up ${d}d ${h}h`;
  if (h > 0) return `up ${h}h ${m}m`;
  return `up ${m}m`;
}

function renderSidebarStatus() {
  const status = getStatus();
  const health = $("#router-health");
  const uptime = $("#router-uptime");
  const dot = $("#router-dot");
  if (!health || !status) return;
  // A fresh process has no reload yet (zero-epoch timestamp) — that is healthy,
  // not a failure. Only an actual rejected reload turns the badge red.
  const at = status.last_reload?.at ?? "";
  const neverReloaded = !at || at.startsWith("0001-01-01");
  const ok = neverReloaded || status.last_reload?.ok !== false;
  health.textContent = ok ? "healthy" : "reload failed";
  health.className = ok ? "text-foreground" : "text-destructive";
  if (dot) dot.className = "dot " + (ok ? "dot-live" : "dot-warn");
  if (uptime) uptime.textContent = fmtUptime(getUptimeSeconds());
  const cfg = $("#sidebar-config");
  if (cfg && status.config_path)
    cfg.textContent = status.config_path.split(/[\\/]/).pop();
}

let chartRange = "24h";

function renderTrafficChart() {
  const wrap = $("#chart-traffic");
  if (!wrap) return;
  const series = adaptTraffic(getMetrics(), chartRange);
  const sub = $("#traffic-sub");
  if (sub)
    sub.textContent =
      chartRange === "7d"
        ? "Requests vs upstream errors · daily buckets · last 7 days"
        : "Requests vs upstream errors · hourly buckets · last 24h";

  if (!series.some((s) => s.req > 0)) {
    wrap.innerHTML = `<p class="flex h-56 items-center justify-center text-xs text-muted-foreground">No traffic recorded yet.</p>`;
    return;
  }

  wrap.innerHTML = chartSvg(
    [
      { values: series.map((s) => s.req), color: "hsl(var(--primary))" },
      {
        values: series.map((s) => s.err),
        color: "hsl(var(--destructive))",
        dash: true,
      },
    ],
    220,
  );
  attachChart(
    "chart-traffic",
    series.map((s) => `<b>${s.label}</b><br>${s.tip}`),
  );
}

function renderKpis() {
  const g = getMetrics()?.global;
  const set = (id, html) => {
    const el = $("#" + id);
    if (el) el.innerHTML = html;
  };
  const errPct = g && g.req > 0 ? ((g.err / g.req) * 100).toFixed(2) : null;
  set("kpi-req", fmtInt(g?.req ?? 0));
  set("kpi-req-sub", `${getStatus()?.models_serving ?? 0} models serving`);
  set(
    "kpi-success",
    g && g.req > 0 ? `${g.success_pct}<span class="text-sm">%</span>` : "—",
  );
  set(
    "kpi-success-sub",
    g && g.req > 0 ? `${fmtInt(g.req - g.err)} ok` : "no traffic yet",
  );
  set("kpi-ttft", g?.ttft_p50_ms ? fmtDur(g.ttft_p50_ms) : "—");
  set("kpi-ttft-sub", g?.ttft_p95_ms ? `p95 ${fmtDur(g.ttft_p95_ms)}` : "");
  set(
    "kpi-tok",
    g?.tok_per_sec
      ? `${fmtInt(Math.round(g.tok_per_sec))}<span class="text-sm"> tok/s</span>`
      : "—",
  );
  set("kpi-tok-sub", g?.tokens ? `${fmtInt(g.tokens)} tokens out` : "");
  set("kpi-err", fmtInt(g?.err ?? 0));
  set("kpi-err-sub", errPct != null ? `${errPct}% of requests` : "");
}

function renderTopModels() {
  const box = $("#top-models");
  if (!box) return;
  const models = Object.entries(getMetrics()?.models || {});
  const total = models.reduce((s, [, m]) => s + m.req, 0);
  const top = models.sort((a, b) => b[1].req - a[1].req).slice(0, 5);
  if (!top.length || total === 0) {
    box.innerHTML = `<p class="text-xs text-muted-foreground">No traffic recorded yet.</p>`;
    return;
  }
  box.innerHTML = top
    .map(([name, m]) => {
      const pct = Math.max(1, Math.round((m.req / total) * 100));
      return `<div><div class="mb-1 flex items-center justify-between"><span class="font-mono">${esc(name)}</span><span class="text-muted-foreground">${fmtInt(m.req)} · ${pct}%</span></div><div class="h-2 rounded-full bg-muted"><div class="h-2 rounded-full bg-primary/70" style="width:${pct}%"></div></div></div>`;
    })
    .join("");
}

let recentFailureRows = [];

function renderRecentFailures() {
  const ul = $("#recent-failures");
  if (!ul) return;
  const rows = adaptLogs(getRecentFailures());
  recentFailureRows = rows;
  if (!rows.length) {
    ul.innerHTML = `<li class="px-5 py-6 text-center text-xs text-muted-foreground">No failures in the retained window.</li>`;
    return;
  }
  ul.innerHTML = rows
    .map(
      (r) => `
    <li class="flex cursor-pointer items-center gap-3 px-5 py-3 hover:bg-muted/50${r.level === "error" ? " log-error-row" : ""}" data-open-trace data-seq="${r.seq}">
      <span class="font-mono text-xs text-muted-foreground">${r.time}</span>
      <span class="badge ${r.level === "error" ? "tone-error" : "tone-warn"}">${r.level}</span>
      <span class="min-w-0 flex-1 truncate">${esc(r.msg)}</span>
      <span class="hidden font-mono text-xs text-muted-foreground sm:inline">${r.status ?? "—"}</span>
      <i data-lucide="chevron-right" class="size-4 text-muted-foreground"></i>
    </li>`,
    )
    .join("");
}

// ----- request trace drawer -------------------------------------------------------

export function openTrace(e) {
  if (!e) return;
  const r = adaptLogs([e])[0];
  const d = new Date(e.time);

  $("#trace-id").textContent = e.request_id || "req #" + e.seq;
  $("#trace-status").innerHTML = e.status
    ? `<span class="badge font-mono ${e.status < 400 ? "tone-ok" : "tone-error"}">${e.status}</span>`
    : `<span class="badge tone-error">no response</span>`;
  $("#trace-started").textContent = d.toLocaleString();
  $("#trace-duration").textContent = fmtDur(e.duration_ms);
  $("#trace-model").textContent =
    e.upstream_model && e.upstream_model !== e.model
      ? `${e.model} → ${e.upstream_model}`
      : e.model;
  $("#trace-provider").textContent = `${e.provider || "—"} · ${e.key || "—"}`;
  $("#trace-ttft").textContent = e.ttft_ms ? fmtDur(e.ttft_ms) : "—";
  $("#trace-tokens").textContent = e.tokens_out ? fmtInt(e.tokens_out) : "—";

  const what = $("#trace-what");
  if (e.err) {
    what.className =
      "mb-4 rounded-md border border-destructive/30 bg-destructive/5 p-3.5 text-xs";
    what.innerHTML = `<p class="mb-1 flex items-center gap-2 font-semibold text-destructive"><i data-lucide="alert-triangle" class="size-3.5"></i>What happened</p><p class="leading-relaxed text-foreground">${esc(e.err)}</p>`;
  } else {
    what.className = "mb-4 rounded-md border bg-muted/30 p-3.5 text-xs";
    what.innerHTML = `<p class="mb-1 font-semibold">Outcome</p><p class="leading-relaxed text-foreground">${esc(r.msg)}</p>`;
  }

  // Captured upstream error bodies (2 KB cap per attempt) — this is the
  // debugging payload when a request fails.
  const bodies = [];
  (e.attempts || []).forEach((a) => {
    if (a.resp_body)
      bodies.push({
        label: `${a.provider || "?"}${a.model ? "/" + a.model : ""}${a.status ? " · " + a.status : ""}`,
        body: a.resp_body,
      });
  });
  if (e.resp_body && !bodies.some((b) => b.body === e.resp_body))
    bodies.push({
      label: `final → client${e.status ? " · " + e.status : ""}`,
      body: e.resp_body,
    });
  const respBox = $("#trace-responses");
  if (respBox) {
    if (bodies.length) {
      respBox.className = "mb-4";
      respBox.innerHTML =
        `<p class="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground/70">Upstream response</p>` +
        bodies
          .map(
            (b) => `
        <div class="mb-2 rounded-md border bg-muted/30 p-3">
          <p class="mb-1.5 font-mono text-[11px] font-medium text-muted-foreground">${esc(b.label)}</p>
          <pre class="max-h-48 overflow-auto whitespace-pre-wrap break-all font-mono text-[11px] leading-relaxed text-foreground">${esc(b.body)}</pre>
        </div>`,
          )
          .join("");
    } else {
      respBox.className = "hidden";
      respBox.innerHTML = "";
    }
  }

  const attempts = e.attempts?.length
    ? e.attempts
    : [
        {
          provider: e.provider || "—",
          model: e.upstream_model || e.model,
          key: e.key,
          status: e.status,
          latency_ms: e.duration_ms,
        },
      ];
  $("#trace-timeline").innerHTML = attempts
    .map(
      (a, i) => `
    <li class="relative py-1.5"><span class="absolute -left-[21px] top-3 size-2 rounded-full ${a.status && a.status < 400 ? "bg-primary" : "bg-amber-500"}"></span><p class="font-mono">attempt ${i + 1}/${attempts.length} → ${esc(a.provider)}${a.model ? "/" + esc(a.model) : ""}${a.key ? " · key " + esc(a.key) : ""} · <span class="${a.status && a.status < 400 ? "" : "text-destructive font-semibold"}">${a.status || "—"}</span> · ${fmtDur(a.latency_ms)}${a.note ? " · " + esc(a.note) : ""}</p></li>`,
    )
    .join("");

  $("#trace-raw").textContent = JSON.stringify(e, null, 2);

  $("#drawer").classList.add("open");
  $("#drawer-scrim").classList.add("open");
  window.lucide.createIcons();
}

function refreshAll() {
  renderProviders();
  renderModels();
  renderTtftList();
  renderKpis();
  renderSidebarStatus();
  renderTopModels();
  renderRecentFailures();
  renderTrafficChart();
  buildLogDropdowns();
  if (!getLogFilters().paused) renderLogs();
  window.lucide.createIcons();
}

// ----- custom dropdowns ------------------------------------------------------

export function buildDD(id, options, selected, onPick) {
  const root = $("#" + id);
  root.innerHTML = `
    <button type="button" class="dd-btn"><span class="dd-label">${esc(selected)}</span><i data-lucide="chevron-down"></i></button>
    <div class="dd-menu">${options.map((o) => `<button type="button" class="dd-item${o === selected ? " active" : ""}">${esc(o)}</button>`).join("")}</div>`;
  root.querySelector(".dd-btn").addEventListener("click", (e) => {
    e.stopPropagation();
    const wasOpen = root.classList.contains("open");
    $$(".dd.open").forEach((d) => d.classList.remove("open"));
    if (!wasOpen) root.classList.add("open");
  });
  root.querySelectorAll(".dd-item").forEach((item) =>
    item.addEventListener("click", () => {
      root.querySelector(".dd-label").textContent = item.textContent;
      root
        .querySelectorAll(".dd-item")
        .forEach((i) => i.classList.toggle("active", i === item));
      root.classList.remove("open");
      onPick?.(item.textContent);
    }),
  );
  window.lucide?.createIcons();
}

// ----- chart tooltips --------------------------------------------------------

export function attachChart(id, tips) {
  const wrap = $("#" + id);
  if (!wrap) return;
  const tip = document.createElement("div");
  tip.className = "chart-tip";
  const guide = document.createElement("div");
  guide.className = "chart-guide";
  const cols = document.createElement("div");
  cols.className = "chart-cols";
  tips.forEach((t) => {
    const col = document.createElement("div");
    col.className = "chart-col";
    col.addEventListener("mouseenter", () => {
      tip.innerHTML = t;
      tip.classList.add("show");
      guide.classList.add("show");
      const x = col.offsetLeft + col.offsetWidth / 2;
      guide.style.left = x + "px";
      tip.style.left = Math.min(Math.max(x, 80), wrap.clientWidth - 80) + "px";
    });
    cols.appendChild(col);
  });
  cols.addEventListener("mouseleave", () => {
    tip.classList.remove("show");
    guide.classList.remove("show");
  });
  wrap.append(guide, cols, tip);
}

// ----- per-provider analytics (drawer section) --------------------------------

function polyline(values, w, h, pad, min, max) {
  const span = max - min || 1;
  return values
    .map((v, i) => {
      const x = (i / (values.length - 1)) * w;
      const y = h - pad - ((v - min) / span) * (h - pad * 2);
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
}

function chartSvg(seriesList, h = 130) {
  const all = seriesList.flatMap((s) => s.values);
  const min = Math.min(...all);
  const max = Math.max(...all);
  const pad = 12;
  const grid = [0.25, 0.5, 0.75]
    .map(
      (t) =>
        `<line x1="0" x2="640" y1="${(h * t).toFixed(1)}" y2="${(h * t).toFixed(1)}" stroke="hsl(var(--border))" stroke-width="1"/>`,
    )
    .join("");
  const lines = seriesList
    .map(
      (s) =>
        `<polyline points="${polyline(s.values, 640, h, pad, min, max)}" fill="none" stroke="${s.color}" stroke-width="2" ${s.dash ? 'stroke-dasharray="4 3"' : ""}/>`,
    )
    .join("");
  return `<svg viewBox="0 0 640 ${h}" class="w-full" style="height:${h}px" preserveAspectRatio="none">${grid}${lines}</svg>`;
}

function buildAnalytics(name) {
  const s = getMetrics()?.providers?.[name];
  if (!s || s.req === 0)
    return `<p class="rounded-lg border border-dashed p-6 text-center text-xs text-muted-foreground">No metrics recorded for this provider in the last 24 hours.</p>`;

  const cell = (label, value) =>
    `<div class="rounded-md border bg-muted/30 p-2.5"><dt class="text-muted-foreground">${label}</dt><dd class="mt-1 font-mono text-lg font-semibold">${value}</dd></div>`;
  return `
    <div class="rounded-lg border bg-card p-4">
      <p class="mb-2 text-sm font-medium">Last 24 hours</p>
      <dl class="grid grid-cols-3 gap-2.5 text-xs">
        ${cell("Requests", fmtInt(s.req))}
        ${cell("Errors", fmtInt(s.err))}
        ${cell("Success", s.req > 0 ? s.success_pct + "%" : "—")}
        ${cell("TTFT p50", s.ttft_p50_ms ? fmtDur(s.ttft_p50_ms) : "—")}
        ${cell("TTFT p95", s.ttft_p95_ms ? fmtDur(s.ttft_p95_ms) : "—")}
        ${cell(
          "Tok/s",
          s.tok_per_sec ? fmtInt(Math.round(s.tok_per_sec)) : "—",
        )}
      </dl>
    </div>`;
}

// ----- provider detail drawer --------------------------------------------------

let currentProvider = null;

export function openProvider(name, onOpened) {
  currentProvider = name;
  const p = getState().providers.find((x) => x.name === name);
  if (!p) return;
  const s = STATUS_META[p.status];
  const rows = providerModelRows(name, getMetrics(), getStatus());
  const agg = providerAgg(getMetrics())[name];
  const totalReq = rows.reduce((sum, r) => sum + r.req, 0);
  const maxP50 = Math.max(...rows.map((r) => r.p50), 1);
  const slowestP50 = Math.max(...rows.map((r) => r.p50), 0);

  $("#pd-title").innerHTML =
    `<span class="dot ${s.dot}"></span><p class="text-sm font-semibold">${esc(p.name)}</p><span class="badge ${s.badge}">${s.label}</span>`;

  const summary = `
    <dl class="mb-4 grid grid-cols-3 gap-2.5 text-xs">
      <div class="rounded-md border bg-muted/30 p-2.5"><dt class="text-muted-foreground">TTFT p50 · weighted</dt><dd class="mt-1 font-mono text-lg font-semibold">${agg ? fmtDur(agg) : "—"}</dd></div>
      <div class="rounded-md border bg-muted/30 p-2.5"><dt class="text-muted-foreground">Requests 24h</dt><dd class="mt-1 font-mono text-lg font-semibold">${p.req}</dd></div>
      <div class="rounded-md border bg-muted/30 p-2.5"><dt class="text-muted-foreground">Errors 24h</dt><dd class="mt-1 font-mono text-lg font-semibold">${p.err}</dd></div>
      <div class="rounded-md border bg-muted/30 p-2.5"><dt class="text-muted-foreground">Keys</dt><dd class="mt-1 font-mono">${keyLine(p)}</dd></div>
      <div class="rounded-md border bg-muted/30 p-2.5"><dt class="text-muted-foreground">Uptime 24h</dt><dd class="mt-1 font-mono">${p.up}</dd></div>
      <div class="rounded-md border bg-muted/30 p-2.5"><dt class="text-muted-foreground">Models served</dt><dd class="mt-1 font-mono text-lg font-semibold">${p.model_count ?? rows.length}</dd></div>
    </dl>`;

  const table =
    rows.length === 0
      ? `<div class="rounded-md border border-dashed p-6 text-center text-xs text-muted-foreground">No models are currently routed through this provider.</div>`
      : `
    <div class="overflow-x-auto rounded-md border">
      <table class="w-full min-w-[520px] text-left text-xs">
        <thead>
          <tr class="border-b bg-muted/40 text-[11px] uppercase tracking-wide text-muted-foreground">
            <th class="px-3 py-2 font-medium">Model</th>
            <th class="px-3 py-2 text-right font-medium">Requests</th>
            <th class="px-3 py-2 font-medium">TTFT p50</th>
            <th class="px-3 py-2 text-right font-medium">p95</th>
            <th class="px-3 py-2 text-right font-medium">Tok/s</th>
          </tr>
        </thead>
        <tbody class="divide-y">
          ${rows
            .map((r) => {
              const slowest = rows.length > 1 && r.p50 === slowestP50;
              return `
            <tr>
              <td class="px-3 py-2 font-mono">${esc(r.modelId)}
                <div class="mt-1.5 h-1.5 w-28 rounded-full bg-muted"><div class="h-1.5 rounded-full ${slowest ? "bg-amber-500" : "bg-primary"}" style="width:${Math.round((r.p50 / maxP50) * 100)}%"></div></div>
              </td>
              <td class="px-3 py-2 text-right font-mono">${fmtInt(r.req)}<span class="block text-[10px] text-muted-foreground">${totalReq ? Math.round((r.req / totalReq) * 100) : 0}% share</span></td>
              <td class="px-3 py-2 font-mono">${r.p50 ? fmtDur(r.p50) : "—"}${slowest ? ' <span class="badge tone-warn">slowest</span>' : ""}</td>
              <td class="px-3 py-2 text-right font-mono">${r.p95 ? fmtDur(r.p95) : "—"}</td>
              <td class="px-3 py-2 text-right font-mono">${r.tps || "—"}</td>
            </tr>`;
            })
            .join("")}
        </tbody>
      </table>
    </div>
    <p class="mt-2 text-[11px] leading-relaxed text-muted-foreground">Aggregate p50 is request-weighted across the models above — heavy models pull the provider average. Bar = relative to the slowest model.</p>`;

  const keysSection = `
    <p class="mb-2 mt-4 text-xs font-medium uppercase tracking-wide text-muted-foreground/70">Manage keys</p>
    <div class="overflow-hidden rounded-md border">
      ${p.keyList
        .map(
          (k, i) => `
        <div class="flex items-center gap-3 px-3 py-2.5 text-xs${i > 0 ? " border-t" : ""}${k.on ? "" : " opacity-50"}">
          <span class="font-mono">${esc(k.masked)}</span>
          <span class="badge ${KEY_TONE[k.status]}">${k.status}</span>
          <span class="ml-auto text-muted-foreground">${k.status === "cooldown" ? `cooldown <span class="font-mono text-foreground">${k.cooldown}s</span>` : k.status === "disabled" ? "manually disabled" : ""}</span>
          <input type="checkbox" class="sw" data-action="key-toggle" data-provider="${esc(p.name)}" data-index="${i}" ${k.on ? "checked" : ""} />
        </div>`,
        )
        .join("")}
    </div>
    <p class="mt-1.5 text-[11px] text-muted-foreground">Keys arrive masked from the backend. The manual toggle is runtime-only — a process restart re-enables disabled keys.</p>`;

  const analyticsBar = `
    <div class="mt-4 flex items-center justify-between border-t pt-3">
      <p class="text-xs font-medium uppercase tracking-wide text-muted-foreground/70">Analytics</p>
      <button id="pd-analytics-btn" class="inline-flex items-center gap-2 rounded-md border bg-background px-3 py-1.5 text-xs shadow-sm hover:bg-accent"><i data-lucide="bar-chart-3" class="size-3"></i>View analytics</button>
    </div>
    <div id="pd-analytics" class="mt-3 hidden"></div>`;

  $("#pd-body").innerHTML =
    summary +
    `<p class="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground/70">TTFT by model</p>` +
    table +
    keysSection +
    analyticsBar;

  $("#pd-analytics-btn").addEventListener("click", () => {
    const box = $("#pd-analytics");
    const showing = !box.classList.contains("hidden");
    box.classList.toggle("hidden", showing);
    $("#pd-analytics-btn").innerHTML = showing
      ? `<i data-lucide="bar-chart-3" class="size-3"></i>View analytics`
      : `<i data-lucide="chevron-up" class="size-3"></i>Hide analytics`;
    if (!showing) box.innerHTML = buildAnalytics(name);
    window.lucide.createIcons();
  });

  onOpened();
  window.lucide.createIcons();
}

// ----- add-failover-leg dialog -------------------------------------------------

export function openLegDialog(modelName) {
  const dialog = $("#leg-dialog");
  const m = getState().models.find((x) => x.name === modelName);
  if (!m) return;
  $("#leg-dialog-model").textContent = modelName;
  const existing = new Set(m.legs.map((l) => l.route.split("/")[0]));
  const candidates = getState()
    .providers.filter((p) => p.on && !existing.has(p.name))
    .map((p) => p.name);
  const emptyChoice = "— every healthy provider is already in this chain —";
  buildDD(
    "leg-dialog-provider",
    candidates.length ? candidates : [emptyChoice],
    candidates.length ? candidates[0] : emptyChoice,
  );
  buildDD(
    "leg-dialog-effort",
    ["(none)", "none", "low", "medium", "high", "xhigh", "max"],
    "(none)",
  );
  $("#leg-dialog-upstream").value = "";
  $("#leg-dialog-confirm").disabled = candidates.length === 0;
  dialog.dataset.model = modelName;
  dialog.showModal();
}

// ----- request logs (server-paged) ----------------------------------------
// One page at a time comes from /admin/api/requests (page mode, newest
// first); the state module prefetches +-2 pages so paging feels instant
// without ever pulling the whole ring.

const RANGE_HOURS = {
  "Last 1h": 1,
  "Last 24h": 24,
  "Last 7d": 168,
  "Last 30d": 720,
};

const levelBadge = {
  info: "tone-info",
  warn: "tone-warn",
  error: "tone-error",
};

const csvQuote = (v) => `"${String(v ?? "").replaceAll('"', '""')}"`;

function logFilterFromState() {
  const f = getLogFilters();
  return {
    provider: f.provider || "All providers",
    model: f.model || "All models",
    level: f.level || "All",
    range:
      Object.entries(RANGE_HOURS).find(([, h]) => h === (f.hours || 24))?.[0] ??
      "Last 24h",
  };
}

export function renderLogs() {
  const tbody = $("#log-tbody");
  if (!tbody) return;
  const { entries, meta } = getLogsPage();
  const rows = adaptLogs(entries);

  if (rows.length === 0) {
    tbody.innerHTML = `<tr><td colspan="10" class="px-4 py-10 text-center text-sm text-muted-foreground">No requests match the current filters.</td></tr>`;
  } else {
    tbody.innerHTML = rows
      .map(
        (r) => `
    <tr class="${r.status && r.status !== 200 ? "log-error-row" : ""} cursor-pointer hover:bg-muted/50" data-open-trace data-seq="${r.seq}">
      <td class="whitespace-nowrap px-4 py-2.5 font-mono text-xs">${r.time}</td>
      <td class="px-3 py-2.5"><span class="badge ${levelBadge[r.level] ?? "tone-info"}">${r.level}</span></td>
      <td class="max-w-[340px] truncate px-3 py-2.5">${esc(r.msg)}</td>
      <td class="px-3 py-2.5 font-mono text-xs">${esc(r.provider)}</td>
      <td class="px-3 py-2.5 font-mono text-xs">${esc(r.model)}</td>
      <td class="px-3 py-2.5 font-mono text-xs">${esc(r.key)}</td>
      <td class="px-3 py-2.5 text-right font-mono text-xs">${r.ttft}</td>
      <td class="px-3 py-2.5 text-right font-mono text-xs${r.tps == null ? " text-muted-foreground" : ""}">${r.tps ?? "—"}</td>
      <td class="px-3 py-2.5 text-right">${r.status == null ? '<span class="text-muted-foreground">—</span>' : `<span class="badge font-mono ${r.status === 200 ? "tone-ok" : "tone-error"}">${r.status}</span>`}</td>
      <td class="px-3 py-2.5 text-right"><i data-lucide="chevron-right" class="ml-auto size-4 text-muted-foreground"></i></td>
    </tr>`,
      )
      .join("");
  }

  const start = (meta.page - 1) * meta.per_page;
  $("#log-rows-label").innerHTML = meta.total
    ? `Rows <span class="font-mono text-foreground">${start + 1}–${start + rows.length}</span> of <span class="font-mono text-foreground">${meta.total}</span>`
    : `Rows <span class="font-mono text-foreground">0</span> of <span class="font-mono text-foreground">0</span>`;

  const errBtn = $("#log-level-seg [data-level='error']");
  let errCount = errBtn?.querySelector("#log-error-count");
  if (errBtn && !errCount) {
    errCount = document.createElement("span");
    errCount.id = "log-error-count";
    errCount.className =
      "ml-1 rounded-full bg-destructive/15 px-1.5 text-[10px] font-semibold text-destructive";
    errBtn.appendChild(errCount);
  }
  if (errCount) errCount.textContent = meta.error_total ?? 0;

  // Windowed pagination: Prev/Next plus at most 5 page buttons centred on the
  // current one — never the whole range.
  const page = meta.page;
  const totalPages = Math.max(1, meta.total_pages);
  const first = Math.max(1, Math.min(page - 2, totalPages - 4));
  const last = Math.min(totalPages, first + 4);
  const pbtn = (
    label,
    target,
    { active = false, disabled = false, ellipsis = false } = {},
  ) =>
    ellipsis
      ? `<span class="px-1 text-xs text-muted-foreground">…</span>`
      : `<button data-log-page="${target}"${disabled ? " disabled" : ""} class="rounded border px-2 py-1 hover:bg-accent disabled:opacity-40${active ? " bg-primary font-medium text-primary-foreground" : ""}">${label}</button>`;
  let bar = pbtn("Prev", page - 1, { disabled: page === 1 });
  if (first > 1) {
    bar += pbtn(1, 1);
    if (first > 2) bar += pbtn("…", 0, { ellipsis: true, disabled: true });
  }
  for (let n = first; n <= last; n++) bar += pbtn(n, n, { active: n === page });
  if (last < totalPages) {
    if (last < totalPages - 1)
      bar += pbtn("…", 0, { ellipsis: true, disabled: true });
    bar += pbtn(totalPages, totalPages);
  }
  bar += pbtn("Next", page + 1, { disabled: page === totalPages });
  $("#log-pagination").innerHTML = bar;
  window.lucide.createIcons();
}

async function goToLogPage(page) {
  if (page < 1 || page > getLogsPage().meta.total_pages) return;
  await loadLogsPage(page);
}

// Rebuilt only when the provider/model name set actually changes, so an open
// dropdown is not yanked away mid-interaction by the 3s poll.
let ddSig = "";

function buildLogDropdowns() {
  const { providers, models } = getState();
  const sig =
    providers.map((p) => p.name).join(",") +
    "|" +
    models.map((m) => m.name).join(",");
  if (sig === ddSig) return;
  ddSig = sig;

  const f = logFilterFromState();
  if (!providers.some((p) => p.name === f.provider)) {
    f.provider = "All providers";
    if (getLogFilters().provider) setLogFilter({ provider: "" });
  }
  if (!models.some((m) => m.name === f.model)) {
    f.model = "All models";
    if (getLogFilters().model) setLogFilter({ model: "" });
  }

  buildDD(
    "dd-provider",
    ["All providers", ...providers.map((p) => p.name)],
    f.provider,
    (v) => {
      const cur = logFilterFromState();
      if (v === cur.provider) return;
      setLogFilter({ provider: v === "All providers" ? "" : v });
    },
  );
  buildDD(
    "dd-model",
    ["All models", ...models.map((m) => m.name)],
    f.model,
    (v) => {
      const cur = logFilterFromState();
      if (v === cur.model) return;
      setLogFilter({ model: v === "All models" ? "" : v });
    },
  );
}

// ----- init ---------------------------------------------------------------------

let wired = false;

export function initDynamic() {
  refreshAll();

  if (wired) return;
  wired = true;

  // The sidebar uptime ticks locally: a pulse-only poll changes no state, so
  // the label needs its own clock.
  setInterval(() => {
    if (getUptimeSeconds() != null) renderSidebarStatus();
  }, 10_000);

  buildDD(
    "dd-range",
    ["Last 1h", "Last 24h", "Last 7d", "Last 30d"],
    "Last 24h",
    (v) => {
      setLogFilter({ hours: RANGE_HOURS[v] ?? 24 });
    },
  );

  $("#chart-range-seg")?.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-range]");
    if (!btn) return;
    chartRange = btn.dataset.range;
    renderTrafficChart();
  });
  $("#reload-btn")?.addEventListener("click", async (e) => {
    e.currentTarget.disabled = true;
    await reloadConfig();
    e.currentTarget.disabled = false;
  });
  $("#recent-failures")?.addEventListener("click", (e) => {
    const row = e.target.closest("[data-open-trace]");
    if (!row) return;
    const ev = recentFailureRows.find(
      (r) => r.entry.seq === Number(row.dataset.seq),
    );
    if (ev) openTrace(ev.entry);
  });

  $("#provider-grid").addEventListener("click", (e) => {
    if (e.target.closest("input, button")) return;
    const card = e.target.closest("[data-provider]");
    if (card)
      openProvider(card.dataset.provider, () => {
        $("#provider-drawer").classList.add("open");
        $("#pd-scrim").classList.add("open");
      });
  });
  $("#provider-grid").addEventListener("change", (e) => {
    const input = e.target.closest("input[data-action='provider-toggle']");
    if (input) toggleProvider(input.dataset.name, input.checked);
  });

  // model chains: one delegated listener survives re-renders
  const dispatch = (action, model, index) => {
    if (action === "move-up") moveLeg(model, index, "up");
    else if (action === "move-down") moveLeg(model, index, "down");
    else if (action === "remove-leg") removeLeg(model, index);
    else if (action === "add-leg") openLegDialog(model);
  };
  $("#model-list").addEventListener("click", (e) => {
    const btn = e.target.closest("[data-action]");
    if (btn && btn.tagName === "BUTTON")
      dispatch(
        btn.dataset.action,
        btn.dataset.model,
        Number(btn.dataset.index),
      );
  });
  $("#model-list").addEventListener("change", (e) => {
    const input = e.target.closest("input[data-action]");
    if (!input) return;
    if (input.dataset.action === "model-toggle")
      toggleModel(input.dataset.model, input.checked);
    if (input.dataset.action === "leg-toggle")
      toggleLeg(
        input.dataset.model,
        Number(input.dataset.index),
        input.checked,
      );
  });

  // bound once; #pd-body is static markup
  $("#pd-body").addEventListener("change", (e) => {
    const input = e.target.closest("input[data-action='key-toggle']");
    if (!input) return;
    toggleKey(
      input.dataset.provider,
      Number(input.dataset.index),
      input.checked,
    );
    if (currentProvider)
      withTransition(() =>
        openProvider(currentProvider, () => {
          $("#provider-drawer").classList.add("open");
          $("#pd-scrim").classList.add("open");
        }),
      );
  });

  $("#log-pagination").addEventListener("click", (e) => {
    const btn = e.target.closest("[data-log-page]");
    if (btn) goToLogPage(Number(btn.dataset.logPage));
  });
  let searchTimer = null;
  $("#log-search").addEventListener("input", (e) => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(
      () => setLogFilter({ q: e.target.value.trim() }),
      250,
    );
  });

  // visual active state is handled by wireUi's .seg handler
  $("#log-level-seg").addEventListener("click", (e) => {
    const btn = e.target.closest("[data-level]");
    if (!btn) return;
    setLogFilter({ level: btn.dataset.level });
  });

  $("#log-pause").addEventListener("click", () => {
    const paused = !getLogFilters().paused;
    setLogFilter({ paused });
    $("#log-pause").innerHTML = paused
      ? `<i data-lucide="play" class="size-3.5"></i>Resume`
      : `<i data-lucide="pause" class="size-3.5"></i>Pause`;
    const badge = $("#log-live-badge");
    badge.className = paused ? "badge tone-warn" : "badge tone-ok";
    badge.innerHTML = paused
      ? `<span class="dot dot-warn"></span>PAUSED`
      : `<span class="dot dot-live"></span>LIVE · auto-refresh 3s`;
    window.lucide.createIcons();
  });

  $("#log-export").addEventListener("click", async () => {
    const btn = $("#log-export");
    btn.disabled = true;
    try {
      const header = "time,level,message,provider,model,key,ttft,tok_s,status";
      const f = getLogFilters();
      const res = await api.requestsPage({
        page: 1,
        perPage: 2000,
        provider: f.provider,
        model: f.model,
        level: f.level,
        q: f.q,
        hours: f.hours,
      });
      const lines = adaptLogs(res.entries || []).map((r) =>
        [
          r.time,
          r.level,
          r.msg,
          r.provider,
          r.model,
          r.key,
          r.ttft,
          r.tps ?? "",
          r.status ?? "",
        ]
          .map(csvQuote)
          .join(","),
      );
      const blob = new Blob([header + "\n" + lines.join("\n")], {
        type: "text/csv;charset=utf-8",
      });
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = "routerllm-requests.csv";
      a.click();
      URL.revokeObjectURL(a.href);

      btn.disabled = false;
    } catch {
      setTimeout(() => {
        btn.disabled = false;
      }, 1500);
    }
  });

  subscribe(() => withTransition(refreshAll));
}
