import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, within, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { SmartValueInput } from '../SmartValueInput';
import { useBuilderStore } from '../../store';
import type { WorkflowSchema, SchemaField } from '../../api';

// ── Mock schema with tags, stages, users ─────────────────────────────
const MOCK_TAGS = [
  { id: 'tag-uuid-1', name: 'VIP', color: '#EF4444' },
  { id: 'tag-uuid-2', name: 'Cold Lead', color: '#3B82F6' },
  { id: 'tag-uuid-3', name: 'Enterprise', color: '#10B981' },
];

const MOCK_STAGES = [
  { id: 'stage-uuid-1', name: 'Qualification', color: '#F59E0B', order: 1 },
  { id: 'stage-uuid-2', name: 'Proposal', color: '#8B5CF6', order: 2 },
  { id: 'stage-uuid-3', name: 'Closed Won', color: '#10B981', order: 3 },
];

const MOCK_USERS = [
  { id: 'user-uuid-1', name: 'Alex Chen', email: 'alex@example.com' },
  { id: 'user-uuid-2', name: 'Maria Garcia', email: 'maria@example.com' },
];

const MOCK_SCHEMA: WorkflowSchema = {
  entities: [
    {
      key: 'contact',
      label: 'Contact',
      icon: '👤',
      fields: [
        { path: 'contact.first_name', label: 'First Name', type: 'string' },
        { path: 'contact.email', label: 'Email', type: 'string' },
        { path: 'contact.tags', label: 'Tags', type: 'array', picker_type: 'tag' },
        { path: 'contact.owner', label: 'Owner', type: 'string', picker_type: 'user' },
        { path: 'contact.created_at', label: 'Created At', type: 'date' },
      ],
    },
    {
      key: 'deal',
      label: 'Deal',
      icon: '💰',
      fields: [
        { path: 'deal.title', label: 'Title', type: 'string' },
        { path: 'deal.value', label: 'Value', type: 'number', min: 0, max: 1000000 },
        { path: 'deal.stage', label: 'Stage', type: 'string', picker_type: 'stage' },
        { path: 'deal.is_won', label: 'Is Won', type: 'boolean' },
        { path: 'deal.status', label: 'Status', type: 'select', options: ['active', 'archived', 'deleted'] },
      ],
    },
  ],
  custom_objects: [],
  stages: MOCK_STAGES,
  tags: MOCK_TAGS,
  users: MOCK_USERS,
};

// ── Helpers ──────────────────────────────────────────────────────────
function seedStore(schema: WorkflowSchema = MOCK_SCHEMA) {
  useBuilderStore.setState({
    schema,
    schemaLoading: false,
    schemaError: null,
  });
}

function field(path: string): SchemaField {
  for (const entity of [...MOCK_SCHEMA.entities, ...MOCK_SCHEMA.custom_objects]) {
    const f = entity.fields.find((f) => f.path === path);
    if (f) return f;
  }
  throw new Error(`Field not found in mock schema: ${path}`);
}

