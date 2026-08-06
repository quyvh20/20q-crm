import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Plus, RefreshCw, Target, Trash2 } from 'lucide-react';
import {
  listLeadScoringRules,
  createLeadScoringRule,
  updateLeadScoringRule,
  deleteLeadScoringRule,
  recomputeLeadScores,
  listReportFields,
  LEAD_SCORE_EVENTS,
  type LeadScoringRule,
  type LeadRuleKind,
  type LeadScoreEvent,
  type ReportFieldDescriptor,
  type ReportFilterGroup,
} from '../../lib/api';
import FilterEditor from '../../features/reports/builder/FilterEditor';
import { useConfirm } from '../../components/common/ConfirmDialog';
import { Badge, Button, EmptyState, ErrorState, Input, Skeleton } from '@/components/ui';

// Lead scoring rules (R9.4). Each rule adds (or subtracts) points from a
// contact's 0-100 score when its condition matches; the total is clamped, so
// rules are order-independent and can be reasoned about one at a time.
//
// Field rules are edited with the REPORT builder's FilterEditor, deliberately:
// the backend validates them with the same compile-check segments use, against
// the same field catalog this page fetches. One editor, one catalog, one
// meaning — a rule that saves here is a rule that runs in the engine.

const EVENT_LABELS: Record<LeadScoreEvent, string> = {
  'email.clicked': 'clicked a marketing email',
  'email.bounced': 'had an email bounce',
  'email.complained': 'marked an email as spam',
};

function RuleCard({
  rule,
  fields,
  onSave,
  onDelete,
  saving,
}: {
  rule: LeadScoringRule;
  fields: ReportFieldDescriptor[];
  onSave: (id: string, data: Parameters<typeof updateLeadScoringRule>[1]) => void;
  onDelete: (rule: LeadScoringRule) => void;
  saving: boolean;
}) {
  const [points, setPoints] = useState(String(rule.points));

  return (
    <div className="rounded-xl border border-border bg-card p-4">
      <div className="flex flex-wrap items-center gap-3">
        <span className="min-w-0 flex-1 truncate text-sm font-medium">{rule.name}</span>
        <Badge variant={rule.kind === 'engagement' ? 'default' : 'secondary'}>
          {rule.kind === 'engagement' ? 'Engagement' : 'Field'}
        </Badge>
        <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
          Points
          <Input
            type="number"
            value={points}
            onChange={(e) => setPoints(e.target.value)}
            onBlur={() => {
              const n = Number(points);
              if (Number.isFinite(n) && n !== rule.points) onSave(rule.id, { points: n });
            }}
            className="h-8 w-20"
          />
        </label>
        <label className="flex items-center gap-1.5 text-xs">
          <input
            type="checkbox"
            checked={rule.enabled}
            disabled={saving}
            onChange={(e) => onSave(rule.id, { enabled: e.target.checked })}
            className="h-4 w-4 rounded border-border"
          />
          Enabled
        </label>
        <Button variant="outline" size="sm" onClick={() => onDelete(rule)} aria-label={`Delete ${rule.name}`}>
          <Trash2 aria-hidden />
        </Button>
      </div>

      <p className="mt-2 text-xs text-muted-foreground">
        {rule.kind === 'engagement'
          ? engagementSummary(rule)
          : 'Matches contacts by their field values.'}
      </p>

      {rule.kind === 'field' && (
        <div className="mt-3 border-t border-border pt-3">
          <FilterEditor
            fields={fields}
            value={rule.condition as unknown as ReportFilterGroup}
            onChange={(g) => {
              // An undefined group means "no rules left". Saving that would be a
              // condition-less rule, which the backend rejects — correctly, since
              // an empty condition would otherwise award points to everyone.
              if (g) onSave(rule.id, { condition: g as unknown as Record<string, unknown> });
            }}
          />
        </div>
      )}
    </div>
  );
}

