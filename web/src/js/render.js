import {
  KEY_TONE,
  LOGS,
  MODELS,
  NOTE_TONE,
  PROVIDER_AGG,
  PROVIDERS,
  STATUS_META,
  TRAFFIC,
  WEEK,
  hashStr,
  providerModelRows,
} from "./data.js";
import {
  getState,
  moveLeg,
  removeLeg,
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
  if (!p.keyList) return p.keys;
  const on = p.keyList.filter((k) => k.on).length;
  if (on === 0) return `0/${p.keyList.length} · all disabled`;
  const bad = p.keyList.filter((k) => k.on && k.status !== "healthy");
  return `${on}/${p.keyList.length} · ${bad.length ? bad.length + " " + bad[0].status : "all healthy"}`;
}

export function providerCard(p) {
  const s = STATUS_META[p.status];
  const agg = PROVIDER_AGG[p.name];
  return `
    <div data-provider="${p.name}" title="Click to see TTFT by model" class="cursor-pointer rounded-lg border bg-card p-4 shadow-sm transition-all hover:shadow-md${p.on ? "" : " opacity-60"}">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-2"><span class="dot ${s.dot}"></span><p class="text-sm font-semibold">${p.name}</p><span class="badge ${s.badge}">${s.label}</span></div>
        <input type="checkbox" class="sw" data-action="provider-toggle" data-name="${p.name}" ${p.on ? "checked" : ""} />
      </div>
      <dl class="mt-3 flex flex-col gap-1.5 text-xs">
        <div class="flex justify-between"><dt class="text-muted-foreground">Keys</dt><dd class="font-mono">${keyLine(p)}</dd></div>
        <div class="flex justify-between"><dt class="text-muted-foreground">Requests 24h</dt><dd class="font-mono">${p.req}</dd></div>
        <div class="flex justify-between"><dt class="text-muted-foreground">TTFT p50 · weighted</dt><dd class="font-mono">${agg ? agg + " ms" : "—"}</dd></div>
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
        <span class="min-w-0 truncate font-mono text-xs">${leg.route}</span>
        <span class="badge ${NOTE_TONE[leg.tone]}">${leg.note}</span>
        ${leg.effort ? `<span class="badge tone-info font-mono">effort: ${leg.effort}</span>` : ""}
        <span class="ml-auto flex items-center gap-1.5">
          <button data-action="move-up" data-model="${m.name}" data-index="${i}" ${i === 0 ? "disabled" : ""} class="rounded border p-1 text-muted-foreground hover:bg-accent disabled:opacity-30"><i data-lucide="arrow-up" class="size-3"></i></button>
          <button data-action="move-down" data-model="${m.name}" data-index="${i}" ${i === m.legs.length - 1 ? "disabled" : ""} class="rounded border p-1 text-muted-foreground hover:bg-accent disabled:opacity-30"><i data-lucide="arrow-down" class="size-3"></i></button>
          <input type="checkbox" class="sw" data-action="leg-toggle" data-model="${m.name}" data-index="${i}" ${leg.on === false ? "" : "checked"} />
        </span>
      </li>`,
    )
    .join("");
  return `
    <div data-model-block="${m.name}" class="rounded-lg border bg-card shadow-sm transition-opacity${m.on ? "" : " opacity-60"}">
      <div class="flex flex-wrap items-center gap-3 border-b px-5 py-3">
        <span class="font-mono text-sm font-medium">${m.name}</span>
        <span class="ml-auto flex items-center gap-4 text-xs text-muted-foreground">
          <span>req <span class="font-mono text-foreground">${m.req}</span></span>
          <span>success <span class="font-mono text-foreground">${m.ok}</span></span>
          <span>ttft <span class="font-mono text-foreground">${m.ttft} ms</span></span>
          <input type="checkbox" class="sw" data-action="model-toggle" data-model="${m.name}" ${m.on ? "checked" : ""} />
        </span>
      </div>
      <ul class="divide-y px-5 py-1 text-sm">${legs}
        <li class="py-2.5">
          <button data-action="add-leg" data-model="${m.name}" class="flex w-full items-center justify-center gap-2 rounded-md border border-dashed py-2 text-xs text-muted-foreground hover:bg-accent"><i data-lucide="plus" class="size-3"></i>Add failover leg</button>
        </li>
      </ul>
    </div>`;
}

