import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Sparkles, Wand2, AlertTriangle } from 'lucide-react';
import Modal from '../../../components/common/Modal';
import { Button, Textarea } from '@/components/ui';
import { draftEmailAI, type AIDraftResult } from '../contentApi';
import { cloneWithNewIds } from './blockUtils';
import { useBuilderStore } from './builderStore';
import type { Block } from './blocks';

export type AIMode = 'email' | 'section' | 'rewrite';

interface Props {
  open: boolean;
  mode: AIMode;
  onClose: () => void;
}

/** Starter briefs per mode. Competitors ship prompt chips because a blank prompt
 *  box is the same blank-page problem as a blank canvas — these are shaped like
 *  real marketing jobs, not "write something". */
const STARTERS: Record<AIMode, string[]> = {
  email: [
    'A welcome email for new customers',
    'Announce a new feature and invite people to try it',
    'A short monthly update with two highlights',
    'Win back customers who haven’t bought in a while',
  ],
  section: [
    'A closing call to action',
    'Three reasons to upgrade, side by side',
    'A customer testimonial',
    'A short intro paragraph',
  ],
  rewrite: [
    'Make it shorter',
    'Make it warmer and more personal',
    'Fix the grammar and tighten it',
    'Rewrite it for a first-time customer',
  ],
};

const TITLES: Record<AIMode, string> = {
  email: 'Write this email with AI',
  section: 'Add a section with AI',
  rewrite: 'Rewrite this block with AI',
};

/** AICopilotModal is the builder's AI surface. It never saves and never edits in
 *  place silently: the draft is shown, and applying it is a single undoable step,
 *  so the canvas stays the review gate (the same contract as the automation
 *  copilot). What it writes is real blocks — editable like anything else — not an
 *  HTML blob, which is the failure mode practitioners call fatal in AI builders. */
