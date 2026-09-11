import { api, clearToken, getToken } from "./api.js"
import { closeDrawers, stopUptimeTicker } from "./render.js"
import { resetState, stopPolling } from "./state.js"

// Auth gate: challenge–response handshake. The admin secret never crosses the
// wire — the browser answers a single-use nonce with hex(HMAC-SHA256(secret,
// nonce)) and receives an opaque session id (12h TTL, in-memory server side).

function showLogin() {
  document.querySelector("#page-login").classList.remove("hidden")
  document.querySelector("#app").classList.add("hidden")
}

function showApp() {
  document.querySelector("#page-login").classList.add("hidden")
  document.querySelector("#app").classList.remove("hidden")
}

function showLoginError(msg) {
  const el = document.querySelector("#login-error")
  if (!el) return
  el.textContent = msg || ""
  el.classList.toggle("hidden", !msg)
}

/**
 * Reveal the app if a stored session is still valid. Probes /status once:
 * a 401 (expired session, server restart) clears the token and shows login.
 * Returns true when authenticated.
 */
export async function applyAuth() {
  if (!getToken()) {
    showLogin()
    return false
  }

  try {
    await api.status()
  } catch {
    clearToken()
    showLogin()
    return false
  }

  showApp()
  return true
}

export function wireAuth({ onAuthed }) {
  document.querySelector("#login-form").addEventListener("submit", async (e) => {
    e.preventDefault()
    const btn = e.target.querySelector("button[type=submit]")
    const secret = document.querySelector("#token").value
    showLoginError("")
    btn.disabled = true

    try {
      await api.login(secret)
    } catch (err) {
      showLoginError(err?.message || "Sign-in failed")
      return
    } finally {
      btn.disabled = false
    }

    document.querySelector("#token").value = ""
    showApp()
    onAuthed()
  })

  document.querySelector("#signout-btn").addEventListener("click", () => {
    api.logout().catch(() => {})
    stopPolling()
    stopUptimeTicker()
    clearToken()
    resetState()
    closeDrawers()
    location.hash = "#/dashboard"
    showLogin()
  })

  // Emitted by api.js on any 401 — session expired or was evicted server side.
  window.addEventListener("routerllm:unauthorized", () => {
    stopPolling()
    stopUptimeTicker()
    resetState()
    closeDrawers()
    showLoginError("Session expired — sign in again")
    showLogin()
  })
}
