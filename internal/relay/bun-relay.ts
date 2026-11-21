const UPSTREAM_DEFAULT = "https://opencode.ai";

// args: --port <n> --upstream <origin>   (both required; spawned by RouterLLM)
let port = 9999;
let upstream = UPSTREAM_DEFAULT;
const argv = process.argv.slice(2);
for (let i = 0; i < argv.length; i++) {
  if (argv[i] === "--port") port = Number(argv[++i]);
  if (argv[i] === "--upstream") upstream = argv[++i];
}

// /health lets the parent confirm the child is actually serving before it
// rewrites any provider base_url.
Bun.serve({
  hostname: "127.0.0.1",
  port,
  async fetch(req) {
    const url = new URL(req.url);
    if (url.pathname === "/health") return new Response("ok");

    // Forward verbatim: same path, query, headers and streaming body — only
    // the transport (TLS stack) and origin change.
    const upstreamUrl = upstream + url.pathname + url.search;
    const headers = new Headers(req.headers);
    headers.delete("host");
    headers.delete("content-length");
    return fetch(upstreamUrl, {
      method: req.method,
      headers,
      body: req.body,
      // @ts-ignore
      duplex: "half",
    });
  },
});

console.log(`relay :${port} -> ${upstream}`);
