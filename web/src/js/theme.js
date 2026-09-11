import { readLocal, writeLocal } from "./api.js"

const THEME_KEY = "routerllm.admin.theme"

export function initTheme() {
  if (readLocal(THEME_KEY) === "dark") document.documentElement.classList.add("dark")
  document.querySelector("#theme-btn").addEventListener("click", () => {
    const dark = document.documentElement.classList.toggle("dark")
    writeLocal(THEME_KEY, dark ? "dark" : "light")
  })
}
