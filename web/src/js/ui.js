import { addLeg, getLogsPage } from "./state.js"
import { $, $$, openTrace } from "./render.js"

export function wireUi() {
  $("#menu-btn").addEventListener("click", () => {
    $("#sidebar").classList.remove("-translate-x-full")
    $("#sidebar-scrim").classList.add("open")
  })
  $("#sidebar-scrim").addEventListener("click", () => {
    $("#sidebar").classList.add("-translate-x-full")
    $("#sidebar-scrim").classList.remove("open")
  })

  $("#avatar-btn").addEventListener("click", (e) => { e.stopPropagation(); $("#avatar-menu").classList.toggle("hidden") })
  document.addEventListener("click", () => {
    $("#avatar-menu").classList.add("hidden")
    $$(".dd.open").forEach((d) => d.classList.remove("open"))
  })

  // rows re-render per page, so delegate on the tbody
  const closeDrawer = () => {
    $("#drawer").classList.remove("open")
    $("#drawer-scrim").classList.remove("open")
  }
  const closeProviderDrawer = () => {
    $("#provider-drawer").classList.remove("open")
    $("#pd-scrim").classList.remove("open")
  }
  $("#log-tbody").addEventListener("click", (e) => {
    const row = e.target.closest("[data-open-trace]")
    if (!row) return
    const ev = getLogsPage().entries.find((x) => x.seq === Number(row.dataset.seq))
    if (ev) openTrace(ev)
  })
  $("#drawer-close").addEventListener("click", closeDrawer)
  $("#drawer-scrim").addEventListener("click", closeDrawer)

  $("#pd-close").addEventListener("click", closeProviderDrawer)
  $("#pd-scrim").addEventListener("click", closeProviderDrawer)
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape") return
    closeDrawer()
    closeProviderDrawer()
  })

  const dialog = $("#leg-dialog")
  $("#leg-dialog-cancel").addEventListener("click", () => dialog.close())
  $("#leg-dialog-confirm").addEventListener("click", () => {
    const provider = $("#leg-dialog-provider .dd-label")?.textContent.trim()
    const effort = $("#leg-dialog-effort .dd-label")?.textContent.trim()
    const upstream = $("#leg-dialog-upstream").value.trim()
    if (provider && !provider.startsWith("—")) addLeg(dialog.dataset.model, provider, { upstreamModel: upstream, effort: effort === "(none)" ? null : effort })
    dialog.close()
  })
  dialog.addEventListener("click", (e) => {
    if (e.target === dialog) dialog.close()
  })

  $$(".tab-btn").forEach((btn) =>
    btn.addEventListener("click", () => {
      $$(".tab-btn").forEach((b) => b.classList.toggle("active", b === btn))
      $$("[data-pane]").forEach((p) => p.classList.toggle("hidden", p.dataset.pane !== btn.dataset.tab))
    })
  )

  $$(".seg").forEach((seg) =>
    seg.querySelectorAll(".seg-btn").forEach((btn) =>
      btn.addEventListener("click", () =>
        seg.querySelectorAll(".seg-btn").forEach((b) => b.classList.toggle("active", b === btn))
      )
    )
  )
}
