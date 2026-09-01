import { useState } from "react";
import { ArrowDown, ArrowUp, Plus, X } from "lucide-react";

import type { ModelStatus, RouteLeg } from "@/lib/api";
import { BorderBeam } from "@/components/magic/border-beam";
import { Badge, Button, EmptyState, Ident } from "@/components/ui/primitives";
import { Switch } from "@/components/ui/switch";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

const UNSET = "__unset";

type NewLeg = {
  provider: string;
  model: string;
  reasoning_effort?: string;
  disabled?: boolean;
};

type RouteChainProps = {
  model: ModelStatus;
  pending: boolean;
  controlsDisabled: boolean;
  /** Provider names available for a new fallback leg. */
  providers: string[];
  onToggleLeg: (index: number, disabled: boolean) => void;
  onMoveLeg: (index: number, direction: "up" | "down") => void;
  onAddLeg: (leg: NewLeg) => void;
  onRemoveLeg: (index: number) => void;
};

export function RouteChain({
  model,
  pending,
  controlsDisabled,
  providers,
  onToggleLeg,
  onMoveLeg,
  onAddLeg,
  onRemoveLeg,
}: RouteChainProps) {
  const [adding, setAdding] = useState(false);

  return (
    <article className="border-b border-line px-4 py-4 last:border-b-0">
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <h3 className="ident text-sm font-semibold text-ink">
          {model.model_id}
        </h3>
        {model.serving ? (
          <Badge tone="live">serving</Badge>
        ) : (
          <Badge tone="dead">404 — no live leg</Badge>
        )}
        <Button
          variant="icon"
          aria-label={`Add provider leg to ${model.model_id}`}
          title="add provider leg"
          disabled={controlsDisabled || providers.length === 0}
          onClick={() => setAdding(true)}
        >
          <Plus className="size-3" />
        </Button>
      </div>

      <AddLegDialog
        open={adding}
        modelId={model.model_id}
        providers={providers}
        onClose={() => setAdding(false)}
        onSubmit={(leg) => {
          setAdding(false);
          onAddLeg(leg);
        }}
      />

      {model.chain.length === 0 ? (
        <EmptyState
          title="No route legs configured"
          detail={`Add a provider route for ${model.model_id}.`}
        />
      ) : (
        <ol className="flex flex-col gap-2 lg:grid lg:grid-cols-2 lg:gap-3 xl:grid-cols-3 2xl:grid-cols-4">
          {model.chain.map((leg, index) => (
            <RouteStep
              key={routeKey(model.chain, index)}
              leg={leg}
              index={index}
              last={index === model.chain.length - 1}
              pending={pending}
              controlsDisabled={controlsDisabled}
              modelId={model.model_id}
              chainLength={model.chain.length}
              onToggle={(disabled) => onToggleLeg(index, disabled)}
              onMove={(direction) => onMoveLeg(index, direction)}
              onRemove={() => onRemoveLeg(index)}
            />
          ))}
        </ol>
      )}
    </article>
  );
}

type AddLegDialogProps = {
  open: boolean;
  modelId: string;
  providers: string[];
  onClose: () => void;
  onSubmit: (leg: NewLeg) => void;
};

