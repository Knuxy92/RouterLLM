import { useState } from "react"

import { setToken } from "@/lib/api"
import { Button } from "@/components/ui/primitives"

type TokenGateProps = {
  message: string
  onUnlock: () => void
}

export function TokenGate({ message, onUnlock }: TokenGateProps) {
  const [value, setValue] = useState("")

  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const token = value.trim()
    if (!token) return

    setToken(token)
    onUnlock()
  }

  return (
    <main className="flex min-h-dvh items-center justify-center px-6">
      <div className="w-full max-w-sm rounded-lg border border-line bg-panel p-6">
        <h1 className="text-sm font-semibold tracking-[0.14em] text-ink-dim uppercase">
          RouterLLM admin
        </h1>
        <p id="token-help" className="mt-3 text-sm text-ink-dim">{message}</p>

        <form onSubmit={submit}>
          <label htmlFor="token" className="mt-5 block text-[11px] text-ink-dim">
            Admin token
          </label>
          <input
            id="token"
            type="password"
            value={value}
            autoFocus
            required
            autoComplete="current-password"
            aria-describedby="token-help token-note"
            onChange={(event) => setValue(event.target.value)}
            className="ident mt-1.5 w-full rounded border border-line-strong bg-inset px-3 py-2 text-sm text-ink focus:border-live/50 focus:outline-2 focus:outline-offset-2 focus:outline-ring"
          />

          <Button type="submit" variant="solid" className="mt-4 w-full justify-center py-1.5">
            Unlock
          </Button>
        </form>

        <p id="token-note" className="mt-4 text-[11px] leading-relaxed text-ink-faint">
          The token comes from <span className="ident">ROUTERLLM_ADMIN_TOKEN</span>. While that
          variable is unset the admin API stays disabled and every request returns 403.
        </p>
      </div>
    </main>
  )
}
