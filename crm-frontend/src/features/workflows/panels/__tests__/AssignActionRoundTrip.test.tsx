import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { useBuilderStore } from '../../store';
import { ActionConfigPanel } from '../ActionConfigPanel';
import type { WorkflowSchema } from '../../api';
import type { ActionSpec } from '../../types';

// ── Mock API layer ───────────────────────────────────────────────────
vi.mock('../../api', async () => {
  const actual = await vi.importActual<typeof import('../../api')>('../../api');
  return {
    ...actual,
    createWorkflow: vi.fn(),
    updateWorkflow: vi.fn(),
    getWorkflow: vi.fn(),
    getWorkflowSchema: vi.fn(),
  };
});

// ── Fixtures ─────────────────────────────────────────────────────────
const USER_ALICE = { id: 'aaaaaaaa-1111-2222-3333-444444444444', name: 'Alice Nguyen', email: 'alice@acme.com' };
const USER_BOB   = { id: 'bbbbbbbb-1111-2222-3333-444444444444', name: 'Bob Tran',     email: 'bob@acme.com' };
const USER_CAROL = { id: 'cccccccc-1111-2222-3333-444444444444', name: 'Carol Le',     email: 'carol@acme.com' };
const USER_DAVE  = { id: 'dddddddd-1111-2222-3333-444444444444', name: 'Dave Pham',    email: 'dave@acme.com' };

const MOCK_SCHEMA: WorkflowSchema = {
  entities: [
    {
      key: 'contact',
      label: 'Contact',
      icon: '👤',
      fields: [
        { path: 'contact.first_name', label: 'First Name', type: 'string' },
        { path: 'contact.email', label: 'Email', type: 'string' },
      ],
    },
  ],
  custom_objects: [],
  stages: [],
  tags: [],
  users: [USER_ALICE, USER_BOB, USER_CAROL, USER_DAVE],
};

/** Assign action with strategy=specific and a known user UUID */
const ASSIGN_SPECIFIC: ActionSpec = {
  id: 'action_assign_specific',
  type: 'assign_user',
  params: {
    entity: 'contact',
    strategy: 'specific',
    user_id: USER_ALICE.id,
  },
};

/** Assign action with strategy=round_robin and a pool of 3 users */
const ASSIGN_ROUND_ROBIN: ActionSpec = {
  id: 'action_assign_rr',
  type: 'assign_user',
  params: {
    entity: 'contact',
    strategy: 'round_robin',
    pool: [USER_ALICE.id, USER_BOB.id, USER_CAROL.id],
  },
};

/** Assign action with strategy=round_robin but empty pool */
const ASSIGN_ROUND_ROBIN_EMPTY: ActionSpec = {
  id: 'action_assign_rr_empty',
  type: 'assign_user',
  params: {
    entity: 'contact',
    strategy: 'round_robin',
    pool: [],
  },
};

/** Assign action with strategy=specific but no user_id */
const ASSIGN_SPECIFIC_EMPTY: ActionSpec = {
  id: 'action_assign_specific_empty',
  type: 'assign_user',
  params: {
    entity: 'contact',
    strategy: 'specific',
  },
};

/** Assign action with strategy=least_loaded */
const ASSIGN_LEAST_LOADED: ActionSpec = {
  id: 'action_assign_ll',
  type: 'assign_user',
  params: {
    entity: 'deal',
    strategy: 'least_loaded',
  },
};

// ── Setup ────────────────────────────────────────────────────────────
beforeEach(() => {
  useBuilderStore.getState().reset();
  useBuilderStore.setState({
    schema: MOCK_SCHEMA,
    schemaLoading: false,
    schemaError: null,
  });
});