function AddLegDialog({
  open,
  modelId,
  providers,
  onClose,
  onSubmit,
}: AddLegDialogProps) {
  const [provider, setProvider] = useState("");
  const [upstream, setUpstream] = useState("");
  const [effort, setEffort] = useState(UNSET);
  const [disabled, setDisabled] = useState(false);

  const close = () => {
    setProvider("");
    setUpstream("");
    setEffort(UNSET);
    setDisabled(false);
    onClose();
  };

  const submit = () => {
    if (!provider || !upstream.trim()) return;
    onSubmit({
      provider,
      model: upstream.trim(),
      reasoning_effort: effort === UNSET ? undefined : effort,
      disabled: disabled || undefined,
    });
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) close();
      }}
    >
      <DialogContent
        aria-describedby={undefined}
        className="max-w-md gap-5 bg-panel text-ink"
      >
        <DialogHeader>
          <DialogTitle className="ident text-sm font-semibold">
            Add Signal Path
          </DialogTitle>
          <DialogDescription className="ident text-[11px] text-ink-dim">
            append a fallback leg to <span className="text-ink">{modelId}</span>
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <label
              htmlFor="add-leg-provider"
              className="text-[11px] text-ink-dim"
            >
              Provider
            </label>
            <Select value={provider || undefined} onValueChange={setProvider}>
              <SelectTrigger
                id="add-leg-provider"
                aria-label={`Provider for new ${modelId} leg`}
                className="ident w-full border-line-strong bg-inset text-xs text-ink data-[placeholder]:text-ink-ghost"
              >
                <SelectValue placeholder="select provider…" />
              </SelectTrigger>
              <SelectContent className="border-line bg-panel text-ink">
                {providers.map((name) => (
                  <SelectItem
                    key={name}
                    value={name}
                    className="ident text-xs focus:bg-raised"
                  >
                    {name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="flex flex-col gap-1.5">
            <label htmlFor="add-leg-model" className="text-[11px] text-ink-dim">
              Upstream model
            </label>
            <input
              id="add-leg-model"
              value={upstream}
              onChange={(event) => setUpstream(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  submit();
                }
              }}
              placeholder="upstream model…"
              className="ident w-full rounded-md border border-line-strong bg-inset px-3 py-2 text-xs text-ink placeholder:text-ink-ghost focus:border-live/50 focus:outline-2 focus:outline-offset-2 focus:outline-ring"
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <label className="text-[11px] text-ink-dim">Reasoning effort</label>
            <Select value={effort} onValueChange={setEffort}>
              <SelectTrigger
                aria-label={`Reasoning effort for new ${modelId} leg`}
                className="ident w-full border-line-strong bg-inset text-xs text-ink data-[placeholder]:text-ink-ghost"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent className="border-line bg-panel text-ink">
                <SelectItem
                  value={UNSET}
                  className="ident text-xs focus:bg-raised"
                >
                  (not set)
                </SelectItem>
                {["low", "medium", "high", "xhigh", "max", "ultra"].map(
                  (level) => (
                    <SelectItem
                      key={level}
                      value={level}
                      className="ident text-xs focus:bg-raised"
                    >
                      {level}
                    </SelectItem>
                  ),
                )}
              </SelectContent>
            </Select>
            <p className="text-[10px] text-ink-faint">
              written as{" "}
              <span className="ident">defaults.reasoning_effort</span> on the
              new leg
            </p>
          </div>

          <div className="flex items-center justify-between gap-3">
            <label
              htmlFor="add-leg-disabled"
              className="text-[11px] text-ink-dim"
            >
              Disabled
            </label>
            <Switch
              id="add-leg-disabled"
              checked={disabled}
              onCheckedChange={setDisabled}
              label={`Create the new ${modelId} leg disabled`}
              size="sm"
            />
          </div>
        </div>

        <DialogFooter className="gap-2">
          <Button variant="ghost" onClick={close}>
            Cancel
          </Button>
          <Button
            variant="solid"
            disabled={!provider || !upstream.trim()}
            onClick={submit}
          >
            Submit
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

type RouteStepProps = {
  leg: RouteLeg;
  index: number;
  last: boolean;
  pending: boolean;
  controlsDisabled: boolean;
  modelId: string;
  chainLength: number;
  onToggle: (disabled: boolean) => void;
  onMove: (direction: "up" | "down") => void;
  onRemove: () => void;
};

function RouteStep({
  leg,
  index,
  last,
  pending,
  controlsDisabled,
  modelId,
  chainLength,
  onToggle,
  onMove,
  onRemove,
}: RouteStepProps) {
  const disabled = leg.disabled || leg.provider_disabled;

  return (
    <li className="flex min-w-0 flex-col">
      <div
        className={cn(
          "relative flex min-w-0 flex-1 flex-col gap-2 rounded-lg border bg-inset px-3 py-2.5",
          leg.active && "border-live/45",
          !leg.active && !disabled && "border-line",
          disabled && "border-parked/30 opacity-60",
          pending && "opacity-70",
        )}
      >
        {leg.active && <BorderBeam duration={6} size={70} />}

        <div className="flex min-w-0 items-start gap-2">
          <span
            className={cn(
              "ident shrink-0 text-sm tabular-nums",
              leg.active ? "text-live" : "text-ink-dim",
            )}
          >
            {String(index + 1).padStart(2, "0")}
          </span>
          <div className="min-w-0 flex-1">
            <h4
              className="ident truncate text-xs font-semibold text-ink"
              title={leg.provider}
            >
              {leg.provider}
            </h4>
            <Ident
              className="block truncate text-[11px] text-ink-dim"
              title={leg.model}
            >
              {leg.model}
            </Ident>
          </div>
        </div>

        <div className="flex items-center justify-between gap-2">
          <div className="flex min-w-0 flex-wrap items-center gap-1">
            {leg.active && <Badge tone="live">primary</Badge>}
            {leg.disabled && <Badge tone="parked">disabled</Badge>}
            {leg.provider_disabled && !leg.disabled && (
              <Badge tone="parked">parked</Badge>
            )}
          </div>

          <div className="flex shrink-0 items-center gap-1">
            <Button
              variant="icon"
              aria-label={`Move ${leg.provider} earlier`}
              disabled={controlsDisabled || index === 0}
              onClick={() => onMove("up")}
            >
              <ArrowUp className="size-3" />
            </Button>
            <Button
              variant="icon"
              aria-label={`Move ${leg.provider} later`}
              disabled={controlsDisabled || index === chainLength - 1}
              onClick={() => onMove("down")}
            >
              <ArrowDown className="size-3" />
            </Button>
            <Button
              variant="icon"
              aria-label={`Remove ${leg.provider} leg`}
              title={
                chainLength === 1 ? "cannot remove the last leg" : "remove leg"
              }
              disabled={controlsDisabled || chainLength === 1}
              onClick={onRemove}
            >
              <X className="size-3" />
            </Button>
            <Switch
              checked={!disabled}
              disabled={controlsDisabled || leg.provider_disabled}
              onCheckedChange={(checked) => onToggle(!checked)}
              label={`Use ${leg.provider} for ${modelId}`}
              size="sm"
              className="ml-1"
            />
          </div>
        </div>
      </div>

      {!last && (
        <div
          className="flex h-5 items-center justify-center lg:hidden"
          aria-hidden
        >
          <div className="relative flex h-full w-full items-center justify-center">
            <span className="absolute h-full w-px bg-line-strong" />
            <ArrowDown className="relative size-3 bg-panel text-ink-faint" />
          </div>
        </div>
      )}
    </li>
  );
}

function routeKey(chain: RouteLeg[], index: number): string {
  const leg = chain[index];
  let occurrence = 0;
  for (let current = 0; current < index; current += 1) {
    if (
      chain[current].provider === leg.provider &&
      chain[current].model === leg.model
    ) {
      occurrence += 1;
    }
  }

  return `${leg.provider}\u0000${leg.model}\u0000${occurrence}`;
}