export function renderProviders() {
  $("#provider-grid").innerHTML = getState()
    .providers.map(providerCard)
    .join("");
}

export function renderModels() {
  $("#model-list").innerHTML = getState().models.map(modelBlock).join("");
}

export function renderTtftList() {
  const models = getState().models;
  const maxTtft = Math.max(...models.map((m) => m.ttft));
  const topModels = [...models]
    .sort((a, b) => parseFloat(b.req) - parseFloat(a.req))
    .slice(0, 10);
  $("#ttft-list").innerHTML = topModels
    .map(
      (m) => `
    <div>
      <div class="mb-1 flex justify-between"><span class="font-mono">${m.name}</span><span class="font-mono text-muted-foreground">${m.ttft} ms</span></div>
      <div class="h-2 rounded-full bg-muted"><div class="h-2 rounded-full bg-primary" style="width:${Math.round((m.ttft / maxTtft) * 100)}%"></div></div>
    </div>`,
    )
    .join("");
}

function refreshAll() {
  renderProviders();
  renderModels();
  window.lucide.createIcons();
}

// ----- custom dropdowns ------------------------------------------------------

export function buildDD(id, options, selected, onPick) {
  const root = $("#" + id);
  root.innerHTML = `
    <button type="button" class="dd-btn"><span class="dd-label">${selected}</span><i data-lucide="chevron-down"></i></button>
    <div class="dd-menu">${options.map((o) => `<button type="button" class="dd-item${o === selected ? " active" : ""}">${o}</button>`).join("")}</div>`;
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

function providerSeries(name) {
  const agg = PROVIDER_AGG[name] ?? 350;
  const h = hashStr(name);
  const degraded =
    getState().providers.find((p) => p.name === name)?.status === "degraded";
  const base = degraded ? 96.2 : 98.8;
  const success = WEEK.map((_, i) => +(base + ((h >> i) % 8) / 10).toFixed(1));
  const p50 = WEEK.map((_, i) =>
    Math.round(agg * (0.88 + ((h >> (i * 2)) % 25) / 100)),
  );
  const p95 = p50.map((v) => Math.round(v * (2.4 + (h % 5) * 0.12)));
  const p99 = p50.map((v) => Math.round(v * (4.6 + (h % 3) * 0.3)));
  return { labels: WEEK, success, p50, p95, p99 };
}

function buildAnalytics(name) {
  const series = providerSeries(name);
  const { success, p50, p95, p99 } = series;
  const successSvg = chartSvg([{ values: success, color: "hsl(142 71% 45%)" }]);
  const ttftSvg = chartSvg([
    { values: p50, color: "#059669" },
    { values: p95, color: "#f59e0b" },
    { values: p99, color: "hsl(var(--destructive))" },
  ]);
  const avg = (success.reduce((a, b) => a + b, 0) / success.length).toFixed(1);
  const html = `
    <div class="flex flex-col gap-3">
      <div class="rounded-lg border bg-card p-4">
        <div class="mb-2 flex items-center justify-between">
          <p class="text-sm font-medium">Success rate · 7d</p>
          <span class="badge tone-ok font-mono">avg ${avg}%</span>
        </div>
        <div class="chart-wrap" id="pd-chart-success">${successSvg}</div>
      </div>
      <div class="rounded-lg border bg-card p-4">
        <div class="mb-2 flex items-center justify-between">
          <p class="text-sm font-medium">TTFT percentiles · 7d</p>
          <span class="flex items-center gap-3 text-xs text-muted-foreground">
            <span class="flex items-center gap-1.5"><span class="h-0.5 w-3 rounded" style="background:#059669"></span>p50</span>
            <span class="flex items-center gap-1.5"><span class="h-0.5 w-3 rounded" style="background:#f59e0b"></span>p95</span>
            <span class="flex items-center gap-1.5"><span class="h-0.5 w-3 rounded" style="background:hsl(var(--destructive))"></span>p99</span>
          </span>
        </div>
        <div class="chart-wrap" id="pd-chart-ttft">${ttftSvg}</div>
      </div>
      <p class="text-[11px] text-muted-foreground">Mock series derived from this provider's aggregate metrics — real history comes with the backend.</p>
    </div>`;
  return { html, series };
}

// ----- provider detail drawer --------------------------------------------------

let currentProvider = null;

export function openProvider(name, onOpened) {
  currentProvider = name;
  const p = getState().providers.find((x) => x.name === name);
  const s = STATUS_META[p.status];
  const rows = providerModelRows(name);
  const agg = PROVIDER_AGG[name];
  const totalReq = rows.reduce((sum, r) => sum + r.req, 0);
  const maxP50 = Math.max(...rows.map((r) => r.p50), 1);
  const slowestP50 = Math.max(...rows.map((r) => r.p50), 0);

  $("#pd-title").innerHTML =
    `<span class="dot ${s.dot}"></span><p class="text-sm font-semibold">${p.name}</p><span class="badge ${s.badge}">${s.label}</span>`;

  const summary = `
    <dl class="mb-4 grid grid-cols-3 gap-2.5 text-xs">
      <div class="rounded-md border bg-muted/30 p-2.5"><dt class="text-muted-foreground">TTFT p50 · weighted</dt><dd class="mt-1 font-mono text-lg font-semibold">${agg ? agg + " ms" : "—"}</dd></div>
      <div class="rounded-md border bg-muted/30 p-2.5"><dt class="text-muted-foreground">Requests 24h</dt><dd class="mt-1 font-mono text-lg font-semibold">${p.req}</dd></div>
      <div class="rounded-md border bg-muted/30 p-2.5"><dt class="text-muted-foreground">Errors 24h</dt><dd class="mt-1 font-mono text-lg font-semibold">${p.err}</dd></div>
      <div class="rounded-md border bg-muted/30 p-2.5"><dt class="text-muted-foreground">Keys</dt><dd class="mt-1 font-mono">${keyLine(p)}</dd></div>
      <div class="rounded-md border bg-muted/30 p-2.5"><dt class="text-muted-foreground">Uptime 24h</dt><dd class="mt-1 font-mono">${p.up}</dd></div>
      <div class="rounded-md border bg-muted/30 p-2.5"><dt class="text-muted-foreground">Models served</dt><dd class="mt-1 font-mono text-lg font-semibold">${rows.length}</dd></div>
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
              const slowest = r.p50 === slowestP50;
              return `
            <tr>
              <td class="px-3 py-2 font-mono">${r.modelId}
                <div class="mt-1.5 h-1.5 w-28 rounded-full bg-muted"><div class="h-1.5 rounded-full ${slowest ? "bg-amber-500" : "bg-primary"}" style="width:${Math.round((r.p50 / maxP50) * 100)}%"></div></div>
              </td>
              <td class="px-3 py-2 text-right font-mono">${r.req.toLocaleString("en-US")}<span class="block text-[10px] text-muted-foreground">${Math.round((r.req / totalReq) * 100)}% share</span></td>
              <td class="px-3 py-2 font-mono">${r.p50} ms${slowest ? ' <span class="badge tone-warn">slowest</span>' : ""}</td>
              <td class="px-3 py-2 text-right font-mono">${r.p95} ms</td>
              <td class="px-3 py-2 text-right font-mono">${r.tps}</td>
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
          <span class="font-mono">${k.masked}</span>
          <span class="badge ${KEY_TONE[k.status]}">${k.status}</span>
          <span class="ml-auto text-muted-foreground">err 24h <span class="font-mono text-foreground">${k.err}</span></span>
          <input type="checkbox" class="sw" data-action="key-toggle" data-provider="${p.name}" data-index="${i}" ${k.on ? "checked" : ""} />
        </div>`,
        )
        .join("")}
    </div>
    <p class="mt-1.5 text-[11px] text-muted-foreground">Keys arrive masked from the backend. Disabling a provider's last key disables the provider.</p>`;

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
    if (!showing) {
      const { html, series } = buildAnalytics(name);
      box.innerHTML = html;
      attachChart(
        "pd-chart-success",
        series.labels.map(
          (d, i) => `<b>${d}</b><br>success ${series.success[i]}%`,
        ),
      );
      attachChart(
        "pd-chart-ttft",
        series.labels.map(
          (d, i) =>
            `<b>${d}</b><br>p50 ${series.p50[i]} · p95 ${series.p95[i]} · p99 ${series.p99[i]} ms`,
        ),
      );
    }
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
    ["low", "medium", "high", "xhigh", "max"],
    "medium",
  );
  $("#leg-dialog-upstream").value = "";
  $("#leg-dialog-confirm").disabled = candidates.length === 0;
  dialog.dataset.model = modelName;
  dialog.showModal();
}

