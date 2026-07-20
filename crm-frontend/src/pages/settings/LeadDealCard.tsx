import { useEffect, useMemo, useState } from 'react';
import { AlertTriangle } from 'lucide-react';
import { Badge, Button, Input, Select } from '@/components/ui';
import { getStages, type PipelineStage } from '../../lib/api';
import { useUpdateSource } from '../../features/integrations/queries';
import {
  DEAL_NAME_TOKENS,
  DEFAULT_DEAL_NAME_TEMPLATE,
  type LeadSource,
} from '../../features/integrations/types';

// Whether a lead from this source is also an opportunity.
//
// The copy here carries as much weight as the controls. Two facts about this
// feature are surprising if you only read the checkbox, and both are stated in the
// UI rather than left for someone to discover from a pipeline that does not fill
// up: a deal is created ONLY for a new contact, and a test lead never makes one.

interface Props {
  source: LeadSource;
}

/** Renders a template the way the server will, so the preview cannot lie about the
 *  blank-token rule. Mirrors renderDealName + collapseSpaces in the Go package. */
function preview(template: string, sample: Record<string, string>): string {
  const rendered = template.replace(/\{\{\s*([\w]+)\s*\}\}/g, (_, key: string) => sample[key] ?? '');
  return rendered.split(/\s+/).filter(Boolean).join(' ').replace(/^[-–—,|·:\s]+|[-–—,|·:\s]+$/g, '');
}

