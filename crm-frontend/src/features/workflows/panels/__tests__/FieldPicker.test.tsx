import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { FieldPicker } from '../FieldPicker';
import { useBuilderStore } from '../../store';
import type { WorkflowSchema } from '../../api';

// ── Mock schema with all category types ──────────────────────────────
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
      ],
    },
    {
      key: 'deal',
      label: 'Deal',
      icon: '💰',
      fields: [
        { path: 'deal.title', label: 'Title', type: 'string' },
        { path: 'deal.value', label: 'Value', type: 'number' },
        { path: 'deal.stage', label: 'Stage', type: 'string', picker_type: 'stage' },
        { path: 'deal.is_won', label: 'Is Won', type: 'boolean' },
      ],
    },
    {
      key: 'trigger',
      label: 'Trigger Event',
      icon: '⚡',
      fields: [
        { path: 'trigger.type', label: 'Type', type: 'string' },
        { path: 'trigger.timestamp', label: 'Timestamp', type: 'date' },
      ],
    },
  ],
  custom_objects: [
    {
      key: 'property',
      label: 'Property',
      icon: '🏠',
      fields: [
        { path: 'property.address', label: 'Address', type: 'string' },
        { path: 'property.price', label: 'Price', type: 'number' },
      ],
    },
  ],
  stages: [],
  tags: [],
  users: [],
};

// ── Helpers ──────────────────────────────────────────────────────────
function seedStore(schema: WorkflowSchema | null = MOCK_SCHEMA) {
  useBuilderStore.setState({
    schema,
    schemaLoading: false,
    schemaError: null,
  });
}

