const THEME_KEY = "routerllm.admin.theme"

export function initTheme() {
  if (localStorage.getItem(THEME_KEY) === "dark") document.documentElement.classList.add("dark")
  document.querySelector("#theme-btn").addEventListener("click", () => {
    const dark = document.documentElement.classList.toggle("dark")
    localStorage.setItem(THEME_KEY, dark ? "dark" : "light")
  })
}
