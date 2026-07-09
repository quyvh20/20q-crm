// The new workflow builder page (A3): a React Flow canvas with a top toolbar and
// a right config panel. Structured editing only — no free-form wiring. As of A3.6
// this is the default builder at /workflows/:id; the legacy dnd-kit builder remains
// at /workflows/:id/legacy until A8.
//
// Data layer (A3.4): the workflow itself loads + saves through React Query
// (../queries); the zustand store holds the working copy + schema. On load the
// query result is hydrated into the store once (id-gated so a post-save cache
// update never clobbers in-progress edits); save goes through the mutation, which
// primes the detail cache and invalidates the list so the index reflects changes.

import { useEffect, useState, useCallback, useRef } from 'react';
import { useParams, useNavigate, useLocation, useSearchParams } from 'react-router-dom';
import { ReactFlowProvider } from '@xyflow/react';
import { ArrowLeft, Loader2, FlaskConical, X } from 'lucide-react';
import { useBuilderStore, getStepAtPath, parseStepPath } from '../store';
import { useWorkflow, useSaveWorkflow, useTestRun } from '../queries';
import { entityKindForTrigger } from '../RunNowModal';
import { BuilderContext, type DryRunState } from './BuilderContext';
import type { InsertContext } from './graph';
import { WorkflowCanvas } from './WorkflowCanvas';
import { InsertMenu } from './InsertMenu';
import { ConfigPanel } from './config/ConfigPanel';
import { DryRunDialog } from './DryRunDialog';
import type { EntityCandidate } from '../EntityPicker';