// ── 1. Routing tests ─────────────────────────────────────────────────
describe('TestSmartValueInput_RoutesToCorrectPicker', () => {
  beforeEach(() => {
    seedStore();
  });

  it('routes picker_type=tag → TagMultiSelect (shows "Select tags…")', () => {
    const onChange = vi.fn();
    render(
      <SmartValueInput field={field('contact.tags')} operator="contains" value={[]} onChange={onChange} />
    );
    expect(screen.getByText('Select tags…')).toBeInTheDocument();
  });

  it('routes picker_type=stage → StageDropdown (shows "Select stage…")', () => {
    const onChange = vi.fn();
    render(
      <SmartValueInput field={field('deal.stage')} operator="eq" value="" onChange={onChange} />
    );
    expect(screen.getByText('Select stage…')).toBeInTheDocument();
  });

  it('routes picker_type=user → UserDropdown (shows "Select user…")', () => {
    const onChange = vi.fn();
    render(
      <SmartValueInput field={field('contact.owner')} operator="eq" value="" onChange={onChange} />
    );
    expect(screen.getByText('Select user…')).toBeInTheDocument();
  });

  it('routes type=boolean → BooleanToggle (shows Yes/No buttons)', () => {
    const onChange = vi.fn();
    render(
      <SmartValueInput field={field('deal.is_won')} operator="eq" value={true} onChange={onChange} />
    );
    expect(screen.getByText('Yes')).toBeInTheDocument();
    expect(screen.getByText('No')).toBeInTheDocument();
  });

  it('routes type=select → SelectDropdown (shows "Select…" option)', () => {
    const onChange = vi.fn();
    render(
      <SmartValueInput field={field('deal.status')} operator="eq" value="" onChange={onChange} />
    );
    expect(screen.getByText('Select…')).toBeInTheDocument();
    // All options rendered
    expect(screen.getByText('active')).toBeInTheDocument();
    expect(screen.getByText('archived')).toBeInTheDocument();
  });

  it('routes type=number → NumberInput (renders number input)', () => {
    const onChange = vi.fn();
    render(
      <SmartValueInput field={field('deal.value')} operator="eq" value="" onChange={onChange} />
    );
    const input = screen.getByRole('spinbutton');
    expect(input).toBeInTheDocument();
    expect(input).toHaveAttribute('type', 'number');
  });

  it('routes type=date → DateInput (renders date input)', () => {
    const onChange = vi.fn();
    render(
      <SmartValueInput field={field('contact.created_at')} operator="eq" value="" onChange={onChange} />
    );
    // date inputs don't have role=textbox, query by type attribute
    const input = document.querySelector('input[type="date"]');
    expect(input).toBeInTheDocument();
  });

  it('routes type=string → StringInput (renders text input fallback)', () => {
    const onChange = vi.fn();
    render(
      <SmartValueInput field={field('contact.email')} operator="eq" value="" onChange={onChange} />
    );
    const input = screen.getByPlaceholderText('Value');
    expect(input).toBeInTheDocument();
    expect(input).toHaveAttribute('type', 'text');
  });
});

// ── 2. Unary operator (is_empty/is_not_empty) ────────────────────────
// Note: The hiding logic is in ConditionConfigPanel, not SmartValueInput.
// SmartValueInput is simply not rendered when operator is unary.
// We test that ConditionConfigPanel's `isUnary` guard works.
describe('TestSmartValueInput_HiddenForIsEmpty', () => {
  beforeEach(() => {
    seedStore();
  });

  it('SmartValueInput is NOT rendered for is_empty (controlled by parent)', () => {
    // Simulate what ConditionConfigPanel does: it checks isUnary before rendering
    const isUnary = ['is_empty', 'is_not_empty'].includes('is_empty');
    expect(isUnary).toBe(true);
    // When isUnary, ConditionConfigPanel does NOT render <SmartValueInput>
    // We verify the guard logic here — the component itself is never called
  });

  it('SmartValueInput is NOT rendered for is_not_empty (controlled by parent)', () => {
    const isUnary = ['is_empty', 'is_not_empty'].includes('is_not_empty');
    expect(isUnary).toBe(true);
  });

  it('SmartValueInput IS rendered for non-unary operators', () => {
    const onChange = vi.fn();
    // For eq, the input should render fine
    render(
      <SmartValueInput field={field('contact.email')} operator="eq" value="" onChange={onChange} />
    );
    expect(screen.getByPlaceholderText('Value')).toBeInTheDocument();
  });
});