// ----- request logs (paged) -----------------------------------------------

const LOG_PAGE_SIZE = 10;
let logPage = 1;
let logQuery = "";
const RANGE_HOURS = {
  "Last 1h": 1,
  "Last 24h": 24,
  "Last 7d": 168,
  "Last 30d": 720,
};
const logFilter = {
  provider: "All providers",
  model: "All models",
  level: "All",
  range: "Last 24h",
  paused: false,
};

function filteredLogs(skip) {
  const q = logQuery.trim().toLowerCase();
  const maxAge = RANGE_HOURS[logFilter.range] ?? 720;
  return LOGS.filter((r) => {
    if (r.ageH > maxAge) return false;
    if (
      logFilter.provider !== "All providers" &&
      r.provider !== logFilter.provider
    )
      return false;
    if (logFilter.model !== "All models" && r.model !== logFilter.model)
      return false;
    if (
      skip !== "level" &&
      logFilter.level !== "All" &&
      r.level !== logFilter.level
    )
      return false;
    if (
      q &&
      ![r.msg, r.provider, r.model, r.key, r.time, String(r.status ?? "")].some(
        (s) => s.toLowerCase().includes(q),
      )
    )
      return false;
    return true;
  });
}

export function renderLogs() {
  const tbody = $("#log-tbody");
  if (!tbody) return;
  const logs = filteredLogs();
  const totalPages = Math.max(1, Math.ceil(logs.length / LOG_PAGE_SIZE));
  logPage = Math.min(Math.max(logPage, 1), totalPages);
  const start = (logPage - 1) * LOG_PAGE_SIZE;
  const rows = logs.slice(start, start + LOG_PAGE_SIZE);
  const levelBadge = {
    info: "tone-info",
    warn: "tone-warn",
    error: "tone-error",
  };
  if (rows.length === 0) {
    tbody.innerHTML = `<tr><td colspan="10" class="px-4 py-10 text-center text-sm text-muted-foreground">No requests match “${logQuery.trim().replace(/[<>&]/g, "")}”.</td></tr>`;
  } else {
    tbody.innerHTML = rows
      .map(
        (r) => `
    <tr class="${r.status && r.status !== 200 ? "log-error-row" : ""} cursor-pointer hover:bg-muted/50" data-open-trace>
      <td class="whitespace-nowrap px-4 py-2.5 font-mono text-xs">${r.time}</td>
      <td class="px-3 py-2.5"><span class="badge ${levelBadge[r.level]}">${r.level}</span></td>
      <td class="max-w-[340px] truncate px-3 py-2.5">${r.msg}</td>
      <td class="px-3 py-2.5 font-mono text-xs">${r.provider}</td>
      <td class="px-3 py-2.5 font-mono text-xs">${r.model}</td>
      <td class="px-3 py-2.5 font-mono text-xs">${r.key}</td>
      <td class="px-3 py-2.5 text-right font-mono text-xs">${r.ttft}</td>
      <td class="px-3 py-2.5 text-right font-mono text-xs${r.tps == null ? " text-muted-foreground" : ""}">${r.tps ?? "—"}</td>
      <td class="px-3 py-2.5 text-right">${r.status == null ? '<span class="text-muted-foreground">—</span>' : `<span class="badge font-mono ${r.status === 200 ? "tone-ok" : "tone-error"}">${r.status}</span>`}</td>
      <td class="px-3 py-2.5 text-right"><i data-lucide="chevron-right" class="ml-auto size-4 text-muted-foreground"></i></td>
    </tr>`,
      )
      .join("");
  }

  $("#log-rows-label").innerHTML =
    `Rows <span class="font-mono text-foreground">${start + 1}–${start + rows.length}</span> of <span class="font-mono text-foreground">${logs.length}</span>`;
  $("#log-error-count").textContent = filteredLogs("level").filter(
    (r) => r.level === "error",
  ).length;

  const pbtn = (label, page, { active = false, disabled = false } = {}) =>
    `<button data-log-page="${page}"${disabled ? " disabled" : ""} class="rounded border px-2 py-1 hover:bg-accent disabled:opacity-40${active ? " bg-primary font-medium text-primary-foreground" : ""}">${label}</button>`;
  $("#log-pagination").innerHTML =
    pbtn("Prev", logPage - 1, { disabled: logPage === 1 }) +
    Array.from({ length: totalPages }, (_, i) =>
      pbtn(i + 1, i + 1, { active: i + 1 === logPage }),
    ).join("") +
    pbtn("Next", logPage + 1, { disabled: logPage === totalPages });
  window.lucide.createIcons();
}

