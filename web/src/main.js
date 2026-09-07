import { applyAuth, wireAuth } from "./js/auth.js"
import { initDynamic } from "./js/render.js"
import { wireCmdk } from "./js/search.js"
import { route, wireRouter } from "./js/router.js"
import { initTheme } from "./js/theme.js"
import { wireUi } from "./js/ui.js"

initDynamic()
applyAuth()
route()
wireRouter()
wireUi()
wireCmdk()
wireAuth({ onAuthed: route })
initTheme()
// Guard so a failed icon bundle never takes the rest of the app down.
window.lucide?.createIcons()