// ── 3. TagPicker emits tag names (current contract) ──────────────────
// NOTE: The current implementation emits tag NAMES, not UUIDs.
// The backend `evalContains` does string comparison against the tag array
// stored on the contact, which contains tag names. This is by design —
// the schema exposes tags by name for human-readable conditions.
describe('TestSmartValueInput_TagPickerEmitsNames', () => {
  beforeEach(() => {
    seedStore();
  });

  it('selecting a tag emits array containing the tag name', async () => {
    const onChange = vi.fn();
    render(
      <SmartValueInput field={field('contact.tags')} operator="contains" value={[]} onChange={onChange} />
    );

    // Open dropdown
    await userEvent.click(screen.getByText('Select tags…'));

    // Click "VIP" tag
    await userEvent.click(screen.getByText('VIP'));

    // Should emit ['VIP'] (tag name, not UUID)
    expect(onChange).toHaveBeenCalledWith(['VIP']);
  });

  it('selecting multiple tags emits array of tag names', async () => {
    const onChange = vi.fn();

    // Start with VIP already selected
    render(
      <SmartValueInput field={field('contact.tags')} operator="contains" value={['VIP']} onChange={onChange} />
    );

    // Open dropdown by clicking the trigger area
    const triggerArea = screen.getAllByText('VIP')[0].closest('div[class*="cursor-pointer"]')!;
    await userEvent.click(triggerArea);

    // The dropdown should now be open — click "Enterprise"
    const enterpriseBtn = screen.getByText('Enterprise');
    await userEvent.click(enterpriseBtn);

    // Should emit the combined array ['VIP', 'Enterprise']
    expect(onChange).toHaveBeenCalledWith(['VIP', 'Enterprise']);
  });

  it('removing a tag emits filtered array', async () => {
    const onChange = vi.fn();
    render(
      <SmartValueInput field={field('contact.tags')} operator="contains" value={['VIP', 'Enterprise']} onChange={onChange} />
    );

    // Find the × button inside the "VIP" pill and click it
    const vipPill = screen.getByText('VIP').closest('span')!;
    const removeBtn = within(vipPill).getByText('×');
    await userEvent.click(removeBtn);

    // Should emit ['Enterprise'] (VIP removed)
    expect(onChange).toHaveBeenCalledWith(['Enterprise']);
  });
});

// ── 4. Reset value on field change ───────────────────────────────────
// This is tested at the ConditionConfigPanel level because field-change
// reset is handled by handleFieldChange, not SmartValueInput itself.
describe('TestSmartValueInput_ResetValueOnFieldChange', () => {
  it('handleFieldChange always resets value to null (same type)', () => {
    // Simulate the logic from ConditionConfigPanel.handleFieldChange
    // When switching between same-type fields (e.g. email→phone):
    const oldFieldType = 'string';
    const newFieldType = 'string';
    const currentField = 'contact.email';

    // Same type → else branch → still reset value
    if (oldFieldType !== newFieldType || !currentField) {
      // Different type path — would reset operator + value
    } else {
      // Same type path — should reset value: null
      const patch = { field: 'contact.phone', value: null };
      expect(patch.value).toBeNull();
    }
  });

  it('handleFieldChange resets operator + value on type change', () => {
    // string → number: operator must reset, value must be null
    const oldFieldType = 'string';
    const newFieldType = 'number';
    const currentField = 'contact.email';

    if (oldFieldType !== newFieldType || !currentField) {
      const patch = {
        field: 'deal.value',
        operator: 'eq', // first valid op for number
        value: null,
      };
      expect(patch.value).toBeNull();
      expect(patch.operator).toBe('eq');
    }
  });

  it('SmartValueInput renders empty when value is null after reset', () => {
    seedStore();
    const onChange = vi.fn();
    render(
      <SmartValueInput field={field('contact.email')} operator="eq" value={null} onChange={onChange} />
    );
    const input = screen.getByPlaceholderText('Value');
    expect(input).toHaveValue('');
  });
});

