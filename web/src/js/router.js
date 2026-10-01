const PAGES = ["dashboard", "logs", "providers"]
const TITLES = { dashboard: "Dashboard", logs: "Request logs", providers: "Providers & Models" }

export function pageFromPath() {
  const path = location.pathname.replace(/^\/admin\/?/, "").replace(/\/$/, "")
  return PAGES.includes(path) ? path : "dashboard"
}

export function go(page) {
  const target = "/admin/" + page
  if (location.pathname === target) return
  history.pushState(null, "", target)
  route()
}

export function route() {
  if (location.hash.startsWith("#/")) {
    const legacy = location.hash.replace("#/", "")
    history.replaceState(null, "", "/admin/" + (PAGES.includes(legacy) ? legacy : "dashboard"))
  }
  const page = pageFromPath()
  PAGES.forEach((p) => document.querySelector("#page-" + p).classList.toggle("hidden", p !== page))
  document.querySelectorAll("[data-nav]").forEach((a) => a.classList.toggle("active", a.dataset.nav === page))
  document.querySelector("#page-title").textContent = TITLES[page]
  document.querySelector("#sidebar").classList.add("-translate-x-full")
  document.querySelector("#sidebar-scrim").classList.remove("open")
}

export function wireRouter() {
  window.addEventListener("popstate", route)
  document.addEventListener("click", (e) => {
    const a = e.target.closest('a[href^="/admin/"]')
    if (!a) return
    e.preventDefault()
    go(a.getAttribute("href").replace(/^\/admin\/?/, "").replace(/\/$/, "") || "dashboard")
  })
}
