import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { AlertTriangle, Check, Loader2, Search } from 'lucide-react';
import Modal from '../../components/common/Modal';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import { Spinner } from '../../components/ui/spinner';
import {
  applyTemplate,
  listTemplates,
  type SystemTemplateSummary,
  type TemplateApplyResult,
} from '../../lib/api';
import { usePermissions } from '../../lib/auth';
import KBQuickFillForm from './KBQuickFillForm';

// "Start from a template" — the OTHER half of the retired welcome wizard.
//
// The wizard's template packs were real: one click deployed a whole custom object
// plus the custom fields that go with it. What was wrong was the delivery — a
// blocking full-screen overlay, shown exactly once, on your very first minute in
// the product, when you had no idea what a custom object even was. Dismiss it and
// the packs became unreachable forever.
//
// Same functionality, opened on purpose: from the setup checklist or right after
// creating a workspace, in the shared Modal (Escape, focus trap, focus restore),
// any time you like.
//
// The catalog now comes from the SERVER (/api/templates). It previously lived here
// as a two-entry array whose payloads were hardcoded `if` branches inside a local
// deploy() — a shape that could only ever create objects and fields, and needed two
// edits per template. The server templates additionally install pipeline stages,
// knowledge-base content, an AI persona and starter automations, none of which this
// component could express.

type Step = 'templates' | 'result' | 'kb' | 'done';

interface StarterTemplateModalProps {
  open: boolean;
  onClose: () => void;
  /** Fired after a successful apply, so a host surface can tick its own state. */
  onApplied?: (result: TemplateApplyResult) => void;
  /**
   * Where this was opened from. 'creation' softens the copy for someone who has
   * just made a workspace and has nothing to lose by applying a template.
   */
  surface?: 'checklist' | 'creation';
}

/** Groups the catalog's free-form category strings into filter chips. */
function categoriesOf(templates: SystemTemplateSummary[]): string[] {
  return Array.from(new Set(templates.map(t => t.category).filter(Boolean))).sort();
}