function engagementSummary(rule: LeadScoringRule): string {
  const c = rule.condition as { event?: string; window_days?: number; min_count?: number };
  const label = EVENT_LABELS[c.event as LeadScoreEvent] ?? c.event ?? 'engagement';
  return `Contact ${label} at least ${c.min_count ?? 1}× in the last ${c.window_days ?? 30} days.`;
}

export default function LeadScoringSection() {
  const qc = useQueryClient();
  const { confirm: confirmDialog, dialog: confirmDialogEl } = useConfirm();
  const [error, setError] = useState('');
  const [adding, setAdding] = useState<LeadRuleKind | null>(null);
  const [newName, setNewName] = useState('');
  const [newPoints, setNewPoints] = useState('10');
  const [newEvent, setNewEvent] = useState<LeadScoreEvent>('email.clicked');
  const [newWindow, setNewWindow] = useState('30');
  const [newMinCount, setNewMinCount] = useState('1');
  const [recomputed, setRecomputed] = useState('');

  const { data: rules = [], isLoading, error: loadError, refetch } = useQuery<LeadScoringRule[]>({
    queryKey: ['lead-scoring-rules'],
    queryFn: listLeadScoringRules,
  });

  // The SAME catalog the engine validates against, so the field picker cannot
  // offer a field a rule would then fail to save with.
  const { data: fields = [] } = useQuery<ReportFieldDescriptor[]>({
    queryKey: ['report-fields', 'contact'],
    queryFn: () => listReportFields('contact'),
  });

  const invalidate = () => qc.invalidateQueries({ queryKey: ['lead-scoring-rules'] });

  const createMut = useMutation({
    mutationFn: (data: Parameters<typeof createLeadScoringRule>[0]) => createLeadScoringRule(data),
    onSuccess: () => {
      setError('');
      setAdding(null);
      setNewName('');
      setNewPoints('10');
      invalidate();
    },
    onError: (e: Error) => setError(e.message),
  });

  const updateMut = useMutation({
    mutationFn: ({ id, data }: { id: string; data: Parameters<typeof updateLeadScoringRule>[1] }) =>
      updateLeadScoringRule(id, data),
    onSuccess: () => { setError(''); invalidate(); },
    onError: (e: Error) => setError(e.message),
  });

  const deleteMut = useMutation({
    mutationFn: (id: string) => deleteLeadScoringRule(id),
    onSuccess: () => { setError(''); invalidate(); },
    onError: (e: Error) => setError(e.message),
  });

  const recomputeMut = useMutation({
    mutationFn: recomputeLeadScores,
    onSuccess: (r) => {
      setError('');
      setRecomputed(
        r.complete
          ? `Rescored ${r.contacts} contact${r.contacts === 1 ? '' : 's'}.`
          : `Rescored ${r.contacts} contacts — more remain and will finish on the next pass.`,
      );
    },
    onError: (e: Error) => setError(e.message),
  });

  const submitNew = () => {
    const points = Number(newPoints);
    if (!newName.trim() || !Number.isFinite(points)) return;
    if (adding === 'engagement') {
      createMut.mutate({
        name: newName.trim(),
        kind: 'engagement',
        points,
        condition: {
          event: newEvent,
          window_days: Number(newWindow) || 30,
          min_count: Number(newMinCount) || 1,
        },
      });
      return;
    }
    // A field rule is created with a starter condition rather than an empty one:
    // the backend rejects a condition-less rule, so an empty new-rule form would
    // fail on save with an error the user cannot act on from here.
    const first = fields.find((f) => f.type === 'text') ?? fields[0];
    if (!first) {
      setError('No contact fields are available to build a rule from.');
      return;
    }
    createMut.mutate({
      name: newName.trim(),
      kind: 'field',
      points,
      condition: { op: 'AND', rules: [{ field: first.key, operator: 'is_not_empty' }] },
    });
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold text-foreground">Lead Scoring</h2>
          <p className="mt-0.5 text-sm text-muted-foreground">
            Rules add or subtract points from every contact's score, which is capped at 0–100.
            Scores refresh automatically; use Recompute to apply changes right away.
          </p>
        </div>
        <Button
          variant="outline"
          onClick={() => recomputeMut.mutate()}
          disabled={recomputeMut.isPending}
        >
          <RefreshCw aria-hidden /> {recomputeMut.isPending ? 'Recomputing…' : 'Recompute now'}
        </Button>
      </div>

      {recomputed && (
        <div className="rounded-lg border border-border bg-accent/30 px-4 py-2 text-sm">{recomputed}</div>
      )}
      {error && (
        <div className="rounded-lg border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive">{error}</div>
      )}

      {isLoading ? (
        <Skeleton className="h-40 rounded-xl" />
      ) : loadError ? (
        <ErrorState title="Couldn't load your scoring rules." error={loadError} onRetry={() => refetch()} />
      ) : rules.length === 0 && !adding ? (
        <EmptyState
          icon={Target}
          title="No scoring rules yet"
          description="Add a rule to start scoring contacts on how well they fit and how they engage."
        />
      ) : (
        <div className="space-y-3">
          {rules.map((rule) => (
            <RuleCard
              key={rule.id}
              rule={rule}
              fields={fields}
              saving={updateMut.isPending}
              onSave={(id, data) => updateMut.mutate({ id, data })}
              onDelete={async (r) => {
                if (!(await confirmDialog({
                  title: `Delete "${r.name}"`,
                  body: 'Contacts will be rescored without this rule.',
                  confirmLabel: 'Delete rule',
                }))) return;
                deleteMut.mutate(r.id);
              }}
            />
          ))}
        </div>
      )}

      {adding && (
        <div className="space-y-3 rounded-xl border border-border bg-accent/20 p-4">
          <h3 className="text-sm font-semibold">
            New {adding === 'engagement' ? 'engagement' : 'field'} rule
          </h3>
          <div className="flex flex-wrap gap-2">
            <Input
              placeholder="Rule name"
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              className="max-w-xs"
            />
            <Input
              type="number"
              placeholder="Points"
              value={newPoints}
              onChange={(e) => setNewPoints(e.target.value)}
              className="w-28"
            />
          </div>
          {adding === 'engagement' && (
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <select
                value={newEvent}
                onChange={(e) => setNewEvent(e.target.value as LeadScoreEvent)}
                className="rounded-md border border-input bg-background px-2 py-1.5 text-sm"
              >
                {LEAD_SCORE_EVENTS.map((ev) => (
                  <option key={ev} value={ev}>{EVENT_LABELS[ev]}</option>
                ))}
              </select>
              <span className="text-muted-foreground">at least</span>
              <Input type="number" value={newMinCount} onChange={(e) => setNewMinCount(e.target.value)} className="w-20" />
              <span className="text-muted-foreground">times in the last</span>
              <Input type="number" value={newWindow} onChange={(e) => setNewWindow(e.target.value)} className="w-24" />
              <span className="text-muted-foreground">days</span>
            </div>
          )}
          <p className="text-xs text-muted-foreground">
            {adding === 'field'
              ? 'The rule is created with a starter condition you can refine below once it exists.'
              : 'Negative points are allowed — a bounce or a spam complaint is a strong negative signal.'}
          </p>
          <div className="flex gap-2">
            <Button onClick={submitNew} disabled={!newName.trim() || createMut.isPending}>
              {createMut.isPending ? 'Adding…' : 'Add rule'}
            </Button>
            <Button variant="outline" onClick={() => { setAdding(null); setError(''); }}>Cancel</Button>
          </div>
        </div>
      )}

      {!adding && (
        <div className="flex gap-2">
          <Button onClick={() => setAdding('field')}>
            <Plus aria-hidden /> Field rule
          </Button>
          <Button variant="outline" onClick={() => setAdding('engagement')}>
            <Plus aria-hidden /> Engagement rule
          </Button>
        </div>
      )}

      {confirmDialogEl}
    </div>
  );
}