export function NextBuilder() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const location = useLocation();
  const store = useBuilderStore();

  const duplicateFromId = (location.state as { duplicateFromId?: string } | null)?.duplicateFromId;
  const isEditing = Boolean(id && id !== 'new');
  const isDuplicating = !isEditing && Boolean(duplicateFromId);

  const wfQuery = useWorkflow(id, { enabled: isEditing });
  const dupQuery = useWorkflow(duplicateFromId, { enabled: isDuplicating });
  const saveMutation = useSaveWorkflow();
  const testMutation = useTestRun();

  // Dry-run overlay (A3.5). Tests the SAVED workflow (the server loads it), so the
  // Test control requires a saved, non-dirty workflow with a supported trigger.
  const [dryRun, setDryRun] = useState<DryRunState | null>(null);
  const [testOpen, setTestOpen] = useState(false);
  const sampleKind = entityKindForTrigger(store.trigger?.type);
  const canTest = Boolean(store.workflowId) && sampleKind !== null;

  // Mobile guard (parity with the legacy builder): the canvas needs a wide screen.
  // A full mobile pass is A8; until then, block below 768px rather than render a
  // cramped, unusable canvas.
  const [isMobile, setIsMobile] = useState(false);
  useEffect(() => {
    const check = () => setIsMobile(window.innerWidth < 768);
    check();
    window.addEventListener('resize', check);
    return () => window.removeEventListener('resize', check);
  }, []);

  // A stale overlay would mislead once the flow changes, so drop it on any
  // structural edit (steps/trigger). No-op on initial hydrate (dryRun starts null).
  useEffect(() => {
    setDryRun(null);
  }, [store.steps, store.trigger]);

  // `hydrated` gates the initial spinner and, via the early-return below, ensures
  // the store is hydrated from the query exactly once per target — so refetches
  // triggered by a save don't overwrite the user's subsequent edits.
  const [hydrated, setHydrated] = useState(false);
  const targetKey = isEditing ? `wf:${id}` : isDuplicating ? `dup:${duplicateFromId}` : 'new';
  const hydratedKey = useRef<string | null>(null);

  // Fetch schema once (stays on the store — shared with the config panels); reset
  // the working copy on unmount.
  useEffect(() => {
    store.fetchSchema();
    return () => store.reset();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Re-arm hydration whenever the target workflow changes (param/route change
  // without a remount).
  useEffect(() => {
    if (hydratedKey.current !== targetKey) setHydrated(false);
  }, [targetKey]);

  useEffect(() => {
    if (hydrated) return;
    if (isEditing) {
      if (wfQuery.data) {
        store.applyLoadedWorkflow(wfQuery.data);
        hydratedKey.current = targetKey;
        setHydrated(true);
      }
    } else if (isDuplicating) {
      if (dupQuery.data) {
        store.applyLoadedWorkflow(dupQuery.data);
        store.detachAsDuplicate();
        hydratedKey.current = targetKey;
        setHydrated(true);
      }
    } else {
      store.reset();
      hydratedKey.current = targetKey;
      setHydrated(true);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hydrated, isEditing, isDuplicating, wfQuery.data, dupQuery.data, targetKey]);

  // Deep link (A3.6): /workflows/:id?node=<action_path> selects the matching canvas
  // node — used by RunHistory to jump from a run's step log to its builder node.
  // Runs once the store is hydrated, once per distinct node param.
  const [searchParams] = useSearchParams();
  const nodeParam = searchParams.get('node');
  const handledNodeRef = useRef<string | null>(null);
  useEffect(() => {
    if (!hydrated || !nodeParam || handledNodeRef.current === nodeParam) return;
    handledNodeRef.current = nodeParam;
    const path = parseStepPath(nodeParam);
    if (!path) return;
    const s = useBuilderStore.getState();
    const step = getStepAtPath(s.steps, path);
    if (step) s.selectNode(step.id);
  }, [hydrated, nodeParam]);

  const [insertState, setInsertState] = useState<{ slot: InsertContext; anchor: { x: number; y: number } } | null>(null);

  const onSelect = useCallback((nodeId: string) => store.selectNode(nodeId), [store]);
  const onInsert = useCallback((slot: InsertContext, anchor?: { x: number; y: number }) => {
    setInsertState({ slot, anchor: anchor ?? { x: window.innerWidth / 2, y: window.innerHeight / 2 } });
  }, []);

  const handleSave = () => {
    if (!store.validate()) return;
    setDryRun(null);
    saveMutation.mutate(
      { id: store.workflowId, payload: store.buildSavePayload() },
      {
        onSuccess: (wf, vars) => {
          useBuilderStore.setState({ workflowId: wf.id, createdBy: wf.created_by ?? null, isDirty: false });
          // After a CREATE (vars.id null), make the URL addressable/refresh-safe —
          // parity with the legacy builder. The detail cache was just primed by the
          // mutation, so the re-hydrate reads it without a network round-trip.
          if (!vars.id) navigate(`/workflows/${wf.id}`, { replace: true });
        },
      },
    );
  };

  const runDryRun = (candidate: EntityCandidate) => {
    if (!store.workflowId || !sampleKind) return;
    const body = sampleKind === 'contact' ? { contact_id: candidate.id } : { deal_id: candidate.id };
    testMutation.mutate(
      { id: store.workflowId, body },
      {
        onSuccess: (res) => {
          setDryRun({
            byStep: Object.fromEntries(res.steps.map((s) => [s.step_id, s])),
            conditionResult: res.condition_result,
            sampleLabel: candidate.label,
          });
          setTestOpen(false);
        },
      },
    );
  };

  const loadError = (isEditing && wfQuery.isError) || (isDuplicating && dupQuery.isError);
  if (loadError) {
    return (
      <div className="flex h-[calc(100vh-4rem)] flex-col items-center justify-center gap-3 text-center">
        <p className="text-sm font-medium text-foreground">Couldn't load this workflow.</p>
        <div className="flex gap-2">
          <button
            onClick={() => (isEditing ? wfQuery.refetch() : dupQuery.refetch())}
            className="rounded-md border border-border px-3 py-1.5 text-sm text-foreground hover:bg-muted"
          >
            Retry
          </button>
          <button
            onClick={() => navigate('/workflows')}
            className="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground"
          >
            Back to Workflows
          </button>
        </div>
      </div>
    );
  }

  if (isMobile) {
    return (
      <div className="flex h-[calc(100vh-4rem)] flex-col items-center justify-center gap-3 px-6 text-center">
        <p className="text-base font-semibold text-foreground">Desktop required</p>
        <p className="max-w-sm text-sm text-muted-foreground">
          The workflow builder needs a wider screen. Open it on a desktop or tablet in landscape.
        </p>
        <button
          onClick={() => navigate('/workflows')}
          className="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground"
        >
          Back to Workflows
        </button>
      </div>
    );
  }

  if (!hydrated) {
    return (
      <div className="flex h-[calc(100vh-4rem)] items-center justify-center text-muted-foreground">
        <Loader2 className="h-5 w-5 animate-spin" />
      </div>
    );
  }

  return (
    <div className="flex h-[calc(100vh-4rem)] flex-col">
      {/* Toolbar */}
      <div className="flex items-center gap-3 border-b border-border bg-card px-4 py-2.5">
        <button
          onClick={() => navigate('/workflows')}
          className="flex items-center gap-1.5 rounded-md px-2 py-1 text-sm text-muted-foreground hover:bg-muted hover:text-foreground"
        >
          <ArrowLeft className="h-4 w-4" />
          Workflows
        </button>
        <input
          value={store.name}
          onChange={(e) => store.setName(e.target.value)}
          placeholder="Untitled workflow"
          className="flex-1 bg-transparent text-sm font-semibold text-foreground outline-none placeholder:text-muted-foreground"
        />
        {saveMutation.isError && (
          <span role="alert" className="text-xs text-destructive">
            {saveMutation.error instanceof Error ? saveMutation.error.message : 'Save failed'}
          </span>
        )}
        {store.workflowId && (
          <button
            onClick={() => navigate(`/workflows/${store.workflowId}/legacy`)}
            title="Open this workflow in the classic builder"
            className="rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
          >
            Classic editor
          </button>
        )}
        {canTest && (
          <button
            onClick={() => { testMutation.reset(); setTestOpen(true); }}
            disabled={store.isDirty}
            title={store.isDirty ? 'Save your changes to test the latest version' : 'Dry-run against a sample record'}
            className="flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1.5 text-sm text-foreground hover:bg-muted disabled:opacity-50"
          >
            <FlaskConical className="h-4 w-4" />
            Test
          </button>
        )}
        <button
          onClick={handleSave}
          disabled={saveMutation.isPending || !store.isDirty}
          className="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground disabled:opacity-50"
        >
          {saveMutation.isPending ? 'Saving…' : 'Save'}
        </button>
      </div>

      {/* Dry-run banner */}
      {dryRun && (
        <div className="flex items-center gap-2 border-b border-border bg-muted/40 px-4 py-1.5 text-xs">
          <FlaskConical className="h-3.5 w-3.5 text-primary" />
          <span className="font-medium text-foreground">Dry run</span>
          <span className="text-muted-foreground">· sample: {dryRun.sampleLabel}</span>
          <span className={dryRun.conditionResult ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'}>
            · {dryRun.conditionResult ? 'trigger conditions match' : 'trigger conditions do not match — nothing runs'}
          </span>
          <button
            onClick={() => setDryRun(null)}
            className="ml-auto flex items-center gap-1 rounded px-1.5 py-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"
          >
            <X className="h-3.5 w-3.5" /> Clear
          </button>
        </div>
      )}

      {/* Canvas + right panel */}
      <div className="flex flex-1 overflow-hidden">
        <div className="relative flex-1">
          <ReactFlowProvider>
            <BuilderContext.Provider value={{ onSelect, onInsert, selectedId: store.selectedNodeId, readOnly: false, dryRun }}>
              <WorkflowCanvas
                trigger={store.trigger}
                steps={store.steps}
                selectedId={store.selectedNodeId}
                onSelect={onSelect}
              />
            </BuilderContext.Provider>
          </ReactFlowProvider>
        </div>
        <aside className="w-[380px] shrink-0 overflow-y-auto border-l border-border bg-card">
          <ConfigPanel dryRun={dryRun} />
        </aside>
      </div>

      {insertState && (
        <InsertMenu slot={insertState.slot} anchor={insertState.anchor} onClose={() => setInsertState(null)} />
      )}

      {testOpen && sampleKind && (
        <DryRunDialog
          kind={sampleKind}
          running={testMutation.isPending}
          error={testMutation.isError ? (testMutation.error instanceof Error ? testMutation.error.message : 'Dry run failed') : null}
          onPick={runDryRun}
          onClose={() => setTestOpen(false)}
        />
      )}
    </div>
  );
}