export const AICopilotModal: React.FC<Props> = ({ open, mode, onClose }) => {
  const blocks = useBuilderStore((s) => s.blocks);
  const scope = useBuilderStore((s) => s.scope);
  const subject = useBuilderStore((s) => s.subject);
  const selectedId = useBuilderStore((s) => s.selectedId);
  const applyAIEmail = useBuilderStore((s) => s.applyAIEmail);
  const insertBlocks = useBuilderStore((s) => s.insertBlocks);
  const patchBlock = useBuilderStore((s) => s.patchBlock);

  const [prompt, setPrompt] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // A result is stored WITH the request that produced it: the mode and the block
  // it targeted. A draft that outlives its modal opening (the user closed and
  // reopened in another mode while it was in flight) must never be applied under
  // the new one.
  const [result, setResult] = useState<{ draft: AIDraftResult; mode: AIMode; targetId: string | null } | null>(null);
  const requestSeq = useRef(0);

  // Each opening is a fresh ask — a stale draft from last time must never be
  // one click from replacing the document.
  useEffect(() => {
    // Bumping the sequence orphans any in-flight request, so its late response
    // is discarded rather than landing in a fresh dialog.
    requestSeq.current += 1;
    setPrompt('');
    setError(null);
    setResult(null);
    setBusy(false);
  }, [open, mode]);

  const selected = useMemo(
    () => (selectedId ? findAnywhere(blocks, selectedId) : null),
    [blocks, selectedId],
  );

  const generate = async () => {
    const brief = prompt.trim();
    if (!brief || busy) return;
    // The target is pinned at REQUEST time: a rewrite must apply to the block the
    // user was looking at, even if the selection moves while the model thinks.
    const targetId = mode === 'rewrite' ? selectedId : null;
    const targetBlock = mode === 'rewrite' ? selected : null;
    const seq = ++requestSeq.current;
    setBusy(true);
    setError(null);
    setResult(null);
    try {
      const res = await draftEmailAI({
        prompt: brief,
        mode,
        merge_scope: scope,
        // Context: the email so far (so a new section matches its tone), or the
        // single block being rewritten.
        current_doc: mode === 'rewrite' ? undefined : blocks,
        block: mode === 'rewrite' ? targetBlock ?? undefined : undefined,
        subject: mode === 'email' ? subject : undefined,
      });
      if (seq !== requestSeq.current) return; // superseded — drop it silently
      if (!res.blocks?.length) {
        setError('The copilot didn’t return anything usable — try describing it differently.');
        return;
      }
      setResult({ draft: res, mode, targetId });
    } catch (e) {
      if (seq !== requestSeq.current) return;
      setError((e as Error).message || 'The copilot could not write that.');
    } finally {
      if (seq === requestSeq.current) setBusy(false);
    }
  };

  const apply = () => {
    if (!result?.draft.blocks?.length) return;
    // Apply under the request's OWN mode and target, never the current ones.
    const { draft, mode: resultMode, targetId } = result;
    // Fresh client ids: server ids are per-response and would collide with
    // anything already on the canvas.
    const fresh = draft.blocks.map(cloneWithNewIds);
    if (resultMode === 'email') {
      applyAIEmail(fresh, draft.subject, draft.preheader);
    } else if (resultMode === 'rewrite') {
      const store = useBuilderStore.getState();
      // The block may have been deleted while the model was working. Falling
      // through to an insert would silently do something the user never asked
      // for, so say so instead.
      if (!targetId || !findAnywhere(store.blocks, targetId)) {
        setError('That block is no longer on the canvas, so there was nothing to rewrite.');
        setResult(null);
        return;
      }
      // Patch ONLY the fields the copilot actually returned: writing undefined
      // over a field the model omitted would erase the author's own copy.
      const w = fresh[0];
      const patch: Partial<Block> = {};
      if (w.text !== undefined) patch.text = w.text;
      if (w.label !== undefined) patch.label = w.label;
      if (w.title !== undefined) patch.title = w.title;
      if (w.alt !== undefined) patch.alt = w.alt;
      if (Object.keys(patch).length === 0) {
        setError('The copilot returned nothing to apply — try again.');
        return;
      }
      patchBlock(targetId, patch);
    } else {
      const s = useBuilderStore.getState();
      const at = s.selectedId ? indexAfter(s.blocks, s.selectedId) : s.blocks.length;
      insertBlocks(fresh, { parentId: null, colIndex: 0, index: at });
    }
    onClose();
  };

  const canRewrite = mode !== 'rewrite' || !!selected;

  return (
    <Modal open={open} onClose={onClose} title={TITLES[mode]} size="md">
      <div className="space-y-3">
        {/* A draft takes seconds — announce start, finish and failure rather than
            leaving assistive tech in silence. Kept OUTSIDE the branch below so an
            error still shows if the selected block disappeared mid-request. */}
        <div role="status" aria-live="polite" className="sr-only">
          {busy ? 'Writing your draft…' : result ? `Draft ready — ${result.draft.blocks.length} blocks` : error ?? ''}
        </div>

        {error && (
          <div className="rounded-lg border border-destructive/30 bg-destructive/10 p-2.5 text-xs text-destructive">
            {error}
          </div>
        )}

        {!canRewrite ? (
          <p className="text-sm text-muted-foreground">
            {error ? 'Pick another block to rewrite.' : 'Select a block on the canvas first.'}
          </p>
        ) : (
          <>
            <div>
              <label htmlFor="ai-brief" className="mb-1 block text-xs font-medium text-muted-foreground">
                {mode === 'rewrite' ? 'How should this block change?' : 'What should it say?'}
              </label>
              <Textarea
                id="ai-brief"
                value={prompt}
                onChange={(e) => setPrompt(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) generate(); }}
                rows={3}
                maxLength={2000}
                placeholder={
                  mode === 'rewrite'
                    ? 'e.g. shorter, and mention the free trial'
                    : 'e.g. announce our new reporting feature to existing customers, friendly tone'
                }
              />
            </div>

            <div className="flex flex-wrap gap-1.5">
              {STARTERS[mode].map((s) => (
                <button
                  key={s}
                  type="button"
                  onClick={() => setPrompt(s)}
                  className="rounded-full border border-border px-2.5 py-1 text-xs text-muted-foreground transition-colors hover:border-ring hover:text-foreground"
                >
                  {s}
                </button>
              ))}
            </div>

            <p className="text-[11px] text-muted-foreground">
              The copilot writes with your CRM’s merge fields (it knows the ones this
              email is allowed to use) and always adds fallback text. It never writes
              links or picks images — you stay in charge of those.
            </p>

            {result && (
              <div className="rounded-lg border border-border bg-muted/40 p-3">
                <p className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  Draft — {result.draft.blocks.length} block{result.draft.blocks.length === 1 ? '' : 's'}
                </p>
                {result.draft.subject && (
                  <p className="mb-1 text-xs text-muted-foreground">
                    Subject: <span className="font-medium text-foreground">{result.draft.subject}</span>
                  </p>
                )}
                <ul className="max-h-48 space-y-1 overflow-y-auto text-xs text-foreground">
                  {result.draft.blocks.map((b, i) => (
                    <li key={i} className="flex gap-2">
                      <span className="shrink-0 font-mono text-[10px] uppercase text-muted-foreground">{b.type}</span>
                      <span className="min-w-0 truncate">{summarize(b)}</span>
                    </li>
                  ))}
                </ul>
                {result.draft.repairs && result.draft.repairs.length > 0 && (
                  <div className="mt-2 flex gap-1.5 rounded border border-amber-500/30 bg-amber-500/10 p-2 text-[11px] text-amber-700 dark:text-amber-400">
                    <AlertTriangle className="mt-px h-3.5 w-3.5 shrink-0" />
                    <span>
                      Adjusted to fit this email: {result.draft.repairs.join('; ')}.
                    </span>
                  </div>
                )}
                <p className="mt-2 text-[11px] text-muted-foreground">
                  {mode === 'email'
                    ? 'Applying replaces the current design — one Ctrl+Z brings it back.'
                    : mode === 'rewrite'
                      ? 'Applying replaces this block’s words and keeps its styling — one Ctrl+Z brings the original back.'
                      : 'Applying adds this to the canvas — one Ctrl+Z removes it.'}
                </p>
              </div>
            )}
          </>
        )}

        <div className="flex justify-end gap-2 pt-1">
          <Button variant="outline" size="sm" onClick={onClose}>Cancel</Button>
          {result ? (
            <>
              <Button variant="outline" size="sm" onClick={generate} disabled={busy}>
                <Wand2 className="h-4 w-4" /> Try again
              </Button>
              <Button size="sm" onClick={apply}>
                {result.mode === 'email' ? 'Use this email' : result.mode === 'rewrite' ? 'Use this rewrite' : 'Add to email'}
              </Button>
            </>
          ) : (
            <Button size="sm" onClick={generate} disabled={busy || !prompt.trim() || !canRewrite} aria-busy={busy}>
              <Sparkles className="h-4 w-4" />
              {busy ? 'Writing…' : 'Generate'}
            </Button>
          )}
        </div>
      </div>
    </Modal>
  );
};

/** summarize renders a one-line preview of a block for the draft list. */
function summarize(b: Block): string {
  const text = (b.text ?? '').replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ').trim();
  if (text) return text;
  if (b.label) return b.label;
  if (b.alt) return `[${b.alt}]`;
  if (b.type === 'columns') return `${b.columns?.length ?? 0} columns`;
  return '—';
}

/** findAnywhere locates a block at the root or one level into columns. */
function findAnywhere(blocks: Block[], id: string): Block | null {
  for (const b of blocks) {
    if (b.id === id) return b;
    for (const col of b.columns ?? []) {
      for (const sub of col) if (sub.id === id) return sub;
    }
  }
  return null;
}

/** indexAfter returns the root insert position just after a selection (a nested
 *  selection resolves to the slot after its enclosing columns block). */
function indexAfter(blocks: Block[], id: string): number {
  const root = blocks.findIndex((b) => b.id === id);
  if (root >= 0) return root + 1;
  const parent = blocks.findIndex((b) => (b.columns ?? []).some((col) => col.some((s) => s.id === id)));
  return parent >= 0 ? parent + 1 : blocks.length;
}

export default AICopilotModal;
