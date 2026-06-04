import { describe, it, expect, beforeEach, vi } from 'vitest';
import { useBuilderStore } from '../store';
import { getWorkflow } from '../api';
import type { Workflow } from '../types';

/**
 * store.duplicateFrom (P23) — cloning a workflow into a fresh, unsaved draft.
 *
 * duplicateFrom loads the source workflow's full spec (via loadWorkflow →
 * getWorkflow) and then detaches it: nulls the identity (workflowId/createdBy)
 * so the next save() creates a NEW workflow, prefixes the name with "Copy of ",
 * forces the copy inactive, and marks the builder dirty. We mock `../api` so the
 * load resolves from a fixture instead of the network.
 */
vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api')>()),
  getWorkflow: vi.fn(),
}));

const mockGetWorkflow = vi.mocked(getWorkflow);

function makeSource(over: Partial<Workflow> = {}): Workflow {
  return {
    id: 'wf-src',
    org_id: 'org-1',
    name: 'Welcome Email',
    description: 'Greets new contacts',
    is_active: true,
    trigger: { type: 'contact_created' },
    conditions: { op: 'AND', rules: [{ field: 'contact.email', operator: 'is_not_empty' }] },
    actions: [{ id: 'a1', type: 'send_email', params: { to: '{{contact.email}}', subject: 'Hi' } }],
    steps: [{ id: 'a1', type: 'action', action: { id: 'a1', type: 'send_email', params: { to: '{{contact.email}}', subject: 'Hi' } } }],
    action_count: 1,
    version: 3,
    created_by: 'user-9',
    created_at: '2024-01-01T00:00:00Z',
    updated_at: '2024-02-02T00:00:00Z',
    last_run_status: 'completed',
    last_run_at: '2024-02-02T00:00:00Z',
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  useBuilderStore.getState().reset();
});

describe('store.duplicateFrom', () => {
  it('detaches the source into a fresh unsaved draft (null id, "Copy of" name, inactive, dirty)', async () => {
    const source = makeSource();
    mockGetWorkflow.mockResolvedValue(source);

    await useBuilderStore.getState().duplicateFrom('wf-src');
    const s = useBuilderStore.getState();

    // Fetched the source by id.
    expect(mockGetWorkflow).toHaveBeenCalledWith('wf-src');

    // Identity is detached so the next save() creates a NEW workflow.
    expect(s.workflowId).toBeNull();
    expect(s.createdBy).toBeNull();

    // Name prefixed, copy starts inactive, builder is dirty (unsaved).
    expect(s.name).toBe('Copy of Welcome Email');
    expect(s.isActive).toBe(false);
    expect(s.isDirty).toBe(true);

    // The actual workflow content is carried over verbatim.
    expect(s.description).toBe('Greets new contacts');
    expect(s.trigger).toEqual(source.trigger);
    expect(s.conditions).toEqual(source.conditions);
    expect(s.actions).toEqual(source.actions);
    expect(s.steps).toEqual(source.steps);
  });

  it('clones a workflow stored as flat actions (no steps) by normalizing into the step tree', async () => {
    // Legacy/flat workflow: actions present, steps omitted. loadWorkflow normalizes
    // actions → steps, and the clone inherits that normalized tree.
    const source = makeSource({ steps: undefined });
    mockGetWorkflow.mockResolvedValue(source);

    await useBuilderStore.getState().duplicateFrom('wf-src');
    const s = useBuilderStore.getState();

    expect(s.workflowId).toBeNull();
    expect(s.steps).toHaveLength(1);
    expect(s.steps[0]).toMatchObject({ id: 'a1', type: 'action' });
    expect(s.actions).toEqual(source.actions);
  });

  it('prefixes "Copy of" even when duplicating an existing copy', async () => {
    mockGetWorkflow.mockResolvedValue(makeSource({ name: 'Copy of Welcome Email' }));

    await useBuilderStore.getState().duplicateFrom('wf-src');

    expect(useBuilderStore.getState().name).toBe('Copy of Copy of Welcome Email');
  });
});
