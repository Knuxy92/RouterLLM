import { useEffect } from "react"

const THEME_KEY = "routerllm.admin.theme"

export default function ThemeToggle() {
  useEffect(() => {
    const lucide = (window as unknown as { lucide?: { createIcons(): void } }).lucide
    lucide?.createIcons()
  }, [])
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
      <i data-lucide="sun" className="hidden size-4 dark:block"></i>
      <i data-lucide="moon" className="size-4 dark:hidden"></i>
    </button>
  )
}