function goToLogPage(page) {
  const totalPages = Math.max(
    1,
    Math.ceil(filteredLogs().length / LOG_PAGE_SIZE),
  );
  const next = Math.min(Math.max(page, 1), totalPages);
  if (next === logPage) return;
  logPage = next;
  withTransition(renderLogs);
}

// ----- init ---------------------------------------------------------------------

export function initDynamic() {
  refreshAll();
  renderTtftList();
  renderLogs();

  buildDD(
    "dd-provider",
    ["All providers", ...PROVIDERS.map((p) => p.name)],
    "All providers",
    (v) => {
      logFilter.provider = v;
      logPage = 1;
      renderLogs();
    },
  );
  buildDD(
    "dd-model",
    ["All models", ...MODELS.map((m) => m.name)],
    "All models",
    (v) => {
      logFilter.model = v;
      logPage = 1;
      renderLogs();
    },
  );
  buildDD(
    "dd-range",
    ["Last 1h", "Last 24h", "Last 7d", "Last 30d"],
    "Last 24h",
    (v) => {
      logFilter.range = v;
      logPage = 1;
      renderLogs();
    },
  );

  attachChart(
    "chart-traffic",
    TRAFFIC.map(([t, v]) => `<b>${t}</b><br>${v}`),
  );

  // provider cards: click opens drawer, switch toggles state
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

  // provider drawer: per-key enable/disable (bound once; #pd-body is static markup)
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

  // request logs pagination + search
  $("#log-pagination").addEventListener("click", (e) => {
    const btn = e.target.closest("[data-log-page]");
    if (btn) goToLogPage(Number(btn.dataset.logPage));
  });
  $("#log-search").addEventListener("input", (e) => {
    logQuery = e.target.value;
    logPage = 1;
    renderLogs();
  });

  // level segmented filter (visual active state is handled by wireUi's .seg handler)
  $("#log-level-seg").addEventListener("click", (e) => {
    const btn = e.target.closest("[data-level]");
    if (!btn) return;
    logFilter.level = btn.dataset.level;
    logPage = 1;
    renderLogs();
  });

  // pause/resume toggle (mirrors the LIVE badge)
  $("#log-pause").addEventListener("click", () => {
    logFilter.paused = !logFilter.paused;
    $("#log-pause").innerHTML = logFilter.paused
      ? `<i data-lucide="play" class="size-3.5"></i>Resume`
      : `<i data-lucide="pause" class="size-3.5"></i>Pause`;
    const badge = $("#log-live-badge");
    badge.className = logFilter.paused ? "badge tone-warn" : "badge tone-ok";
    badge.innerHTML = logFilter.paused
      ? `<span class="dot dot-warn"></span>PAUSED`
      : `<span class="dot dot-live"></span>LIVE · auto-refresh 3s`;
    window.lucide.createIcons();
  });

  // export the current filtered set as CSV
  $("#log-export").addEventListener("click", () => {
    const header = "time,level,message,provider,model,key,ttft,tok_s,status";
    const lines = filteredLogs().map((r) =>
      [
        r.time,
        r.level,
        `"${r.msg.replaceAll('"', '""')}"`,
        r.provider,
        r.model,
        r.key,
        r.ttft,
        r.tps ?? "",
        r.status ?? "",
      ].join(","),
    );
    const blob = new Blob([header + "\n" + lines.join("\n")], {
      type: "text/csv;charset=utf-8",
    });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = "routerllm-requests.csv";
    a.click();
    URL.revokeObjectURL(a.href);
  });

  // re-render on any committed state change
  subscribe(() => withTransition(refreshAll));
}