export default function StarterTemplateModal({
  open,
  onClose,
  onApplied,
  surface = 'checklist',
}: StarterTemplateModalProps) {
  const qc = useQueryClient();
  const { can, loaded: permsLoaded } = usePermissions();
  // Apply is gated on org.settings server-side; matching it here keeps us from
  // showing a button whose every click would 403.
  const canApply = can('org.settings');
  const canKB = can('knowledge.manage');

  const [step, setStep] = useState<Step>('templates');
  const [query, setQuery] = useState('');
  const [category, setCategory] = useState<string | null>(null);
  const [applied, setApplied] = useState<TemplateApplyResult | null>(null);
  const [appliedName, setAppliedName] = useState<string>('');

  const { data: templates = [], isLoading, isError } = useQuery<SystemTemplateSummary[]>({
    queryKey: ['templates'],
    queryFn: listTemplates,
    enabled: open,
  });

  const mutation = useMutation({
    mutationFn: applyTemplate,
    onSuccess: result => {
      setApplied(result);
      // A template installs objects, fields, stages, KB and workflows — every
      // cached view of those is now stale.
      for (const key of [
        ['sidebar-objects'], ['registry-objects'], ['field-defs'],
        ['pipeline-stages'], ['knowledge-base'], ['workflows'], ['templates'],
      ]) {
        qc.invalidateQueries({ queryKey: key });
      }
      onApplied?.(result);
      setStep('result');
    },
  });

  const busy = mutation.isPending;

  const close = () => {
    if (busy) return; // don't strand a half-applied template
    onClose();
  };

  const categories = useMemo(() => categoriesOf(templates), [templates]);

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    return templates.filter(t => {
      if (category && t.category !== category) return false;
      if (!q) return true;
      return (
        t.name.toLowerCase().includes(q) ||
        t.description.toLowerCase().includes(q) ||
        t.category.toLowerCase().includes(q)
      );
    });
  }, [templates, query, category]);

  const runApply = (t: SystemTemplateSummary) => {
    setAppliedName(t.name);
    mutation.mutate(t.slug);
  };

  const title =
    step === 'templates'
      ? 'Start from a template'
      : step === 'result'
        ? `${appliedName} applied`
        : step === 'kb'
          ? 'Train your AI assistant'
          : 'All set';

  return (
    <Modal open={open} onClose={close} title={title} size="xl" dismissable={!busy}>
      {step === 'templates' && (
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">
            {surface === 'creation'
              ? 'Pick the closest match to your business and we will set up your pipeline, fields and knowledge base. Everything it creates is ordinary configuration — rename, extend or delete any of it afterwards.'
              : 'A template sets up the pipeline, fields, objects and knowledge base for your industry. Everything it creates is ordinary configuration — rename, extend or delete any of it afterwards.'}
          </p>

          {permsLoaded && !canApply && (
            <div className="rounded-lg border border-amber-500/20 bg-amber-500/10 px-3 py-2 text-sm text-amber-700 dark:text-amber-400">
              You need the workspace-settings permission to apply a template. Ask an admin to run this for you.
            </div>
          )}

          {mutation.isError && (
            <div className="rounded-lg border border-destructive/20 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {mutation.error instanceof Error ? mutation.error.message : 'Failed to apply the template.'}
            </div>
          )}

          {isError && (
            <div className="rounded-lg border border-destructive/20 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              Could not load the template catalog. Close this and try again.
            </div>
          )}

          {isLoading ? (
            <div className="flex justify-center py-10"><Spinner /></div>
          ) : (
            <>
              {/* 25 cards needs finding; 2 did not. */}
              <div className="flex flex-wrap items-center gap-2">
                <div className="relative min-w-[200px] flex-1">
                  <Search aria-hidden className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    value={query}
                    onChange={e => setQuery(e.target.value)}
                    placeholder="Search templates…"
                    aria-label="Search templates"
                    className="pl-8"
                  />
                </div>
                <div className="flex flex-wrap gap-1.5">
                  <button
                    type="button"
                    onClick={() => setCategory(null)}
                    className={`rounded-full border px-2.5 py-1 text-xs font-medium transition-colors ${
                      category === null
                        ? 'border-primary bg-primary/10 text-primary'
                        : 'border-border text-muted-foreground hover:bg-accent'
                    }`}
                  >
                    All
                  </button>
                  {categories.map(c => (
                    <button
                      key={c}
                      type="button"
                      onClick={() => setCategory(c === category ? null : c)}
                      className={`rounded-full border px-2.5 py-1 text-xs font-medium capitalize transition-colors ${
                        c === category
                          ? 'border-primary bg-primary/10 text-primary'
                          : 'border-border text-muted-foreground hover:bg-accent'
                      }`}
                    >
                      {c}
                    </button>
                  ))}
                </div>
              </div>

              <div className="grid max-h-[52vh] gap-3 overflow-y-auto pr-1 sm:grid-cols-2">
                {visible.map(t => (
                  <button
                    key={t.slug}
                    onClick={() => runApply(t)}
                    disabled={busy || !canApply}
                    className="flex flex-col rounded-xl border border-border p-4 text-left transition-colors hover:border-primary hover:bg-accent/40 disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    <span className="mb-2 flex items-center gap-2">
                      <span aria-hidden className="text-xl leading-none">{t.icon}</span>
                      <span className="font-semibold text-foreground">{t.name}</span>
                      {t.applied && (
                        <span className="inline-flex items-center gap-1 rounded-full bg-emerald-500/10 px-2 py-0.5 text-[11px] font-medium text-emerald-600 dark:text-emerald-400">
                          <Check aria-hidden className="h-3 w-3" /> Applied
                        </span>
                      )}
                    </span>
                    <span className="flex-1 text-sm text-muted-foreground">{t.description}</span>
                    <span className="mt-3 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
                      <span>{t.stage_count} stages</span>
                      <span>{t.field_count} fields</span>
                      {t.object_count > 0 && (
                        <span>{t.object_count} object{t.object_count === 1 ? '' : 's'}</span>
                      )}
                      {t.workflow_count > 0 && (
                        <span>{t.workflow_count} automation{t.workflow_count === 1 ? '' : 's'}</span>
                      )}
                    </span>
                    <span className="mt-3 inline-flex items-center gap-1.5 text-sm font-medium text-primary">
                      {busy && mutation.variables === t.slug ? (
                        <><Loader2 aria-hidden className="h-4 w-4 animate-spin" /> Applying…</>
                      ) : t.applied ? 'Apply again' : 'Apply template'}
                    </span>
                  </button>
                ))}
                {visible.length === 0 && (
                  <p className="col-span-full py-8 text-center text-sm text-muted-foreground">
                    No template matches “{query}”. Try a broader search, or build your own in Settings → Objects.
                  </p>
                )}
              </div>
            </>
          )}

          <div className="flex items-center justify-between gap-3 border-t border-border pt-4">
            <p className="text-sm text-muted-foreground">Prefer to build your own?</p>
            <Button variant="outline" onClick={() => (canKB ? setStep('kb') : close())} disabled={busy}>
              {canKB ? 'Skip — train the AI instead' : 'Not now'}
            </Button>
          </div>
        </div>
      )}

      {step === 'result' && applied && (
        <ApplyReport
          result={applied}
          canKB={canKB}
          onContinue={() => setStep(canKB ? 'kb' : 'done')}
        />
      )}

      {step === 'kb' && (
        <KBQuickFillForm
          templateId={applied?.template_slug ?? null}
          onSaved={() => setStep('done')}
          onSkip={close}
          skipLabel={applied ? 'Finish' : 'Skip for now'}
        />
      )}

      {step === 'done' && (
        <div className="space-y-4">
          <div className="rounded-lg border border-emerald-500/20 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-600 dark:text-emerald-400">
            {applied ? 'Your workspace is set up — everything is in the sidebar.' : 'Saved to your knowledge base.'}
          </div>
          <p className="text-sm text-muted-foreground">
            Fine-tune anything in{' '}
            <Link to="/settings/objects" onClick={close} className="text-primary underline">Settings → Objects</Link>
            {canKB && (
              <>{' '}or{' '}
                <Link to="/settings/knowledge" onClick={close} className="text-primary underline">Knowledge Base</Link>
              </>
            )}
            .
          </p>
          <div className="flex justify-end border-t border-border pt-4">
            <Button onClick={close}>Done</Button>
          </div>
        </div>
      )}
    </Modal>
  );
}