export default function LeadDealCard({ source }: Props) {
  const updateSource = useUpdateSource();
  const deal = source.config?.deal;

  const [stages, setStages] = useState<PipelineStage[]>([]);
  const [stagesFailed, setStagesFailed] = useState(false);
  const [enabled, setEnabled] = useState(Boolean(deal?.enabled));
  const [stageId, setStageId] = useState(deal?.stage_id ?? '');
  const [template, setTemplate] = useState(deal?.name_template ?? DEFAULT_DEAL_NAME_TEMPLATE);
  const [error, setError] = useState('');

  useEffect(() => {
    setEnabled(Boolean(deal?.enabled));
    setStageId(deal?.stage_id ?? '');
    setTemplate(deal?.name_template ?? DEFAULT_DEAL_NAME_TEMPLATE);
  }, [source.id, deal?.enabled, deal?.stage_id, deal?.name_template]);

  useEffect(() => {
    let cancelled = false;
    getStages()
      .then((s) => {
        if (cancelled) return;
        setStages(Array.isArray(s) ? s : []);
      })
      .catch(() => { if (!cancelled) setStagesFailed(true); });
    return () => { cancelled = true; };
  }, []);

  // Won/lost stages are not offered, matching the server's refusal: deal creation
  // does not derive is_won/is_lost, so a deal started in "Closed Won" would sit in
  // the won column reporting the opposite.
  const openStages = useMemo(() => stages.filter((s) => !s.is_won && !s.is_lost), [stages]);

  const previewName = preview(template || DEFAULT_DEAL_NAME_TEMPLATE, {
    full_name: 'Ada Lovelace',
    first_name: 'Ada',
    last_name: 'Lovelace',
    email: 'ada@example.com',
    company: 'Analytical Engines',
    source_name: source.name,
    date: new Date().toISOString().slice(0, 10),
  });

  const dirty =
    enabled !== Boolean(deal?.enabled) ||
    stageId !== (deal?.stage_id ?? '') ||
    template !== (deal?.name_template ?? DEFAULT_DEAL_NAME_TEMPLATE);

  const save = async (next: { enabled: boolean; stage_id?: string; name_template?: string }) => {
    setError('');
    try {
      await updateSource.mutateAsync({ id: source.id, input: { deal: next } });
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save');
    }
  };

  const toggle = async (on: boolean) => {
    setEnabled(on);
    // Turning it OFF is immediate — nothing else is required to make it valid.
    // Turning it ON needs a stage, so it waits for an explicit Save.
    if (!on) {
      await save({ enabled: false, stage_id: stageId || undefined, name_template: template });
    }
  };

  return (
    <div className="rounded-xl border border-border p-4 space-y-4">
      <div>
        <h3 className="text-sm font-medium text-foreground">Also create a deal</h3>
        <p className="text-xs text-muted-foreground mt-0.5">
          Turn leads from this source into opportunities, linked to the contact and carrying the
          same campaign attribution — so revenue reports can tell you what this channel earned.
        </p>
      </div>

      <label className="flex items-start gap-2.5 cursor-pointer">
        <input
          type="checkbox"
          className="mt-0.5"
          checked={enabled}
          onChange={(e) => void toggle(e.target.checked)}
          disabled={updateSource.isPending}
        />
        <span>
          <span className="text-sm text-foreground">Open a deal for each new lead</span>
          <span className="block text-xs text-muted-foreground mt-0.5">
            Only when the lead is someone new. If they are already in your CRM the submission is
            matched and logged, and no second deal is opened — you will see that on the delivery.
            To open one every time instead, use a workflow on “contact created”.
          </span>
        </span>
      </label>

      {enabled && (
        <div className="space-y-4 border-t border-border pt-4">
          {source.deal_stage_missing && (
            <div className="flex items-start gap-2 rounded-lg border border-warning/40 bg-warning/10 p-3 text-xs">
              <AlertTriangle className="h-4 w-4 shrink-0 text-warning" aria-hidden />
              <span className="text-foreground">
                The stage this source was set to has been deleted. New deals are going to your
                first stage instead — pick one below to make that deliberate.
              </span>
            </div>
          )}

          <div className="space-y-1.5">
            <label className="text-xs text-muted-foreground" htmlFor="deal-stage">
              Start new deals in
            </label>
            {stagesFailed ? (
              <p className="text-xs text-destructive">
                Could not load your pipeline stages. Reload to try again.
              </p>
            ) : (
              <Select
                id="deal-stage"
                value={stageId}
                onChange={(e) => setStageId(e.target.value)}
                className="w-64"
              >
                <option value="">Choose a stage…</option>
                {openStages.map((s) => (
                  <option key={s.id} value={s.id}>{s.name}</option>
                ))}
              </Select>
            )}
            <p className="text-xs text-muted-foreground">
              Won and lost stages are not offered — a deal that starts closed never gets counted
              as one.
            </p>
          </div>

          <div className="space-y-1.5">
            <label className="text-xs text-muted-foreground" htmlFor="deal-name">
              Deal name
            </label>
            <Input
              id="deal-name"
              value={template}
              onChange={(e) => setTemplate(e.target.value)}
              className="w-full max-w-md font-mono text-xs"
            />
            <div className="flex flex-wrap gap-1 pt-0.5">
              {DEAL_NAME_TOKENS.map((tok) => (
                <button
                  key={tok}
                  type="button"
                  className="rounded border border-border px-1.5 py-0.5 text-[11px] text-muted-foreground hover:bg-muted"
                  onClick={() => setTemplate((t) => `${t}{{${tok}}}`)}
                >
                  {`{{${tok}}}`}
                </button>
              ))}
            </div>
            <p className="text-xs text-muted-foreground">
              Preview: <span className="text-foreground">{previewName || source.name}</span>
              {' · '}
              A field the lead did not send is left out rather than printed, so a name never
              shows <code>{'{{…}}'}</code> to your sales team.
            </p>
          </div>

          <div className="flex items-center gap-2">
            <Button
              size="sm"
              disabled={!stageId || updateSource.isPending || (!dirty && Boolean(deal?.enabled))}
              onClick={() => void save({ enabled: true, stage_id: stageId, name_template: template })}
            >
              {updateSource.isPending ? 'Saving…' : 'Save'}
            </Button>
            {!stageId && (
              <span className="text-xs text-muted-foreground">Choose a stage to turn this on.</span>
            )}
          </div>

          <p className="text-xs text-muted-foreground border-t border-border pt-3">
            <Badge variant="secondary" className="mr-1.5">Note</Badge>
            “Send test lead” never opens a deal. A test deal would be counted in your forecast and
            there would be no way to tell it apart from a real one.
          </p>
        </div>
      )}

      {error && (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {error}
        </div>
      )}
    </div>
  );
}
