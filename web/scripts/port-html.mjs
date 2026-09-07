import fs from "node:fs"

const wire = fs.readFileSync("wireframe/index.html", "utf8")

const bodyStart = wire.indexOf("<body")
const bodyEnd = wire.lastIndexOf("</body>") + "</body>".length
let body = wire.slice(bodyStart, bodyEnd)

// Replace the trailing inline script block with the module entrypoint.
body = body.replace(
  /\s*<script>[\s\S]*?<\/script>\s*\n<\/body>/,
  '\n<script type="module" src="/src/main.js"></script>\n</body>',
)

// Rebrand: this is the real app now, not the wireframe.
body = body.replaceAll("WIREFRAME v0.3 — static mock, no backend", "MOCK DATA — native build, backend wiring next")

const html = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<meta name="color-scheme" content="light dark" />
<title>RouterLLM admin</title>
<link rel="preconnect" href="https://fonts.googleapis.com" />
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet" />
<link rel="stylesheet" href="/src/index.css" />
<script src="/admin/vendor/lucide.min.js"></script>
</head>
${body}
</html>
`

fs.writeFileSync("index.html", html)
console.log("written", html.length, "bytes; inline script stripped:", !html.includes("const $ ="))