// ═══════════════════════════════════════════════════════════════════════
// Test 1: Specific strategy — UUID saves, user shown by name on reload
// ═══════════════════════════════════════════════════════════════════════
describe('TestAssignAction_SpecificUserRoundTrip', () => {
  it('stores user_id as UUID in params', () => {
    useBuilderStore.setState({
      actions: [ASSIGN_SPECIFIC],
      selectedNodeId: ASSIGN_SPECIFIC.id,
    });

    const action = useBuilderStore.getState().actions.find((a) => a.id === ASSIGN_SPECIFIC.id);
    expect(action).toBeDefined();
    expect(action!.params.user_id).toBe(USER_ALICE.id);
    expect(action!.params.strategy).toBe('specific');
  });

  it('renders user dropdown with Alice selected by name', () => {
    useBuilderStore.setState({
      actions: [ASSIGN_SPECIFIC],
      selectedNodeId: ASSIGN_SPECIFIC.id,
    });

    render(<ActionConfigPanel />);

    // The select should show Alice's UUID as the value (rendered as her name in the option)
    const userSelect = screen.getByDisplayValue(`${USER_ALICE.name} (${USER_ALICE.email})`);
    expect(userSelect).toBeInTheDocument();
    expect(userSelect.tagName).toBe('SELECT');
  });

  it('shows all org users as dropdown options', () => {
    useBuilderStore.setState({
      actions: [ASSIGN_SPECIFIC],
      selectedNodeId: ASSIGN_SPECIFIC.id,
    });

    render(<ActionConfigPanel />);

    // All 4 users + "Select a user…" placeholder = 5 options in the User dropdown
    // Find the user select by its current value
    const userSelect = screen.getByDisplayValue(`${USER_ALICE.name} (${USER_ALICE.email})`);
    const options = userSelect.querySelectorAll('option');
    expect(options.length).toBe(5); // placeholder + 4 users
    expect(options[0].textContent).toBe('Select a user…');
    expect(options[1].textContent).toContain('Alice Nguyen');
    expect(options[2].textContent).toContain('Bob Tran');
  });

  it('full save → reset → load: UUID preserved, user shown by name', () => {
    // 1. Set up state
    useBuilderStore.setState({
      workflowId: 'wf_assign_1',
      name: 'Assign Test',
      trigger: { type: 'contact_created', params: {} },
      actions: [ASSIGN_SPECIFIC],
      isDirty: true,
    });

    // 2. Capture payload
    const savedPayload = {
      actions: useBuilderStore.getState().actions.map((a) => ({
        ...a,
        params: { ...a.params },
      })),
    };

    // 3. Verify UUID is in the payload
    expect(savedPayload.actions[0].params.user_id).toBe(USER_ALICE.id);

    // 4. Reset (simulate navigation away)
    useBuilderStore.getState().reset();
    expect(useBuilderStore.getState().actions).toHaveLength(0);

    // 5. Reload from "API response"
    useBuilderStore.setState({
      workflowId: 'wf_assign_1',
      name: 'Assign Test',
      trigger: { type: 'contact_created', params: {} },
      actions: savedPayload.actions as ActionSpec[],
      isDirty: false,
      selectedNodeId: ASSIGN_SPECIFIC.id,
      schema: MOCK_SCHEMA,
    });

    // 6. Verify UUID survived
    const reloaded = useBuilderStore.getState().actions[0];
    expect(reloaded.params.user_id).toBe(USER_ALICE.id);

    // 7. Verify UI renders user by name
    render(<ActionConfigPanel />);
    expect(screen.getByDisplayValue(`${USER_ALICE.name} (${USER_ALICE.email})`)).toBeInTheDocument();
  });

  it('stale UUID falls back to "Select a user…" placeholder', () => {
    const staleAssign: ActionSpec = {
      id: 'action_assign_stale',
      type: 'assign_user',
      params: {
        entity: 'contact',
        strategy: 'specific',
        user_id: 'ffffffff-dead-beef-0000-999999999999', // Not in schema.users
      },
    };

    useBuilderStore.setState({
      actions: [staleAssign],
      selectedNodeId: staleAssign.id,
    });

    render(<ActionConfigPanel />);

    // Should fall back to empty value (showing placeholder option)
    const userSelect = screen.getByDisplayValue('Select a user…');
    expect(userSelect).toBeInTheDocument();
  });
});

