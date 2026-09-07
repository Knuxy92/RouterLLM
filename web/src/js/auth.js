const SESSION_KEY = "routerllm.admin.session"

export const isAuthed = () => localStorage.getItem(SESSION_KEY) === "1"

// Auth gate: login first, once per device (mock — any token accepted).
// Replace signIn's body with the HMAC challenge/verify flow when wiring the backend.
export function applyAuth() {
  const authed = isAuthed()
  document.querySelector("#page-login").classList.toggle("hidden", authed)
  document.querySelector("#app").classList.toggle("hidden", !authed)
  return authed
}

export function wireAuth({ onAuthed }) {
  document.querySelector("#login-form").addEventListener("submit", (e) => {
    e.preventDefault()
    localStorage.setItem(SESSION_KEY, "1")
    applyAuth()
    onAuthed()
  })
  document.querySelector("#signout-btn").addEventListener("click", () => {
    localStorage.removeItem(SESSION_KEY)
    location.hash = "#/dashboard"
    applyAuth()
  })
}
