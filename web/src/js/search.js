import { STATUS_META, adaptLogs, esc, fmtDur } from "./data.js"
import { getRequests, getState } from "./state.js"
import { $, openProvider } from "./render.js"

// Global ⌘K palette: pages, models, providers and log lines — usable from any page.

const escRe = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")

function hi(text, q) {
  if (!q) return text
  return text.replace(new RegExp(`(${escRe(q)})`, "ig"), "<u>$1</u>")
}

let results = []
let active = 0

function collect(q) {
  const needle = q.trim().toLowerCase()
  const hit = (...parts) => !needle || parts.some((s) => s.toLowerCase().includes(needle))
  const state = getState()
  const out = []

  const pages = [
    { icon: "layout-dashboard", title: "Dashboard", sub: "Requests · success rate · TTFT overview", meta: "page", go: () => go("dashboard") },
    { icon: "scroll-text", title: "Request logs", sub: "Paged request rows with full traces", meta: "page", go: () => go("logs") },
    { icon: "server", title: "Providers & Models", sub: "Fallback chains · keys · analytics", meta: "page", go: () => go("providers") },
  ]
  pages.filter((p) => hit(p.title, p.sub)).forEach((p) => out.push({ group: "Pages", ...p }))

  state.models.filter((m) => hit(m.name, ...m.legs.map((l) => l.route))).forEach((m) =>
    out.push({
      group: "Models",
      icon: "box",
      title: m.name,
      sub: m.legs.map((l) => l.route).join("  ·  "),
      meta: `${fmtDur(m.ttft)} · req ${m.req}`,
      go: () => gotoModel(m.name),
    })
  )

  state.providers.filter((p) => hit(p.name, p.status)).forEach((p) =>
    out.push({
      group: "Providers",
      icon: "plug",
      title: p.name,
      sub: STATUS_META[p.status].label,
      meta: p.keyList ? `${p.keyList.filter((k) => k.on).length}/${p.keyList.length} keys` : "",
      go: () => gotoProvider(p.name),
    })
  )

  if (needle) {
    adaptLogs(getRequests()).filter((r) => hit(r.msg, r.provider, r.model, r.key, String(r.status ?? "")))
      .slice(0, 6)
      .forEach((r) =>
        out.push({ group: "Logs", icon: "file-text", title: r.msg, sub: `${r.time} · ${r.provider} · ${r.model}`, meta: r.status ?? "—", go: () => go("logs") })
      )
  }

  return out
}

function go(page) {
  if (location.hash === "#/" + page) return
  location.hash = "#/" + page
}

function gotoModel(name) {
  go("providers")
  setTimeout(() => {
    const el = document.querySelector(`[data-model-block="${CSS.escape(name)}"]`)
    if (!el) return
    el.scrollIntoView({ behavior: "smooth", block: "center" })
    el.classList.add("model-flash")
    setTimeout(() => el.classList.remove("model-flash"), 1800)
  }, 80)
}

function gotoProvider(name) {
  go("providers")
  setTimeout(() => openProvider(name, () => {
    $("#provider-drawer").classList.add("open")
    $("#pd-scrim").classList.add("open")
  }), 80)
}

function draw(q) {
  const box = $("#cmdk-results")
  if (results.length === 0) {
    box.innerHTML = `<p class="cmdk-empty">No results for “${q.trim().replace(/[<>&]/g, "")}”</p>`
    return
  }
  let html = ""
  let lastGroup = null
  results.forEach((r, i) => {
    if (r.group !== lastGroup) {
      html += `<p class="cmdk-group">${r.group}</p>`
      lastGroup = r.group
    }
    html += `
      <button type="button" data-idx="${i}" class="cmdk-row${i === active ? " active" : ""}">
        <i data-lucide="${r.icon}"></i>
        <span class="flex min-w-0 flex-col">
          <span class="cmdk-title truncate">${hi(esc(r.title), q)}</span>
          <span class="cmdk-sub">${hi(esc(r.sub), q)}</span>
        </span>
        <span class="cmdk-meta">${r.meta}</span>
      </button>`
  })
  box.innerHTML = html
  box.querySelector(".active")?.scrollIntoView({ block: "nearest" })
  window.lucide?.createIcons()
}

function setActive(i) {
  if (!results.length) return
  active = (i + results.length) % results.length
  $("#cmdk-results").querySelectorAll(".cmdk-row").forEach((row) =>
    row.classList.toggle("active", Number(row.dataset.idx) === active)
  )
  $("#cmdk-results").querySelector(`[data-idx="${active}"]`)?.scrollIntoView({ block: "nearest" })
}

export function wireCmdk() {
  const dialog = $("#cmdk")
  const input = $("#cmdk-input")
  const open = () => {
    input.value = ""
    results = collect("")
    active = 0
    draw("")
    dialog.showModal()
    input.focus()
  }

  $("#global-search").addEventListener("click", open)
  document.addEventListener("keydown", (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
      e.preventDefault()
      if (dialog.open) dialog.close()
      else open()
    }
  })
  input.addEventListener("input", () => {
    results = collect(input.value)
    active = 0
    draw(input.value)
  })
  input.addEventListener("keydown", (e) => {
    if (e.key === "ArrowDown") {
      e.preventDefault()
      setActive(active + 1)
    } else if (e.key === "ArrowUp") {
      e.preventDefault()
      setActive(active - 1)
    } else if (e.key === "Enter") {
      e.preventDefault()
      results[active]?.go()
      dialog.close()
    }
  })
  $("#cmdk-results").addEventListener("click", (e) => {
    const row = e.target.closest("[data-idx]")
    if (!row) return
    results[Number(row.dataset.idx)]?.go()
    dialog.close()
  })
  $("#cmdk-results").addEventListener("mouseover", (e) => {
    const row = e.target.closest("[data-idx]")
    if (row) setActive(Number(row.dataset.idx))
  })
}
