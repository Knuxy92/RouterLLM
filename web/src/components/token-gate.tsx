import { useState } from "react"

import { clearToken, login } from "@/lib/api"
import { Button } from "@/components/ui/primitives"

type TokenGateProps = {
  message: string
  onUnlock: () => void
}

export function TokenGate({ message, onUnlock }: TokenGateProps) {
  const [value, setValue] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const secret = value.trim()
    if (!secret || busy) return

    setBusy(true)
    setError(null)
    try {
      await login(secret)
      onUnlock()
    } catch (err) {
      // A stale session from a previous login must not survive a failed one.
      clearToken()
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="flex min-h-dvh items-center justify-center px-6">
      <div className="w-full max-w-sm rounded-lg border border-line bg-panel p-6">
        <h1 className="text-sm font-semibold tracking-[0.14em] text-ink-dim uppercase">
          RouterLLM admin
        </h1>
        <p id="token-help" className="mt-3 text-sm text-ink-dim">{message}</p>

        <form onSubmit={(event) => void submit(event)}>
          <label htmlFor="token" className="mt-5 block text-[11px] text-ink-dim">
            Admin secret
          </label>
          <input
            id="token"
            type="password"
            value={value}
            autoFocus
            required
            autoComplete="current-password"
            aria-describedby="token-help token-note"
            aria-invalid={error !== null}
            onChange={(event) => setValue(event.target.value)}
            className="ident mt-1.5 w-full rounded border border-line-strong bg-inset px-3 py-2 text-sm text-ink focus:border-live/50 focus:outline-2 focus:outline-offset-2 focus:outline-ring"
          />
          {error && (
            <p role="alert" className="mt-2 rounded border border-dead/40 bg-dead-bg px-3 py-2 text-xs text-dead">
              {error}
            </p>
          )}

          <Button
            type="submit"
            variant="solid"
            disabled={busy}
            aria-busy={busy}
            className="mt-4 w-full justify-center py-1.5"
          >
            {busy ? "Unlocking…" : "Unlock"}
          </Button>
        </form>

        <p id="token-note" className="mt-4 text-[11px] leading-relaxed text-ink-faint">
          The secret comes from <span className="ident">ROUTERLLM_ADMIN_TOKEN</span> and is never
          sent to the server — the browser proves it knows the secret by answering an HMAC
          challenge, then holds only a session id. While the variable is unset the admin API stays
          disabled and every request returns 403.
        </p>
      </div>
    </main>
  )
}
