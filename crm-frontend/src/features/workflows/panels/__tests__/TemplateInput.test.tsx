import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { TemplateInput } from '../inputs/TemplateInput';
import { useBuilderStore } from '../../store';
import type { WorkflowSchema } from '../../api';

// ── Mock schema ──────────────────────────────────────────────────────
const MOCK_SCHEMA: WorkflowSchema = {
  entities: [
    {
      key: 'contact',
      label: 'Contact',
      icon: '👤',
      fields: [
        { path: 'contact.first_name', label: 'First Name', type: 'string' },
        { path: 'contact.email', label: 'Email', type: 'string' },
        { path: 'contact.phone', label: 'Phone', type: 'string' },
      ],
    },
    {
      key: 'deal',
      label: 'Deal',
      icon: '💰',
      fields: [
        { path: 'deal.title', label: 'Title', type: 'string' },
        { path: 'deal.value', label: 'Value', type: 'number' },
      ],
    },
    {
      key: 'trigger',
      label: 'Trigger Event',
      icon: '⚡',
      fields: [
        { path: 'trigger.type', label: 'Event Type', type: 'string' },
      ],
    },
  ],
  custom_objects: [],
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

/** Render TemplateInput with sensible defaults */
function renderInput(overrides: Partial<Parameters<typeof TemplateInput>[0]> = {}) {
  const onChange = vi.fn();
  const props = {
    label: 'Subject',
    value: '',
    onChange,
    placeholder: 'Enter text...',
    ...overrides,
  };
  const utils = render(<TemplateInput {...props} />);
  return { ...utils, onChange, props };
}

/** Open the variable picker by clicking the {x} button */
async function openPicker() {
  const btn = screen.getByTitle('Insert template variable');
  await userEvent.click(btn);
}

// ── Setup ────────────────────────────────────────────────────────────
beforeEach(() => {
  useBuilderStore.setState({
    schema: null,
    schemaLoading: false,
    schemaError: null,
  });
});

// ═══════════════════════════════════════════════════════════════════════
// Test 1: Insert at cursor position
// ═══════════════════════════════════════════════════════════════════════
describe('TestTemplateInput_InsertsAtCursorPosition', () => {
  it('inserts {{path}} at cursor position within existing text', async () => {
    seedStore();
    const { onChange } = renderInput({ value: 'Hello World' });

    const input = screen.getByPlaceholderText('Enter text...') as HTMLInputElement;

    // Position cursor between "Hello " and "World" (at index 6)
    fireEvent.focus(input);
    input.setSelectionRange(6, 6);

    await openPicker();

    // Click "First Name"
    const firstNameBtn = screen.getByText('First Name');
    await userEvent.click(firstNameBtn);

    // Should insert at position 6: "Hello " + "{{contact.first_name}}" + "World"
    expect(onChange).toHaveBeenCalledWith('Hello {{contact.first_name}}World');
  });

  it('appends at end when cursor is at end of text', async () => {
    seedStore();
    const { onChange } = renderInput({ value: 'Dear ' });

    const input = screen.getByPlaceholderText('Enter text...') as HTMLInputElement;
    fireEvent.focus(input);
    input.setSelectionRange(5, 5); // End of "Dear "

    await openPicker();
    await userEvent.click(screen.getByText('Email'));

    expect(onChange).toHaveBeenCalledWith('Dear {{contact.email}}');
  });

  it('inserts at beginning when cursor is at position 0', async () => {
    seedStore();
    const { onChange } = renderInput({ value: 'suffix' });

    const input = screen.getByPlaceholderText('Enter text...') as HTMLInputElement;
    fireEvent.focus(input);
    input.setSelectionRange(0, 0);

    await openPicker();
    await userEvent.click(screen.getByText('Phone'));

    expect(onChange).toHaveBeenCalledWith('{{contact.phone}}suffix');
  });

  it('inserts into empty input', async () => {
    seedStore();
    const { onChange } = renderInput({ value: '' });

    await openPicker();
    await userEvent.click(screen.getByText('First Name'));

    expect(onChange).toHaveBeenCalledWith('{{contact.first_name}}');
  });
});

// ═══════════════════════════════════════════════════════════════════════
// Test 2: Replace selection
// ═══════════════════════════════════════════════════════════════════════
describe('TestTemplateInput_ReplacesSelection', () => {
  it('replaces selected text range with {{path}}', async () => {
    seedStore();
    const { onChange } = renderInput({ value: 'Hello NAME here' });

    const input = screen.getByPlaceholderText('Enter text...') as HTMLInputElement;
    fireEvent.focus(input);
    // Select "NAME" (indices 6..10)
    input.setSelectionRange(6, 10);

    await openPicker();
    await userEvent.click(screen.getByText('First Name'));

    // "Hello " + "{{contact.first_name}}" + " here"
    expect(onChange).toHaveBeenCalledWith('Hello {{contact.first_name}} here');
  });

  it('replaces entire text when all is selected', async () => {
    seedStore();
    const { onChange } = renderInput({ value: 'old text' });

    const input = screen.getByPlaceholderText('Enter text...') as HTMLInputElement;
    fireEvent.focus(input);
    input.setSelectionRange(0, 8); // Select all of "old text"

    await openPicker();
    await userEvent.click(screen.getByText('Email'));

    expect(onChange).toHaveBeenCalledWith('{{contact.email}}');
  });
});

// ═══════════════════════════════════════════════════════════════════════
// Test 3: Focus returns after insert
// ═══════════════════════════════════════════════════════════════════════
describe('TestTemplateInput_FocusReturnsAfterInsert', () => {
  it('dropdown closes after variable insertion', async () => {
    seedStore();
    renderInput({ value: 'test' });

    await openPicker();

    // Picker should be visible
    expect(screen.getByPlaceholderText('Search fields…')).toBeInTheDocument();

    // Insert a variable
    await userEvent.click(screen.getByText('First Name'));

    // Picker should be closed
    expect(screen.queryByPlaceholderText('Search fields…')).not.toBeInTheDocument();
  });

  it('calls requestAnimationFrame with focus + setSelectionRange', async () => {
    seedStore();

    // Spy on requestAnimationFrame
    const rafSpy = vi.spyOn(window, 'requestAnimationFrame').mockImplementation((cb) => {
      cb(0);
      return 0;
    });

    renderInput({ value: 'Hello World' });

    const input = screen.getByPlaceholderText('Enter text...') as HTMLInputElement;
    const focusSpy = vi.spyOn(input, 'focus');
    const selectionSpy = vi.spyOn(input, 'setSelectionRange');

    fireEvent.focus(input);
    input.setSelectionRange(5, 5);

    await openPicker();
    await userEvent.click(screen.getByText('First Name'));

    // requestAnimationFrame was called (to schedule focus restoration)
    expect(rafSpy).toHaveBeenCalled();
    // focus() and setSelectionRange() were called on the input
    expect(focusSpy).toHaveBeenCalled();
    expect(selectionSpy).toHaveBeenCalled();

    // Cursor should be positioned right after the token
    // "Hello" (5) + "{{contact.first_name}}" (24) = position 29
    const template = '{{contact.first_name}}';
    const expectedCursor = 5 + template.length;
    expect(selectionSpy).toHaveBeenCalledWith(expectedCursor, expectedCursor);

    rafSpy.mockRestore();
  });
});

// ═══════════════════════════════════════════════════════════════════════
// Test 4: Previous action variables hidden for earlier actions
// ═══════════════════════════════════════════════════════════════════════
describe('TestTemplateInput_PreviousActionVarsHidden_ForEarlierActions', () => {
  it('shows only schema entities, no "Previous Actions" group', async () => {
    seedStore();
    renderInput();

    await openPicker();

    // Schema entity groups should be visible (CSS uppercase, DOM text is title-case)
    expect(screen.getByText('Contact')).toBeInTheDocument();
    expect(screen.getByText('Deal')).toBeInTheDocument();
    expect(screen.getByText('Trigger Event')).toBeInTheDocument();

    // There should NOT be a "Previous Actions" group
    expect(screen.queryByText('Previous Actions')).not.toBeInTheDocument();
    expect(screen.queryByText('previous actions')).not.toBeInTheDocument();
  });

  it('does not show action result variables', async () => {
    seedStore();
    renderInput();

    await openPicker();

    // No action.* paths should be in the dropdown
    expect(screen.queryByText(/actions?\.\w+/i)).not.toBeInTheDocument();
  });
});

// ═══════════════════════════════════════════════════════════════════════
// Test 5: Search filters dropdown
// ═══════════════════════════════════════════════════════════════════════
describe('TestTemplateInput_SearchFiltersDropdown', () => {
  it('filters by field label', async () => {
    seedStore();
    renderInput();

    await openPicker();
    const searchInput = screen.getByPlaceholderText('Search fields…');

    await userEvent.type(searchInput, 'email');

    // Email field should be visible
    expect(screen.getByText('Email')).toBeInTheDocument();
    // Non-matching fields should be hidden
    expect(screen.queryByText('First Name')).not.toBeInTheDocument();
    expect(screen.queryByText('Phone')).not.toBeInTheDocument();
    expect(screen.queryByText('Title')).not.toBeInTheDocument();
  });

  it('filters by field path', async () => {
    seedStore();
    renderInput();

    await openPicker();
    const searchInput = screen.getByPlaceholderText('Search fields…');

    await userEvent.type(searchInput, 'first_name');

    // First Name matched by path "contact.first_name"
    expect(screen.getByText('First Name')).toBeInTheDocument();
    // Others hidden
    expect(screen.queryByText('Email')).not.toBeInTheDocument();
  });

  it('search is case-insensitive', async () => {
    seedStore();
    renderInput();

    await openPicker();
    await userEvent.type(screen.getByPlaceholderText('Search fields…'), 'PHONE');

    expect(screen.getByText('Phone')).toBeInTheDocument();
  });

  it('shows empty state when no fields match', async () => {
    seedStore();
    renderInput();

    await openPicker();
    await userEvent.type(screen.getByPlaceholderText('Search fields…'), 'xyznonexistent');

    expect(screen.getByText('No matching fields')).toBeInTheDocument();
  });

  it('hides empty entity groups after filtering', async () => {
    seedStore();
    renderInput();

    await openPicker();
    await userEvent.type(screen.getByPlaceholderText('Search fields…'), 'email');

    // Only Contact group should remain (Email is under Contact)
    expect(screen.getByText('Contact')).toBeInTheDocument();
    // Deal and Trigger groups should be hidden (no matching fields)
    expect(screen.queryByText('Deal')).not.toBeInTheDocument();
    expect(screen.queryByText('Trigger Event')).not.toBeInTheDocument();
  });

  it('shows all groups again after clearing search', async () => {
    seedStore();
    renderInput();

    await openPicker();
    const searchInput = screen.getByPlaceholderText('Search fields…');

    await userEvent.type(searchInput, 'email');
    expect(screen.queryByText('Deal')).not.toBeInTheDocument();

    await userEvent.clear(searchInput);

    // All groups should be back
    expect(screen.getByText('Contact')).toBeInTheDocument();
    expect(screen.getByText('Deal')).toBeInTheDocument();
    expect(screen.getByText('Trigger Event')).toBeInTheDocument();
  });
});