// ── Tests ────────────────────────────────────────────────────────────
describe('TestFieldPicker_RendersAllSchemaCategories', () => {
  const onChange = vi.fn();

  beforeEach(() => {
    onChange.mockClear();
    useBuilderStore.setState({
      schema: null,
      schemaLoading: false,
      schemaError: null,
    });
  });

  // ── Category list rendering ────────────────────────────────────────

  it('renders all built-in entity categories from schema', async () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} />);

    // Open the dropdown
    await userEvent.click(screen.getByText('Select field…'));

    // All 3 built-in categories should appear
    expect(screen.getByText('Contact')).toBeInTheDocument();
    expect(screen.getByText('Deal')).toBeInTheDocument();
    expect(screen.getByText('Trigger Event')).toBeInTheDocument();
  });

  it('renders custom_objects as categories alongside built-in entities', async () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} />);

    await userEvent.click(screen.getByText('Select field…'));

    // Custom object should appear in category list
    expect(screen.getByText('Property')).toBeInTheDocument();
  });

  it('shows field counts for each category', async () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} />);

    await userEvent.click(screen.getByText('Select field…'));

    // Contact=3, Deal=4, Trigger=2, Property=2
    expect(screen.getByText('3 fields')).toBeInTheDocument();
    expect(screen.getByText('4 fields')).toBeInTheDocument();
    expect(screen.getAllByText('2 fields')).toHaveLength(2); // Trigger + Property
  });

  // ── Drill-in to category ───────────────────────────────────────────

  it('drills into category showing only its fields', async () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} />);

    await userEvent.click(screen.getByText('Select field…'));
    await userEvent.click(screen.getByText('Contact'));

    // Contact fields visible
    expect(screen.getByText('First Name')).toBeInTheDocument();
    expect(screen.getByText('Email')).toBeInTheDocument();
    expect(screen.getByText('Tags')).toBeInTheDocument();

    // Deal fields NOT visible
    expect(screen.queryByText('Title')).not.toBeInTheDocument();
    expect(screen.queryByText('Value')).not.toBeInTheDocument();
  });

  it('shows back button when drilled into a category', async () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} />);

    await userEvent.click(screen.getByText('Select field…'));
    await userEvent.click(screen.getByText('Deal'));

    // Back breadcrumb should show the category name
    expect(screen.getByText('Deal')).toBeInTheDocument();
    // Deal fields visible
    expect(screen.getByText('Title')).toBeInTheDocument();
    expect(screen.getByText('Is Won')).toBeInTheDocument();
  });

  // ── Type badges ────────────────────────────────────────────────────

  it('shows type badges for each field', async () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} />);

    await userEvent.click(screen.getByText('Select field…'));
    await userEvent.click(screen.getByText('Deal'));

    // Type label badges — Deal has Title(string/Abc), Value(number/#), Stage(string/Abc), Is Won(boolean/✓/✗)
    expect(screen.getAllByText('Abc').length).toBeGreaterThanOrEqual(1); // string fields
    expect(screen.getByText('#')).toBeInTheDocument();      // number (Value)
    expect(screen.getByText('✓/✗')).toBeInTheDocument();   // boolean (Is Won)
  });

  it('shows picker_type badges for tag/stage/user fields', async () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} />);

    await userEvent.click(screen.getByText('Select field…'));
    await userEvent.click(screen.getByText('Contact'));

    // Tags field has picker_type: 'tag'
    expect(screen.getByText('tag')).toBeInTheDocument();
  });

  // ── Entity filter prop ─────────────────────────────────────────────

  it('filters categories when entities prop is provided', async () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} entities={['contact']} />);

    await userEvent.click(screen.getByText('Select field…'));

    // Only Contact should appear
    expect(screen.getByText('Contact')).toBeInTheDocument();

    // Others should NOT appear
    expect(screen.queryByText('Deal')).not.toBeInTheDocument();
    expect(screen.queryByText('Trigger Event')).not.toBeInTheDocument();
    expect(screen.queryByText('Property')).not.toBeInTheDocument();
  });

  // ── onChange emits path + fieldMeta ─────────────────────────────────

  it('emits path and fieldMeta on selection', async () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} />);

    await userEvent.click(screen.getByText('Select field…'));
    await userEvent.click(screen.getByText('Contact'));
    await userEvent.click(screen.getByText('Tags'));

    expect(onChange).toHaveBeenCalledOnce();
    expect(onChange).toHaveBeenCalledWith('contact.tags', {
      type: 'array',
      picker_type: 'tag',
      options: undefined,
    });
  });

  // ── Search ─────────────────────────────────────────────────────────

  it('search matches by field label', async () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} />);

    await userEvent.click(screen.getByText('Select field…'));
    await userEvent.type(screen.getByPlaceholderText('Search all fields...'), 'Tags');

    // Tags field visible in flat search results
    expect(screen.getByText('Tags')).toBeInTheDocument();
    // Unrelated fields not visible
    expect(screen.queryByText('Is Won')).not.toBeInTheDocument();
  });

  it('search matches by field path', async () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} />);

    await userEvent.click(screen.getByText('Select field…'));
    await userEvent.type(screen.getByPlaceholderText('Search all fields...'), 'first_name');

    // First Name matched via path "contact.first_name"
    expect(screen.getByText('First Name')).toBeInTheDocument();
  });

  it('search matches by entity label', async () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} />);

    await userEvent.click(screen.getByText('Select field…'));
    await userEvent.type(screen.getByPlaceholderText('Search all fields...'), 'deal');

    // All deal fields visible
    expect(screen.getByText('Title')).toBeInTheDocument();
    expect(screen.getByText('Value')).toBeInTheDocument();
    expect(screen.getByText('Stage')).toBeInTheDocument();
    expect(screen.getByText('Is Won')).toBeInTheDocument();
  });

  it('shows empty state when search has no matches', async () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} />);

    await userEvent.click(screen.getByText('Select field…'));
    await userEvent.type(screen.getByPlaceholderText('Search all fields...'), 'xyznonexistent');

    expect(screen.getByText('No matching fields')).toBeInTheDocument();
  });

  // ── Loading / Error states ─────────────────────────────────────────

  it('renders skeleton loader when schema is loading', () => {
    useBuilderStore.setState({ schema: null, schemaLoading: true, schemaError: null });
    const { container } = render(<FieldPicker value={null} onChange={onChange} />);

    expect(container.querySelector('.animate-pulse')).toBeInTheDocument();
  });

  it('renders disabled error state when schema fails', () => {
    useBuilderStore.setState({ schema: null, schemaLoading: false, schemaError: 'Network error' });
    render(<FieldPicker value={null} onChange={onChange} />);

    expect(screen.getByText('Schema unavailable')).toBeInTheDocument();
  });

  // ── No hardcoded fields ────────────────────────────────────────────

  it('shows empty state when schema has no entities', async () => {
    seedStore({ entities: [], custom_objects: [], stages: [], tags: [], users: [] });
    render(<FieldPicker value={null} onChange={onChange} />);

    await userEvent.click(screen.getByText('Select field…'));

    expect(screen.getByText('No fields available')).toBeInTheDocument();
  });

  // ── Value display ──────────────────────────────────────────────────

  it('displays selected value with entity breadcrumb in trigger button', () => {
    seedStore();
    render(<FieldPicker value="contact.tags" onChange={onChange} />);

    // Trigger button should show: Contact › Tags
    expect(screen.getByText('Contact')).toBeInTheDocument();
    expect(screen.getByText('Tags')).toBeInTheDocument();
    expect(screen.getByText('array')).toBeInTheDocument(); // type badge in trigger
  });

  it('shows placeholder when value is null', () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} placeholder="Pick a field" />);

    expect(screen.getByText('Pick a field')).toBeInTheDocument();
  });

  // ── A11y: Keyboard navigation ──────────────────────────────────────

  it('ArrowDown/ArrowUp cycles through categories', async () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} />);

    // Open the picker
    await userEvent.click(screen.getByText('Select field…'));

    // ArrowDown → first category gets focus highlight
    await userEvent.keyboard('{ArrowDown}');
    // First category = Contact
    const contactBtn = screen.getByText('Contact').closest('button')!;
    expect(contactBtn.className).toContain('text-white');

    // ArrowDown again → Deal
    await userEvent.keyboard('{ArrowDown}');
    const dealBtn = screen.getByText('Deal').closest('button')!;
    expect(dealBtn.className).toContain('text-white');

    // ArrowUp → back to Contact
    await userEvent.keyboard('{ArrowUp}');
    expect(contactBtn.className).toContain('text-white');
  });

  it('Enter on a category drills in', async () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} />);

    await userEvent.click(screen.getByText('Select field…'));
    await userEvent.keyboard('{ArrowDown}'); // Focus Contact
    await userEvent.keyboard('{Enter}');     // Drill in

    // Should now show Contact fields
    expect(screen.getByText('First Name')).toBeInTheDocument();
    expect(screen.getByText('Email')).toBeInTheDocument();
  });

  it('Escape closes the dropdown', async () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} />);

    await userEvent.click(screen.getByText('Select field…'));
    expect(screen.getByPlaceholderText('Search all fields...')).toBeInTheDocument();

    await userEvent.keyboard('{Escape}');
    // Dropdown should be closed
    expect(screen.queryByPlaceholderText('Search all fields...')).not.toBeInTheDocument();
  });

  it('ArrowRight drills into focused category', async () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} />);

    await userEvent.click(screen.getByText('Select field…'));
    await userEvent.keyboard('{ArrowDown}');  // Focus Contact
    await userEvent.keyboard('{ArrowRight}'); // Drill in

    // Should show Contact fields
    expect(screen.getByText('First Name')).toBeInTheDocument();
    expect(screen.getByText('Tags')).toBeInTheDocument();
  });

  it('Enter on a field selects it and emits onChange', async () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} />);

    await userEvent.click(screen.getByText('Select field…'));
    await userEvent.keyboard('{ArrowDown}'); // Focus Contact
    await userEvent.keyboard('{Enter}');     // Drill in
    await userEvent.keyboard('{ArrowDown}'); // Skip back button (idx 0)
    await userEvent.keyboard('{ArrowDown}'); // Focus First Name (idx 1)
    await userEvent.keyboard('{Enter}');     // Select

    expect(onChange).toHaveBeenCalledOnce();
    expect(onChange).toHaveBeenCalledWith('contact.first_name', {
      type: 'string',
      picker_type: undefined,
      options: undefined,
    });
  });

  // ── ARIA attributes ────────────────────────────────────────────────

  it('trigger button has correct ARIA attributes', () => {
    seedStore();
    render(<FieldPicker value={null} onChange={onChange} />);

    const trigger = screen.getByText('Select field…').closest('button')!;
    expect(trigger).toHaveAttribute('aria-haspopup', 'listbox');
    expect(trigger).toHaveAttribute('aria-expanded', 'false');
  });

  // ── Performance: Virtualization ────────────────────────────────────

  it('limits rendered items to 50 and shows overflow message', async () => {
    // Create an entity with 60 fields
    const bigEntity: WorkflowSchema = {
      entities: [{
        key: 'big',
        label: 'Big',
        icon: '📦',
        fields: Array.from({ length: 60 }, (_, i) => ({
          path: `big.field_${i}`,
          label: `Field ${i}`,
          type: 'string' as const,
        })),
      }],
      custom_objects: [],
      stages: [],
      tags: [],
      users: [],
    };
    seedStore(bigEntity);
    render(<FieldPicker value={null} onChange={onChange} />);

    await userEvent.click(screen.getByText('Select field…'));

    // Type to search and get all 60 in flat results
    await userEvent.type(screen.getByPlaceholderText('Search all fields...'), 'Field');

    // Should show the overflow message
    expect(screen.getByText('+10 more — refine your search')).toBeInTheDocument();

    // Should NOT render Field 59 (beyond limit)
    expect(screen.queryByText('Field 59')).not.toBeInTheDocument();

    // Should render Field 0 (within limit)
    expect(screen.getByText('Field 0')).toBeInTheDocument();
  });
});
