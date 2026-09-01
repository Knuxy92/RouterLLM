import { useCallback, useEffect, useRef, useState } from "react"
import { RefreshCw } from "lucide-react"

import { ApiError, api, clearToken, getToken, type LogEntry, type Status } from "@/lib/api"
import { LogPanel } from "@/components/log-panel"
import { ProviderCard } from "@/components/provider-card"
import { ReloadBanner } from "@/components/reload-banner"
import { RouteChain } from "@/components/route-chain"
import { StatTile } from "@/components/stat-tile"
import { TokenGate } from "@/components/token-gate"
import { Button, EmptyState, Ident, Panel, PanelHeader } from "@/components/ui/primitives"
import { cn } from "@/lib/utils"

const POLL_MS = 3000
const LOG_CAP = 500

export default function App() {
  const [status, setStatus] = useState<Status | null>(null)
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [gate, setGate] = useState<string | null>(
    getToken() ? null : "Enter the admin token to continue.",
  )
  const [pollError, setPollError] = useState<string | null>(null)
  const [mutationError, setMutationError] = useState<string | null>(null)
  const [pending, setPending] = useState<string | null>(null)
  const [paused, setPaused] = useState(false)
  const [dismissedReload, setDismissedReload] = useState<string | null>(null)

  const lastSeq = useRef(0)
  const pollRunningRef = useRef(false)
  const pollAbortRef = useRef<AbortController | null>(null)
  const statusGenerationRef = useRef(0)
  const mutationInFlightRef = useRef(false)
  const pausedRef = useRef(paused)

  useEffect(() => {
    pausedRef.current = paused
  }, [paused])

  const handleAuthFailure = useCallback((error: unknown): boolean => {
    if (!(error instanceof ApiError) || (error.status !== 401 && error.status !== 403)) {
      return false
    }

    clearToken()
    setGate(error.message)

    return true
  }, [])

  const poll = useCallback(async () => {
    if (pollRunningRef.current || document.hidden) return

    const controller = new AbortController()
    const generation = statusGenerationRef.current
    pollRunningRef.current = true
    pollAbortRef.current = controller

    try {
      const statusRequest = api.status(controller.signal)
      const logsRequest = pausedRef.current
        ? Promise.resolve<{ entries: LogEntry[] } | null>(null)
        : api.logs(lastSeq.current, controller.signal)
      const [statusResult, logsResult] = await Promise.allSettled([statusRequest, logsRequest])

      if (controller.signal.aborted) return

      const errors: unknown[] = []
      if (statusResult.status === "fulfilled") {
        if (generation === statusGenerationRef.current) {
          setStatus(statusResult.value)
        }
      } else {
        errors.push(statusResult.reason)
      }

      if (logsResult.status === "fulfilled") {
        const entries = logsResult.value?.entries ?? []
        const unseen = entries.filter((entry) => entry.seq > lastSeq.current)
        if (unseen.length > 0) {
          lastSeq.current = Math.max(...unseen.map((entry) => entry.seq))
          setLogs((current) => appendLogs(current, unseen))
        }
      } else {
        errors.push(logsResult.reason)
      }

      if (errors.length === 0) {
        setPollError(null)
      } else if (!errors.some(handleAuthFailure)) {
        setPollError(errorMessage(errors[0]))
      }
    } finally {
      if (pollAbortRef.current === controller) {
        pollAbortRef.current = null
      }
      pollRunningRef.current = false
    }
  }, [handleAuthFailure])

  useEffect(() => {
    if (gate) return

    let stopped = false
    let timer: ReturnType<typeof setTimeout> | undefined

    const schedule = () => {
      if (stopped || document.hidden) return
      timer = setTimeout(run, POLL_MS)
    }
    const run = async () => {
      if (stopped || document.hidden) return

      await poll()
      schedule()
    }
    const handleVisibility = () => {
      if (document.hidden) {
        if (timer) clearTimeout(timer)
        pollAbortRef.current?.abort()
        return
      }

      if (timer) clearTimeout(timer)
      if (!pollRunningRef.current) void run()
    }

    void run()
    document.addEventListener("visibilitychange", handleVisibility)

    return () => {
      stopped = true
      if (timer) clearTimeout(timer)
      pollAbortRef.current?.abort()
      document.removeEventListener("visibilitychange", handleVisibility)
    }
  }, [gate, poll])

  const mutate = useCallback(
    async (key: string, action: () => Promise<Status>) => {
      if (mutationInFlightRef.current) return

      mutationInFlightRef.current = true
      statusGenerationRef.current += 1
      pollAbortRef.current?.abort()
      setPending(key)
      setMutationError(null)

      try {
        setStatus(await action())
        setPollError(null)
      } catch (error) {
        if (!handleAuthFailure(error)) {
          setMutationError(errorMessage(error))
          await poll()
        }
      } finally {
        mutationInFlightRef.current = false
        setPending(null)
      }
    },
    [handleAuthFailure, poll],
  )

  if (gate) {
    return (
      <TokenGate
        message={gate}
        onUnlock={() => {
          setGate(null)
          setPollError(null)
          setMutationError(null)
        }}
      />
    )
  }

  if (!status) {
    return (
      <main className="flex min-h-dvh items-center justify-center" aria-busy="true">
        <p role={pollError ? "alert" : "status"} className="text-sm text-ink-dim">
          {pollError ?? "Connecting to the router…"}
        </p>
      </main>
    )
  }

  const showReloadError =
    !status.last_reload.ok &&
    Boolean(status.last_reload.error) &&
    dismissedReload !== status.last_reload.at
  const controlsDisabled = pending !== null

  return (
    <div className="mx-auto min-h-dvh max-w-6xl px-4 py-5 sm:px-5 sm:py-6">
      <header className="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-3">
            <h1 className="text-base font-semibold text-ink">RouterLLM</h1>
            <span className="inline-flex items-center gap-1.5 rounded-full border border-live/40 bg-live-bg px-2.5 py-0.5 text-[11px] text-live">
              <span className="size-1.5 rounded-full bg-live" />
              up {formatUptime(status.uptime_seconds)}
            </span>
          </div>
          <Ident className="mt-1 block max-w-[70vw] truncate text-[11px] text-ink-dim" title={status.config_path}>
            {status.config_path}
          </Ident>
        </div>
        <Button
          variant="solid"
          disabled={controlsDisabled}
          aria-busy={pending === "reload"}
          onClick={() => void mutate("reload", api.reload)}
        >
          <RefreshCw className={cn("size-3", pending === "reload" && "animate-spin")} />
          {pending === "reload" ? "Reloading…" : "Reload now"}
        </Button>
      </header>

      <main aria-busy={controlsDisabled}>
        {pollError && (
          <p role="status" className="mb-4 rounded-lg border border-cooling/40 bg-cooling-bg px-4 py-2.5 text-xs text-cooling">
            Status refresh failed. Displaying the last known state: {pollError}
          </p>
        )}

        {mutationError && (
          <p role="alert" className="mb-4 rounded-lg border border-dead/40 bg-dead-bg px-4 py-2.5 text-xs text-dead">
            Action failed: {mutationError}
          </p>
        )}

        {showReloadError && (
          <div className="mb-4">
            <ReloadBanner
              reload={status.last_reload}
              configPath={status.config_path}
              onDismiss={() => setDismissedReload(status.last_reload.at)}
            />
          </div>
        )}

        <section aria-labelledby="status-summary-title" className="mb-5 grid grid-cols-2 gap-3 lg:grid-cols-4">
          <h2 id="status-summary-title" className="sr-only">Router status summary</h2>
          <StatTile
            label="Providers active"
            value={status.providers_active}
            suffix={`/ ${status.providers_total}`}
            tone="live"
          />
          <StatTile
            label="Models serving"
            value={status.models_serving}
            suffix={`/ ${status.models.length}`}
          />
          <StatTile
            label="Requests"
            value={status.providers.reduce((sum, provider) => sum + provider.requests, 0)}
            hint="since process start"
          />
          <StatTile
            label="Errors"
            value={status.providers.reduce((sum, provider) => sum + provider.errors, 0)}
            tone="dead"
            hint="upstream failures"
          />
        </section>

        <div className="flex flex-col gap-5">
          <Panel>
            <PanelHeader title="Providers" meta={`${status.providers.length} configured`} />
            {status.providers.length === 0 ? (
              <EmptyState title="No providers configured" detail="Add a provider to routerllm.yaml to start routing requests." />
            ) : (
              <div className="grid gap-3 p-4 md:grid-cols-2 xl:grid-cols-3">
                {status.providers.map((provider) => (
                  <ProviderCard
                    key={provider.name}
                    provider={provider}
                    pending={pending === `provider:${provider.name}`}
                    controlsDisabled={controlsDisabled}
                    onToggle={(disabled) =>
                      void mutate(`provider:${provider.name}`, () =>
                        api.setProviderDisabled(provider.name, disabled),
                      )
                    }
                  />
                ))}
              </div>
            )}
          </Panel>

          <Panel>
            <PanelHeader title="Signal paths" meta={`${status.models.length} models`} />
            {status.models.length === 0 ? (
              <EmptyState title="No models available" detail="Configure at least one enabled route for a model." />
            ) : (
              status.models.map((model) => (
                <RouteChain
                  key={model.model_id}
                  model={model}
                  pending={pending === `model:${model.model_id}`}
                  controlsDisabled={controlsDisabled}
                  providers={status.providers.map((provider) => provider.name)}
                  onToggleLeg={(index, disabled) =>
                    void mutate(`model:${model.model_id}`, () =>
                      api.setRouteDisabled(model.model_id, index, disabled),
                    )
                  }
                  onMoveLeg={(index, direction) =>
                    void mutate(`model:${model.model_id}`, () =>
                      api.moveRoute(model.model_id, index, direction),
                    )
                  }
                  onAddLeg={(leg) =>
                    void mutate(`model:${model.model_id}`, () =>
                      api.addRouteLeg(model.model_id, leg),
                    )
                  }
                  onRemoveLeg={(index) =>
                    void mutate(`model:${model.model_id}`, () =>
                      api.removeRouteLeg(model.model_id, index),
                    )
                  }
                />
              ))
            )}
          </Panel>

          <LogPanel entries={logs} paused={paused} onPausedChange={setPaused} />
        </div>

        {status.skipped_routes.length > 0 && (
          <p className="mt-4 text-[11px] text-ink-dim">
            Skipped at build: <Ident>{status.skipped_routes.join(", ")}</Ident>
          </p>
        )}
      </main>
    </div>
  )
}

function appendLogs(current: LogEntry[], incoming: LogEntry[]): LogEntry[] {
  const bySequence = new Map(current.map((entry) => [entry.seq, entry]))
  for (const entry of incoming) {
    bySequence.set(entry.seq, entry)
  }

  return Array.from(bySequence.values()).sort((a, b) => a.seq - b.seq).slice(-LOG_CAP)
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

function formatUptime(seconds: number): string {
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`

  return `${Math.floor(seconds / 86400)}d ${Math.floor((seconds % 86400) / 3600)}h`
}