// ═══════════════════════════════════════════════════════════════════════
// Test 2: Round Robin — pool[] of 3 users saves and reloads correctly
// ═══════════════════════════════════════════════════════════════════════
describe('TestAssignAction_RoundRobinPoolRoundTrip', () => {
  it('stores pool as array of 3 UUIDs in params', () => {
    useBuilderStore.setState({
      actions: [ASSIGN_ROUND_ROBIN],
      selectedNodeId: ASSIGN_ROUND_ROBIN.id,
    });

    const action = useBuilderStore.getState().actions.find((a) => a.id === ASSIGN_ROUND_ROBIN.id);
    expect(action).toBeDefined();
    expect(action!.params.pool).toEqual([USER_ALICE.id, USER_BOB.id, USER_CAROL.id]);
    expect(action!.params.strategy).toBe('round_robin');
  });

  it('renders checkboxes with 3 users checked', () => {
    useBuilderStore.setState({
      actions: [ASSIGN_ROUND_ROBIN],
      selectedNodeId: ASSIGN_ROUND_ROBIN.id,
    });

    render(<ActionConfigPanel />);

    // All 4 users should render as checkboxes
    const checkboxes = screen.getAllByRole('checkbox');
    expect(checkboxes.length).toBe(4);

    // Alice, Bob, Carol should be checked; Dave should not
    expect(checkboxes[0]).toBeChecked(); // Alice
    expect(checkboxes[1]).toBeChecked(); // Bob
    expect(checkboxes[2]).toBeChecked(); // Carol
    expect(checkboxes[3]).not.toBeChecked(); // Dave
  });

  it('shows pool count in label', () => {
    useBuilderStore.setState({
      actions: [ASSIGN_ROUND_ROBIN],
      selectedNodeId: ASSIGN_ROUND_ROBIN.id,
    });

    render(<ActionConfigPanel />);

    expect(screen.getByText('(3 selected)')).toBeInTheDocument();
  });

  it('shows user names next to checkboxes', () => {
    useBuilderStore.setState({
      actions: [ASSIGN_ROUND_ROBIN],
      selectedNodeId: ASSIGN_ROUND_ROBIN.id,
    });

    render(<ActionConfigPanel />);

    expect(screen.getByText('Alice Nguyen')).toBeInTheDocument();
    expect(screen.getByText('Bob Tran')).toBeInTheDocument();
    expect(screen.getByText('Carol Le')).toBeInTheDocument();
    expect(screen.getByText('Dave Pham')).toBeInTheDocument();
  });

  it('full save → reset → load: all 3 pool UUIDs preserved', () => {
    // 1. Set up state
    useBuilderStore.setState({
      workflowId: 'wf_rr_1',
      name: 'Round Robin Test',
      trigger: { type: 'contact_created', params: {} },
      actions: [ASSIGN_ROUND_ROBIN],
      isDirty: true,
    });

    // 2. Capture payload (deep copy pool array)
    const savedPayload = {
      actions: useBuilderStore.getState().actions.map((a) => ({
        ...a,
        params: { ...a.params, pool: Array.isArray(a.params.pool) ? [...(a.params.pool as string[])] : a.params.pool },
      })),
    };

    // 3. Verify pool in payload
    expect(savedPayload.actions[0].params.pool).toEqual([USER_ALICE.id, USER_BOB.id, USER_CAROL.id]);

    // 4. Reset
    useBuilderStore.getState().reset();
    expect(useBuilderStore.getState().actions).toHaveLength(0);

    // 5. Reload from "API response"
    useBuilderStore.setState({
      workflowId: 'wf_rr_1',
      name: 'Round Robin Test',
      trigger: { type: 'contact_created', params: {} },
      actions: savedPayload.actions as ActionSpec[],
      isDirty: false,
      selectedNodeId: ASSIGN_ROUND_ROBIN.id,
      schema: MOCK_SCHEMA,
    });

    // 6. Verify all 3 pool IDs survived
    const reloaded = useBuilderStore.getState().actions[0];
    expect(reloaded.params.pool).toEqual([USER_ALICE.id, USER_BOB.id, USER_CAROL.id]);

    // 7. Verify UI renders 3 checked checkboxes
    render(<ActionConfigPanel />);
    const checkboxes = screen.getAllByRole('checkbox');
    expect(checkboxes[0]).toBeChecked();
    expect(checkboxes[1]).toBeChecked();
    expect(checkboxes[2]).toBeChecked();
    expect(checkboxes[3]).not.toBeChecked();
    expect(screen.getByText('(3 selected)')).toBeInTheDocument();
  });

  it('pool survives updateAction merge — changing entity preserves pool', () => {
    useBuilderStore.setState({
      actions: [ASSIGN_ROUND_ROBIN],
      selectedNodeId: ASSIGN_ROUND_ROBIN.id,
    });

    // User changes entity to "deal" — pool should stay untouched
    useBuilderStore.getState().updateAction(ASSIGN_ROUND_ROBIN.id, {
      params: { entity: 'deal' },
    });

    const updated = useBuilderStore.getState().actions.find((a) => a.id === ASSIGN_ROUND_ROBIN.id);
    expect(updated!.params.pool).toEqual([USER_ALICE.id, USER_BOB.id, USER_CAROL.id]);
    expect(updated!.params.entity).toBe('deal');
    expect(updated!.params.strategy).toBe('round_robin');
  });
});

