import { useEffect, type ComponentType } from "react"
import { BoxIcon, LayoutDashboardIcon, ScrollTextIcon, ServerIcon, WaypointsIcon } from "./icons"

const BASE = import.meta.env.BASE_URL || "/admin/"

type NavIcon = ComponentType<{ className?: string }>

const NAV_GROUPS: { label: string; items: { slug: string; label: string; icon: NavIcon }[] }[] = [
  {
    label: "Platform",
    items: [
      { slug: "dashboard", label: "Dashboard", icon: LayoutDashboardIcon },
      { slug: "logs", label: "Request logs", icon: ScrollTextIcon },
    ],
  },
  {
    label: "Management",
    items: [
      { slug: "providers", label: "Providers & Models", icon: ServerIcon },
    ],
  },
  {
    label: "docs",
    items: [
      { slug: "models", label: "Models detail", icon: BoxIcon },
    ],
  },
]

interface SidebarProps {
  activeSlug?: string
}

export default function Sidebar({ activeSlug = "dashboard" }: SidebarProps) {
  useEffect(() => {
    const sidebar = document.querySelector("#sidebar")
    const scrim = document.querySelector("#sidebar-scrim")
    const close = () => {
      sidebar?.classList.add("-translate-x-full")
      scrim?.classList.remove("open")
    }
    const open = () => {
      sidebar?.classList.remove("-translate-x-full")
      scrim?.classList.add("open")
    }
    const onMenu = () => open()
    const onScrim = () => close()

    document.querySelector("#menu-btn")?.addEventListener("click", onMenu)
    scrim?.addEventListener("click", onScrim)

    return () => {
      document.querySelector("#menu-btn")?.removeEventListener("click", onMenu)
      scrim?.removeEventListener("click", onScrim)
    }
  }, [])

  return (
    <>
      <div
        id="sidebar-scrim"
        className="scrim fixed inset-0 z-30 bg-black/50 md:hidden"
      ></div>
      <aside
        id="sidebar"
        className="fixed inset-y-0 left-0 z-40 flex w-64 -translate-x-full flex-col border-r bg-card transition-transform duration-200 md:translate-x-0"
      >
        <div className="flex h-14 items-center gap-2.5 border-b px-4">
          <div className="flex size-7 items-center justify-center rounded-md border bg-background">
            <WaypointsIcon className="size-4" />
          </div>
          <div className="leading-tight">
            <p className="text-sm font-semibold">RouterLLM</p>
            <p id="sidebar-config" className="font-mono text-[10px] text-muted-foreground">
              routerllm.yaml
            </p>
          </div>
        </div>

        <nav className="flex-1 overflow-y-auto px-3 py-3">
          {NAV_GROUPS.map((group) => (
            <div key={group.label}>
              <p className="px-2.5 pb-1.5 pt-2 text-[11px] font-medium uppercase tracking-wide text-muted-foreground/70">
                {group.label}
              </p>
              {group.items.map((item) => {
                const ItemIcon = item.icon
                return (
                  <a
                    key={item.slug}
                    className={`nav-item ${activeSlug === item.slug ? "active" : ""}`}
                    data-nav={item.slug}
                    href={BASE + item.slug}
                  >
                    <ItemIcon />{item.label}
                  </a>
                )
              })}
            </div>
          ))}
        </nav>

        <div className="border-t px-3 py-3">
          <div className="flex items-center gap-2 rounded-md px-2 py-1.5">
            <span id="router-dot" className="dot dot-muted"></span>
            <p className="text-xs text-muted-foreground">
              Router process <span id="router-health" className="text-foreground">—</span>
            </p>
            <span id="router-uptime" className="badge tone-info ml-auto font-mono">—</span>
          </div>
        </div>
      </aside>
    </>
  )
}
