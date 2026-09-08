import { hmacProof } from "./hmac.js"

// Thin fetch wrapper over /admin/api. The session id minted by the
// challenge-response handshake lives in localStorage; the admin secret itself
// is never stored or sent — only the HMAC proof, once per login.

const SESSION_KEY = "routerllm.admin.session"
const BASE = "/admin/api"

export function getToken() {
  return localStorage.getItem(SESSION_KEY)
}

export function setToken(t) {
  localStorage.setItem(SESSION_KEY, t)
}

export function clearToken() {
  localStorage.removeItem(SESSION_KEY)
}

export class ApiError extends Error {
  constructor(status, message) {
    super(message)
    this.status = status
    this.name = "ApiError"
  }
}

function errorMessage(payload, fallback) {
  if (payload && typeof payload === "object") {
    const msg = payload.error?.message ?? payload.message
    if (msg) return msg
  }
  return fallback
}

export async function call(path, init = {}) {
  const headers = { ...(init.headers || {}) }
  const token = getToken()
  if (token) headers.Authorization = "Bearer " + token

  if (init.body !== undefined && !headers["Content-Type"]) {
    headers["Content-Type"] = "application/json"
  }

  const res = await fetch(BASE + path, { ...init, headers })

  let payload = null
  try {
    payload = await res.json()
  } catch {
    // non-JSON body (proxy error page etc.) — fall through to status handling
  }

  if (res.status === 401 && path !== "/auth/challenge" && path !== "/auth/verify") {
    clearToken()
    window.dispatchEvent(new CustomEvent("routerllm:unauthorized"))
    throw new ApiError(401, errorMessage(payload, "session expired — sign in again"))
  }

  if (!res.ok) {
    throw new ApiError(res.status, errorMessage(payload, res.status + " " + res.statusText))
  }

  return payload
}

/** Full login handshake: nonce in, HMAC proof back, session id stored. */
export async function login(secret) {
  const ch = await call("/auth/challenge", { method: "POST" })
  const proof = hmacProof(secret, ch.nonce)
  const v = await call("/auth/verify", {
    method: "POST",
    body: JSON.stringify({ challenge_id: ch.challenge_id, proof }),
  })
  setToken(v.session)
  return v
}

async function post(path, body) {
  return call(path, { method: "POST", body: JSON.stringify(body ?? {}) })
}

export const api = {
  login,
  status: () => call("/status"),
  metrics: () => call("/metrics"),
  /** Page mode: {page, perPage, provider, model, level, q, hours} → {entries, page, per_page, total, total_pages, error_total, latest} */
  requestsPage: (p = {}) => {
    const qs = new URLSearchParams()
    if (p.page) qs.set("page", p.page)
    if (p.perPage) qs.set("per_page", p.perPage)
    if (p.provider) qs.set("provider", p.provider)
    if (p.model) qs.set("model", p.model)
    if (p.level) qs.set("level", p.level)
    if (p.q) qs.set("q", p.q)
    if (p.hours) qs.set("hours", p.hours)
    const s = qs.toString()
    return call("/requests" + (s ? "?" + s : ""))
  },
  reload: () => post("/reload"),
  setProviderDisabled: (name, disabled) => post("/providers/" + encodeURIComponent(name), { disabled }),
  setKeyDisabled: (name, index, disabled) => post(`/providers/${encodeURIComponent(name)}/keys/${index}`, { disabled }),
  setModelDisabled: (model, disabled) => post("/routes/" + encodeURIComponent(model), { disabled }),
  setRouteDisabled: (model, index, disabled) => post(`/routes/${encodeURIComponent(model)}/${index}`, { disabled }),
  moveRoute: (model, index, direction) => post("/routes/" + encodeURIComponent(model) + "/move", { index, direction }),
  addRouteLeg: (model, leg) => post("/routes/" + encodeURIComponent(model) + "/add", leg),
  removeRouteLeg: (model, index) => post("/routes/" + encodeURIComponent(model) + "/remove", { index }),
}