/**
 * The per-item report. Skips are the NORMAL case on an established workspace —
 * every collision is a deliberate no-overwrite — so they are stated plainly rather
 * than dressed up as problems. Automations left switched off get their own callout,
 * because "we created it but did not turn it on" is the one outcome a user would
 * otherwise discover by accident.
 */
function ApplyReport({
  result,
  canKB,
  onContinue,
}: {
  result: TemplateApplyResult;
  canKB: boolean;
  onContinue: () => void;
}) {
  const created = result.items.filter(i => i.status === 'created');
  const skipped = result.items.filter(i => i.status === 'skipped');
  const failed = result.items.filter(i => i.status === 'failed');
  const review = result.items.filter(i => i.status === 'needs_review');

  if (result.status === 'already_applied') {
    return (
      <div className="space-y-4">
        <div className="rounded-lg border border-border bg-muted/40 px-3 py-2 text-sm text-muted-foreground">
          You have already applied this template to this workspace, so nothing was changed.
        </div>
        <div className="flex justify-end border-t border-border pt-4">
          <Button onClick={onContinue}>{canKB ? 'Continue' : 'Done'}</Button>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="rounded-lg border border-emerald-500/20 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-600 dark:text-emerald-400">
        {created.length} item{created.length === 1 ? '' : 's'} added to your workspace.
      </div>

      <dl className="grid grid-cols-3 gap-3 text-center">
        {[
          ['Added', created.length],
          ['Already there', skipped.length],
          ['Needs a look', review.length + failed.length],
        ].map(([label, n]) => (
          <div key={label as string} className="rounded-lg border border-border px-3 py-2">
            <dt className="text-[11px] uppercase tracking-wide text-muted-foreground">{label}</dt>
            <dd className="text-lg font-semibold text-foreground">{n as number}</dd>
          </div>
        ))}
      </dl>

      {skipped.length > 0 && (
        <p className="text-sm text-muted-foreground">
          {skipped.length} item{skipped.length === 1 ? ' was' : 's were'} left alone because you already had
          {skipped.length === 1 ? ' it' : ' them'} — a template never overwrites your own configuration.
        </p>
      )}

      {review.length > 0 && (
        <div className="rounded-lg border border-amber-500/20 bg-amber-500/10 px-3 py-2 text-sm text-amber-700 dark:text-amber-400">
          <p className="flex items-center gap-1.5 font-medium">
            <AlertTriangle aria-hidden className="h-4 w-4" />
            {review.length} automation{review.length === 1 ? '' : 's'} created but switched off
          </p>
          <p className="mt-1">
            Anything that could message people outside your CRM stays off until you have read it. Review them in
            Automations and switch on what you want.
          </p>
        </div>
      )}

      {failed.length > 0 && (
        <div className="rounded-lg border border-destructive/20 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          <p className="font-medium">{failed.length} item{failed.length === 1 ? '' : 's'} could not be created</p>
          <ul className="mt-1 list-inside list-disc">
            {failed.map(f => (
              <li key={f.kind + f.key}>
                {f.key}
                {f.error ? ` — ${f.error}` : ''}
              </li>
            ))}
          </ul>
        </div>
      )}

      {(result.warnings ?? []).map(w => (
        <div key={w} className="rounded-lg border border-amber-500/20 bg-amber-500/10 px-3 py-2 text-sm text-amber-700 dark:text-amber-400">
          {w}
        </div>
      ))}

      <div className="flex justify-end border-t border-border pt-4">
        <Button onClick={onContinue}>{canKB ? 'Continue' : 'Done'}</Button>
      </div>
    </div>
  );
}
