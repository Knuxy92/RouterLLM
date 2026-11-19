import { useEffect, useRef, useState, type SubmitEvent } from "react"
import { api } from "../lib/api"
import { adminStore } from "../lib/store"

const AUTHED_EVENT = "routerllm:authed"
const UNAUTHORIZED_EVENT = "routerllm:unauthorized"

function refreshIcons() {
  const lucide = (window as unknown as { lucide?: { createIcons(): void } }).lucide
  lucide?.createIcons()
}

function showShell() {
  document.querySelector("#page-login")?.classList.add("hidden")
  document.querySelector("#app")?.classList.remove("hidden")
}

function showLogin(message = "") {
  document.querySelector("#app")?.classList.add("hidden")
  document.querySelector("#page-login")?.classList.remove("hidden")
  const error = document.querySelector<HTMLElement>("#login-error")
  if (error) {
    error.textContent = message
    error.classList.toggle("hidden", !message)
  }
}

export default function LoginGate() {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [visible, setVisible] = useState(true)
  const secretRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    refreshIcons()
  })

  useEffect(() => {
    let active = true

    const reveal = () => {
      if (!active) return
      setVisible(false)
      showShell()
      void adminStore.init().then(() => adminStore.startPolling())
      window.dispatchEvent(new Event(AUTHED_EVENT))
    }
    const onUnauthorized = (event: Event) => {
      if (!active) return
      const message =
        event instanceof CustomEvent && typeof event.detail === "string"
          ? event.detail
          : "Session expired — sign in again"
      setVisible(true)
      setError(message)
      showLogin(message)
    }
    const onSignOut = async () => {
      try {
        await api.logout()
      } catch {
        // session already gone
      }
      adminStore.stopPolling()
      window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT, { detail: "" }))
    }

    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
    document.querySelector("#signout-btn")?.addEventListener("click", onSignOut)

    api.status().then(reveal).catch(() => {
      if (active) {
        setVisible(true)
        showLogin()
      }
    })

    return () => {
      active = false
      window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
      document.querySelector("#signout-btn")?.removeEventListener("click", onSignOut)
    }
  }, [])

  async function handleSubmit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault()
    const secret = secretRef.current?.value ?? ""
    if (!secret) return

    setBusy(true)
    setError("")
    try {
      await api.login(secret)
      if (secretRef.current) secretRef.current.value = ""
      setVisible(false)
      showShell()
      void adminStore.init().then(() => adminStore.startPolling())
      window.dispatchEvent(new Event(AUTHED_EVENT))
    } catch (err) {
      const msg = err instanceof Error ? err.message : "Sign-in failed"
      setError(msg)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      {visible && (
        <div
          id="page-login"
          className="flex min-h-dvh items-center justify-center bg-muted/40 px-4"
        >
          <div className="w-full max-w-sm">
            <div className="mb-6 flex flex-col items-center gap-2">
              <div className="flex size-11 items-center justify-center rounded-lg border bg-background shadow-sm">
                <i data-lucide="waypoints" className="size-5"></i>
              </div>
              <h1 className="text-lg font-semibold tracking-tight">RouterLLM</h1>
              <p className="text-sm text-muted-foreground">
                Sign in to the admin console
              </p>
            </div>

            <div className="rounded-lg border bg-card p-6 shadow-sm">
              <form id="login-form" className="flex flex-col gap-4" onSubmit={handleSubmit}>
                <div className="flex flex-col gap-2">
                  <label htmlFor="token" className="text-sm font-medium">Admin secret</label>
                  <div className="relative">
                    <i
                      data-lucide="key-round"
                      className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
                    ></i>
                    <input
                      id="token"
                      name="token"
                      ref={secretRef}
                      type="password"
                      autoComplete="off"
                      className="w-full rounded-md border bg-transparent py-2 pl-9 pr-3 text-sm shadow-sm outline-none focus:border-ring focus:ring-2 focus:ring-ring/30"
                      placeholder="Paste admin token"
                    />
                  </div>
                  <p
                    id="login-error"
                    className={`text-xs font-medium text-destructive ${error ? "" : "hidden"}`}
                  >
                    {error}
                  </p>
                </div>

                <button
                  type="submit"
                  disabled={busy}
                  className="mt-1 w-full rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground shadow-sm hover:bg-primary/90 disabled:opacity-50"
                >
                  Sign in
                </button>
              </form>
            </div>

            <p className="mt-4 text-center text-xs text-muted-foreground">
              Sessions last 12 hours and survive page reloads on this device
            </p>
          </div>
        </div>
      )}
    </>
  )
}
