import fs from "node:fs"

let html = fs.readFileSync("index.html", "utf8")

// 1. drop the Analytics nav item
html = html.replace(/\n\s*<a class="nav-item" data-nav="analytics"[^>]*>[\s\S]*?<\/a>/, "")

// 2. drop the whole Analytics page section (banner comments included)
html = html.replace(
  /\n[^\n]*<!-- ={10,} -->\n[^\n]*<!-- 4 · ANALYTICS[^\n]*-->\n[^\n]*<!-- ={10,} -->\n[\s\S]*?<\/section>\n/,
  "\n",
)

// 3. insert the add-failover-leg dialog right before the module entrypoint
const dialog = `
<!-- add failover leg dialog -->
<dialog id="leg-dialog" class="leg-dialog">
  <div class="p-5">
    <h3 class="text-sm font-semibold">Add failover leg</h3>
    <p class="mt-1 text-xs text-muted-foreground">Fallback route for <span id="leg-dialog-model" class="font-mono text-foreground"></span></p>
    <label class="mt-4 block text-xs font-medium text-muted-foreground">
      Provider
      <select id="leg-dialog-provider" class="mt-1.5 w-full rounded-md border bg-transparent px-2.5 py-1.5 text-sm text-foreground focus:border-ring focus:outline-none"></select>
    </label>
    <p class="mt-3 text-[11px] text-muted-foreground">The leg is appended last (lowest priority) and starts in standby. Reorder it with the arrows afterwards.</p>
    <div class="mt-5 flex justify-end gap-2">
      <button id="leg-dialog-cancel" class="rounded-md border px-3 py-1.5 text-sm hover:bg-accent">Cancel</button>
      <button id="leg-dialog-confirm" class="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-40">Add leg</button>
    </div>
  </div>
</dialog>
</body>`
html = html.replace(/\n*<\/body>/, `\n${dialog}`)

fs.writeFileSync("index.html", html)
console.log(
  "analytics nav:", html.includes('data-nav="analytics"'),
  "| analytics section:", html.includes('id="page-analytics"'),
  "| dialog:", html.includes('id="leg-dialog"'),
)