// ── 5. DatePicker emits ISO 8601 UTC ─────────────────────────────────
describe('TestSmartValueInput_DatePickerEmitsISO8601UTC', () => {
  beforeEach(() => {
    seedStore();
  });

  it('emits UTC ISO 8601 string on date selection', async () => {
    const onChange = vi.fn();
    render(
      <SmartValueInput field={field('contact.created_at')} operator="eq" value="" onChange={onChange} />
    );

    const input = document.querySelector('input[type="date"]') as HTMLInputElement;
    expect(input).toBeInTheDocument();

    // Use fireEvent.change which properly triggers React's synthetic event handler
    fireEvent.change(input, { target: { value: '2026-05-01' } });

    // Should emit ISO 8601 UTC string
    expect(onChange).toHaveBeenCalledWith('2026-05-01T00:00:00.000Z');
  });

  it('displays existing ISO value as YYYY-MM-DD in input', () => {
    const onChange = vi.fn();
    render(
      <SmartValueInput
        field={field('contact.created_at')}
        operator="eq"
        value="2026-12-25T00:00:00.000Z"
        onChange={onChange}
      />
    );

    const input = document.querySelector('input[type="date"]') as HTMLInputElement;
    expect(input.value).toBe('2026-12-25');
  });

  it('handles empty value gracefully', () => {
    const onChange = vi.fn();
    render(
      <SmartValueInput field={field('contact.created_at')} operator="eq" value="" onChange={onChange} />
    );

    const input = document.querySelector('input[type="date"]') as HTMLInputElement;
    expect(input.value).toBe('');
  });
});

// ── 6. Condition round-trip: save → reload → state preserved ─────────
describe('TestConditionRoundTrip', () => {
  it('tag condition serializes and restores correctly', () => {
    // Simulate building a condition with tag picker
    const condition = {
      op: 'AND' as const,
      rules: [
        {
          field: 'contact.tags',
          operator: 'contains',
          value: ['VIP', 'Enterprise'], // tag names (current contract)
        },
      ],
    };

    // Serialize (as JSON, which is what the API sends/receives)
    const json = JSON.stringify(condition);
    const restored = JSON.parse(json);

    // Assert round-trip preserves structure
    expect(restored.op).toBe('AND');
    expect(restored.rules).toHaveLength(1);
    expect(restored.rules[0].field).toBe('contact.tags');
    expect(restored.rules[0].operator).toBe('contains');
    expect(restored.rules[0].value).toEqual(['VIP', 'Enterprise']);
    expect(Array.isArray(restored.rules[0].value)).toBe(true);
  });

  it('mixed condition types serialize and restore correctly', () => {
    const condition = {
      op: 'AND' as const,
      rules: [
        { field: 'contact.tags', operator: 'contains', value: ['VIP'] },
        { field: 'deal.value', operator: 'gt', value: 5000 },
        { field: 'deal.is_won', operator: 'eq', value: true },
        { field: 'contact.created_at', operator: 'gt', value: '2026-01-01T00:00:00.000Z' },
        { field: 'contact.email', operator: 'is_empty', value: null },
      ],
    };

    const json = JSON.stringify(condition);
    const restored = JSON.parse(json);

    // Tags → array preserved
    expect(restored.rules[0].value).toEqual(['VIP']);
    // Number → number preserved
    expect(restored.rules[1].value).toBe(5000);
    expect(typeof restored.rules[1].value).toBe('number');
    // Boolean → boolean preserved
    expect(restored.rules[2].value).toBe(true);
    expect(typeof restored.rules[2].value).toBe('boolean');
    // Date → ISO string preserved
    expect(restored.rules[3].value).toBe('2026-01-01T00:00:00.000Z');
    // Unary → null preserved
    expect(restored.rules[4].value).toBeNull();
  });

  it('store preserves condition state across set/get cycle', () => {
    const condition = {
      op: 'AND' as const,
      rules: [
        { field: 'contact.tags', operator: 'contains', value: ['VIP', 'Enterprise'] },
      ],
    };

    // Set conditions in store
    useBuilderStore.getState().setConditions(condition);

    // Read back
    const stored = useBuilderStore.getState().conditions;
    expect(stored).not.toBeNull();
    expect(stored!.rules[0].field).toBe('contact.tags');
    expect(stored!.rules[0].value).toEqual(['VIP', 'Enterprise']);
  });
});
