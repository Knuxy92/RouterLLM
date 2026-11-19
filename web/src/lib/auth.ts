import { api, clearToken, getToken } from "./api"

// Framework-agnostic auth helpers. DOM/login-gate teardown lives in LoginGate.tsx.

export const UNAUTHORIZED_EVENT = "routerllm:unauthorized"

export function hasSession(): boolean {
  return Boolean(getToken())
}

/** Probe /status once; an expired session clears the token and reports false. */
export async function checkSession(): Promise<boolean> {
  if (!hasSession()) return false

  try {
    await api.status()
    return true
  } catch {
    clearToken()
    return false
  }
}

/** Clear every client-side trace of the current session. */
export function clearSession(): void {
  clearToken()
}

/** Best-effort server logout followed by a full local teardown. */
export async function signOut(): Promise<void> {
  try {
    await api.logout()
  } catch {
    // server-side invalidation is best-effort; local teardown always happens
  }
  clearToken()
}
