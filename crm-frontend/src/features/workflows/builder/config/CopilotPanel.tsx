// AI copilot panel (A7.3): describe an automation in plain language, and the draft
// is applied to the canvas for review. Calls POST /api/workflows/ai/draft (never
// saves) and hands the returned draft to the store; a Keep/Undo banner (rendered by
// the builder) then commits or reverts it. The canvas is the preview.
import { useState, useEffect, useRef } from 'react';
import { Sparkles, Loader2, AlertTriangle, CheckCircle2 } from 'lucide-react';
import { useDraftWorkflow } from '../../queries';
import { useBuilderStore } from '../../store';
import { localDraftFromPrompt } from '../localDraft';
import type { WorkflowEditContext } from '../../api';

/** Snapshot the workflow currently on the canvas so the copilot edits it in place
 *  (A7.4) instead of drafting from scratch. Returns null for an empty/new workflow. */
function currentWorkflowContext(): WorkflowEditContext | null {
  const s = useBuilderStore.getState();
  if (!s.trigger && s.steps.length === 0) return null;
  return { name: s.name, trigger: s.trigger, conditions: s.conditions, steps: s.steps };
}

const EXAMPLES = [
  'When a deal moves to Won, notify the owner and create a follow-up task',
  'When a contact is created, wait 2 days, then email them a welcome',
  'When a deal stalls, log a note and assign it to a manager',
];

/** initialPrompt seeds the box and auto-generates once — used by the AI Command
 *  Center's create_workflow/update_workflow handoff (A7.4), which navigates here with
 *  the prompt in the `?ai=` query param. */
export function CopilotPanel({ initialPrompt = '' }: { initialPrompt?: string }) {
  const [prompt, setPrompt] = useState(initialPrompt);
  const applyDraft = useBuilderStore((s) => s.applyDraft);
  // The result banners describe the pending draft, so they're only meaningful
  // while one is awaiting Keep/Undo — once committed/reverted (draftSnapshot null)
  // they'd point at a banner that's gone.
  const draftPending = useBuilderStore((s) => s.draftSnapshot !== null);
  const draft = useDraftWorkflow();
  // True when the AI couldn't be reached and we applied the offline heuristic draft.
  // We DON'T hide this — the note tells the user it's an offline draft and to retry —
  // but the real AI draft is always what we try for first (with a retry).
  const [usedFallback, setUsedFallback] = useState(false);

  // Never leave the user at a dead end. draftWorkflow already retries the real AI once
  // on a transient failure; if it still can't be reached, build a starting draft from
  // their text on the client so they can keep moving, and say so honestly.
  const runDraft = (p: string) => {
    setUsedFallback(false);
    draft.mutate(
      { prompt: p, current: currentWorkflowContext() },
      {
        onSuccess: (res) => applyDraft(res.draft),
        onError: (err) => {
          console.warn('[copilot] AI draft unreachable — applied an offline fallback draft:', err);
          applyDraft(localDraftFromPrompt(p, useBuilderStore.getState().schema));
          setUsedFallback(true);
        },
      },
    );
  };

  const generate = () => {
    const p = prompt.trim();
    if (!p) return;
    runDraft(p);
  };

  // Auto-draft once when handed a prompt from the Command Center (A7.4). Guarded by a
  // ref so a re-render can't re-fire it; NextBuilder clears the prompt after the first
  // hydrate so a save→re-hydrate remount doesn't re-fire.
  const autoRanRef = useRef(false);
  useEffect(() => {
    if (autoRanRef.current) return;
    const p = initialPrompt.trim();
    if (!p) return;
    autoRanRef.current = true;
    runDraft(p);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialPrompt]);

  const onPromptChange = (value: string) => {
    setPrompt(value);
    // Drop a stale success/error banner as soon as the user edits the prompt.
    if (draft.isError || draft.isSuccess) draft.reset();
    if (usedFallback) setUsedFallback(false);
  };

  const validation = draft.data?.validation;
  const issues = validation?.errors ?? [];
  const hasIssues = validation?.valid === false && issues.length > 0;

  return (
    <div className="space-y-3 p-4">
      <div className="flex items-center gap-2">
        <Sparkles className="h-4 w-4 text-purple-500" />
        <h3 className="text-sm font-semibold text-foreground">Copilot</h3>
      </div>
      <p className="text-xs text-muted-foreground">
        Describe the automation in plain language. The draft appears on the canvas — review it, tweak anything, then Save.
      </p>

      <textarea
        value={prompt}
        onChange={(e) => onPromptChange(e.target.value)}
        rows={5}
        placeholder="e.g. When a deal moves to Won, notify the owner and create a follow-up task"
        className="w-full resize-y rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground focus:border-ring focus:outline-none focus:ring-1 focus:ring-ring"
      />

      <div className="flex flex-wrap gap-1.5">
        {EXAMPLES.map((ex) => (
          <button
            key={ex}
            type="button"
            onClick={() => setPrompt(ex)}
            className="rounded-full border border-border bg-muted/40 px-2.5 py-1 text-left text-[11px] text-muted-foreground hover:border-primary/40 hover:text-foreground"
          >
            {ex}
          </button>
        ))}
      </div>

      <button
        type="button"
        onClick={generate}
        disabled={draft.isPending || !prompt.trim()}
        className="flex w-full items-center justify-center gap-1.5 rounded-lg bg-primary px-3 py-2 text-sm font-medium text-primary-foreground disabled:opacity-50"
      >
        {draft.isPending ? (
          <><Loader2 className="h-4 w-4 animate-spin" /> Drafting…</>
        ) : (
          <><Sparkles className="h-4 w-4" /> Generate draft</>
        )}
      </button>

      {/* Real AI draft applied. */}
      {draftPending && !usedFallback && !hasIssues && (
        <div className="flex items-start gap-2 rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs text-emerald-700 dark:text-emerald-400">
          <CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <span>Draft applied to the canvas. Review it, then Keep or Undo above.</span>
        </div>
      )}

      {/* AI unreachable → offline draft. Honest but low-key (a muted line, not an alarm):
          the user knows it's a fallback and can retry the real AI, without the copilot
          reading as broken. */}
      {draftPending && usedFallback && (
        <p className="px-1 text-[11px] leading-relaxed text-muted-foreground">
          Offline draft — the AI wasn't reachable just now. Review it on the canvas, or press{' '}
          <span className="font-medium text-foreground">Generate draft</span> to try the AI again.
        </p>
      )}

      {draftPending && hasIssues && (
        <div className="space-y-1 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-400">
          <div className="flex items-center gap-1.5 font-medium">
            <AlertTriangle className="h-3.5 w-3.5" /> Draft applied, but review these on the canvas:
          </div>
          <ul className="ml-5 list-disc space-y-0.5">
            {issues.slice(0, 6).map((e, i) => (
              <li key={i}>{e.message}</li>
            ))}
            {issues.length > 6 && (
              <li className="list-none text-amber-600/80 dark:text-amber-400/70">+{issues.length - 6} more…</li>
            )}
          </ul>
        </div>
      )}

      {/* Genuine dead end: only if we couldn't put ANY draft on the canvas (the local
          fallback normally guarantees one, so this is a last resort). */}
      {draft.isError && !draftPending && (
        <div className="flex items-start gap-2 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <span>{draft.error instanceof Error ? draft.error.message : 'Could not draft a workflow.'}</span>
        </div>
      )}
    </div>
  );
}
