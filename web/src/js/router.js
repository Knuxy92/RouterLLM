const PAGES = ["dashboard", "logs", "providers"]
const TITLES = { dashboard: "Dashboard", logs: "Request logs", providers: "Providers & Models" }

export function route() {
  let page = location.hash.replace("#/", "") || "dashboard"
  if (!PAGES.includes(page)) page = "dashboard"
  PAGES.forEach((p) => document.querySelector("#page-" + p).classList.toggle("hidden", p !== page))
  document.querySelectorAll("[data-nav]").forEach((a) => a.classList.toggle("active", a.dataset.nav === page))
  document.querySelector("#page-title").textContent = TITLES[page]
  document.querySelector("#sidebar").classList.add("-translate-x-full")
  document.querySelector("#sidebar-scrim").classList.remove("open")
}

export function wireRouter() {
  window.addEventListener("hashchange", route)
}
