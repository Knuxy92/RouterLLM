import { MoonIcon, SunIcon } from "./icons"

const THEME_KEY = "routerllm.admin.theme"

export default function ThemeToggle() {
  function toggle() {
    const isDark = document.documentElement.classList.toggle("dark")
    try {
      localStorage.setItem(THEME_KEY, isDark ? "dark" : "light")
    } catch {
      // storage unavailable — the class still applies for this session
    }
  }

  return (
    <button id="theme-btn" type="button" className="rounded-md border p-1.5" onClick={toggle}>
      <SunIcon className="hidden size-4 dark:block" />
      <MoonIcon className="size-4 dark:hidden" />
    </button>
  )
}
