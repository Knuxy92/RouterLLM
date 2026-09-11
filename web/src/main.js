import { applyAuth, wireAuth } from "./js/auth.js"
import { initDynamic, refreshIcons } from "./js/render.js"
import { wireCmdk } from "./js/search.js"
import { startPolling } from "./js/state.js"
import { route, wireRouter } from "./js/router.js"
import { initTheme } from "./js/theme.js"
import { wireUi } from "./js/ui.js"

function onAuthed() {
  initDynamic()
  startPolling()
  route()
}

wireRouter()
wireUi()
wireCmdk()
wireAuth({ onAuthed })
initTheme()

// Auth first: the app shell and its 3s polling only start once the stored
// session proves valid (or a fresh login succeeds).
applyAuth().then((authed) => {
  if (authed) onAuthed()
})

// Guard so a failed icon bundle never takes the rest of the app down.
refreshIcons()