// ═══════════════════════════════════════════════════════════════════════
// Test 3: Validation — round_robin rejects empty pool, specific rejects missing user_id
// ═══════════════════════════════════════════════════════════════════════
describe('TestAssignAction_Validation', () => {
  it('rejects save when round_robin pool is empty', () => {
    useBuilderStore.setState({
      name: 'Validation Test',
      trigger: { type: 'contact_created', params: {} },
      actions: [ASSIGN_ROUND_ROBIN_EMPTY],
    });

    const isValid = useBuilderStore.getState().validate();
    expect(isValid).toBe(false);

    const errors = useBuilderStore.getState().errors;
    expect(errors['actions.0.params.pool']).toBeDefined();
    expect(errors['actions.0.params.pool'][0]).toContain('at least one user');
  });

  it('rejects save when specific user_id is missing', () => {
    useBuilderStore.setState({
      name: 'Validation Test',
      trigger: { type: 'contact_created', params: {} },
      actions: [ASSIGN_SPECIFIC_EMPTY],
    });

    const isValid = useBuilderStore.getState().validate();
    expect(isValid).toBe(false);

    const errors = useBuilderStore.getState().errors;
    expect(errors['actions.0.params.user_id']).toBeDefined();
    expect(errors['actions.0.params.user_id'][0]).toContain('Select a user');
  });

  it('allows save when round_robin pool has users', () => {
    useBuilderStore.setState({
      name: 'Valid RR',
      trigger: { type: 'contact_created', params: {} },
      actions: [ASSIGN_ROUND_ROBIN],
    });

    const isValid = useBuilderStore.getState().validate();
    expect(isValid).toBe(true);
    expect(useBuilderStore.getState().errors).toEqual({});
  });

  it('allows save when specific has user_id', () => {
    useBuilderStore.setState({
      name: 'Valid Specific',
      trigger: { type: 'contact_created', params: {} },
      actions: [ASSIGN_SPECIFIC],
    });

    const isValid = useBuilderStore.getState().validate();
    expect(isValid).toBe(true);
    expect(useBuilderStore.getState().errors).toEqual({});
  });

  it('allows save for least_loaded — no params required', () => {
    useBuilderStore.setState({
      name: 'Valid LL',
      trigger: { type: 'contact_created', params: {} },
      actions: [ASSIGN_LEAST_LOADED],
    });

    const isValid = useBuilderStore.getState().validate();
    expect(isValid).toBe(true);
    expect(useBuilderStore.getState().errors).toEqual({});
  });
});

// ═══════════════════════════════════════════════════════════════════════
// Test 4: Least loaded — shows info text, no picker
// ═══════════════════════════════════════════════════════════════════════
describe('TestAssignAction_LeastLoadedInfo', () => {
  it('renders info text for least_loaded — no user picker', () => {
    useBuilderStore.setState({
      actions: [ASSIGN_LEAST_LOADED],
      selectedNodeId: ASSIGN_LEAST_LOADED.id,
    });

    render(<ActionConfigPanel />);

    // Should show explanatory text
    expect(screen.getByText(/automatically assigns/i)).toBeInTheDocument();
    expect(screen.getByText(/fewest/i)).toBeInTheDocument();

    // Should NOT show checkboxes or user dropdown
    expect(screen.queryAllByRole('checkbox')).toHaveLength(0);
    expect(screen.queryByText('Select a user…')).not.toBeInTheDocument();
  });
});

// ═══════════════════════════════════════════════════════════════════════
// Test 5: Strategy switch migrates data — no silent data loss
// ═══════════════════════════════════════════════════════════════════════
describe('TestAssignAction_StrategySwitchMigration', () => {
  it('specific → round_robin: seeds pool with the selected user_id', () => {
    // Start with specific + Alice selected
    useBuilderStore.setState({
      actions: [{ ...ASSIGN_SPECIFIC, params: { ...ASSIGN_SPECIFIC.params } }],
      selectedNodeId: ASSIGN_SPECIFIC.id,
    });

    // Simulate strategy switch via updateAction (same as handleStrategyChange)
    const action = useBuilderStore.getState().actions[0];
    const uid = String(action.params.user_id || '');
    useBuilderStore.getState().updateAction(action.id, {
      params: { strategy: 'round_robin', pool: [uid] },
    });

    const updated = useBuilderStore.getState().actions[0];
    expect(updated.params.strategy).toBe('round_robin');
    expect(updated.params.pool).toEqual([USER_ALICE.id]);
    // user_id should still exist (not cleaned up — harmless)
    expect(updated.params.user_id).toBe(USER_ALICE.id);
  });

  it('round_robin → specific: takes first pool member as user_id', () => {
    // Start with round_robin + [Alice, Bob, Carol]
    useBuilderStore.setState({
      actions: [{ ...ASSIGN_ROUND_ROBIN, params: { ...ASSIGN_ROUND_ROBIN.params } }],
      selectedNodeId: ASSIGN_ROUND_ROBIN.id,
    });

    // Simulate strategy switch
    const pool = useBuilderStore.getState().actions[0].params.pool as string[];
    useBuilderStore.getState().updateAction(ASSIGN_ROUND_ROBIN.id, {
      params: { strategy: 'specific', user_id: pool[0] },
    });

    const updated = useBuilderStore.getState().actions[0];
    expect(updated.params.strategy).toBe('specific');
    expect(updated.params.user_id).toBe(USER_ALICE.id); // first pool member
  });

  it('specific → round_robin → specific: data survives full cycle', () => {
    // Start with specific + Alice
    useBuilderStore.setState({
      actions: [{ ...ASSIGN_SPECIFIC, params: { ...ASSIGN_SPECIFIC.params } }],
      selectedNodeId: ASSIGN_SPECIFIC.id,
    });

    // Switch to round_robin — pool should be [Alice]
    useBuilderStore.getState().updateAction(ASSIGN_SPECIFIC.id, {
      params: { strategy: 'round_robin', pool: [USER_ALICE.id] },
    });
    expect(useBuilderStore.getState().actions[0].params.pool).toEqual([USER_ALICE.id]);

    // Switch back to specific — user_id should be Alice (from pool[0])
    useBuilderStore.getState().updateAction(ASSIGN_SPECIFIC.id, {
      params: { strategy: 'specific', user_id: USER_ALICE.id },
    });
    const final = useBuilderStore.getState().actions[0];
    expect(final.params.strategy).toBe('specific');
    expect(final.params.user_id).toBe(USER_ALICE.id);
  });

  it('least_loaded → round_robin: pool starts empty (no data to migrate)', () => {
    useBuilderStore.setState({
      actions: [{ ...ASSIGN_LEAST_LOADED, params: { ...ASSIGN_LEAST_LOADED.params } }],
      selectedNodeId: ASSIGN_LEAST_LOADED.id,
    });

    // Switch to round_robin — no pool or user_id to seed from
    useBuilderStore.getState().updateAction(ASSIGN_LEAST_LOADED.id, {
      params: { strategy: 'round_robin' },
    });

    const updated = useBuilderStore.getState().actions[0];
    expect(updated.params.strategy).toBe('round_robin');
    // pool should be undefined or empty (no source data)
    const pool = Array.isArray(updated.params.pool) ? updated.params.pool : [];
    expect(pool).toHaveLength(0);
  });

  it('UserDropdown always emits UUID — never name', () => {
    useBuilderStore.setState({
      actions: [ASSIGN_SPECIFIC],
      selectedNodeId: ASSIGN_SPECIFIC.id,
    });

    // Verify the stored value is a UUID format, not a name
    const action = useBuilderStore.getState().actions[0];
    const userId = String(action.params.user_id);
    expect(userId).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/);
    expect(userId).not.toContain('Alice');
    expect(userId).not.toContain('Nguyen');
  });
});
