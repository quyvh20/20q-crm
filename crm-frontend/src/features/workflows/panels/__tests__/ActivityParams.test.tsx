import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/react';
import fc from 'fast-check';
import { useBuilderStore } from '../../store';
import { ActionConfigPanel } from '../ActionConfigPanel';
import type { ActionSpec } from '../../types';

// ─────────────────────────────────────────────────────────────────────
// Mocks
//
// The ActivityParams editor lives inside ActionConfigPanel and is not
// exported, so (matching the existing panel test convention) we render the
// whole ActionConfigPanel with the builder store seeded with a selected
// `log_activity` action.
//
// `TemplateInput` is mocked with a faithful double that:
//   • renders real DOM (a <label> plus an <input>/<textarea>) so the
//     example tests can assert labels/values, and
//   • records the exact props the panel passed (notably `onChange`) into a
//     hoisted registry keyed by label, so the verbatim-storage property test
//     (Property 8) can invoke the panel's onChange wiring directly with
//     arbitrary strings — including ones a single-line <input> would coerce
//     in the DOM (e.g. embedded newlines).
// ─────────────────────────────────────────────────────────────────────

const h = vi.hoisted(() => {
  const props: Record<string, any> = {};
  return { props };
});

// Mock the API layer the store imports (mirrors the other panel tests).
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

// Faithful TemplateInput double.
vi.mock('../inputs', async () => {
  const actual = await vi.importActual<any>('../inputs');
  const React = await import('react');
  const MockTemplateInput = (p: any) => {
    // Record the latest props the panel passed for this labeled input.
    h.props[p.label] = p;
    return React.createElement(
      'div',
      null,
      React.createElement('label', null, p.label),
      React.createElement(p.multiline ? 'textarea' : 'input', {
        'aria-label': p.label,
        placeholder: p.placeholder,
        value: p.value ?? '',
        onChange: (e: any) => p.onChange(e.target.value),
      }),
    );
  };
  return { ...actual, TemplateInput: MockTemplateInput };
});

// ─────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────

const ACTION_ID = 'action_log_activity_1';

// Capture the real updateAction so we can restore it after spy overrides
// (reset() shallow-merges and does not replace store action functions).
const REAL_UPDATE_ACTION = useBuilderStore.getState().updateAction;

/** Seed the store with a single selected log_activity action. */
function seedAction(
  params: Record<string, unknown>,
  updateAction?: (...args: any[]) => void,
) {
  const action: ActionSpec = { id: ACTION_ID, type: 'log_activity', params };
  const patch: Record<string, unknown> = {
    actions: [action],
    selectedNodeId: ACTION_ID,
    schema: null,
    schemaLoading: false,
    schemaError: null,
  };
  if (updateAction) patch.updateAction = updateAction;
  useBuilderStore.setState(patch);
}

const VALID_TYPES = ['call', 'meeting', 'note', 'email'] as const;
const TYPE_LABELS: Record<(typeof VALID_TYPES)[number], string> = {
  call: 'Call',
  meeting: 'Meeting',
  note: 'Note',
  email: 'Email',
};

/**
 * Inspect the rendered segmented type picker and return the value of the
 * button currently shown as selected. Selection is indicated by the
 * emerald highlight classes used in the component
 * (`bg-emerald-500/20 text-emerald-300`).
 */
function selectedType(): string | null {
  let found: string | null = null;
  for (const value of VALID_TYPES) {
    const btn = screen.getByRole('button', { name: new RegExp(TYPE_LABELS[value], 'i') });
    if (btn.className.includes('text-emerald-300')) {
      found = value;
    }
  }
  return found;
}

// ─────────────────────────────────────────────────────────────────────
// Setup
// ─────────────────────────────────────────────────────────────────────

beforeEach(() => {
  useBuilderStore.getState().reset();
  // Restore the genuine updateAction in case a prior test swapped in a spy.
  useBuilderStore.setState({ updateAction: REAL_UPDATE_ACTION });
  // Clear the recorded TemplateInput props between tests.
  for (const k of Object.keys(h.props)) delete h.props[k];
});

// ═══════════════════════════════════════════════════════════════════════
// Task 6.2 — Property 8: Title and Body are stored verbatim
// ═══════════════════════════════════════════════════════════════════════
describe('ActivityParams — Property 8: Title and Body stored verbatim', () => {
  // Feature: log-activity-action, Property 8: For any string entered into the
  // Title or Body input — including {{template}} delimiters and leading/trailing
  // whitespace — the panel stores the value character-for-character identical to
  // the entered text, without trimming, escaping, or reordering.
  //
  // NOTE: We assert at the panel's onChange boundary (invoking the onChange the
  // panel passed to TemplateInput). The panel performs no transformation, so this
  // is the precise layer for this property; it also avoids the DOM coercion a
  // single-line <input> applies to strings containing newlines.

  /** Strings that exercise template delimiters, whitespace, and newlines. */
  const verbatimString = fc.oneof(
    fc.string(),
    fc.string().map((s) => `{{${s}}}`),
    fc.string().map((s) => `  ${s}  `),
    fc.string().map((s) => `${s}\n${s}`),
    fc.constantFrom(
      '{{contact.first_name}}',
      '  leading',
      'trailing  ',
      'line1\nline2',
      '{{a}}{{b}}',
      '}}{{',
      '\t\ttabbed\t',
      ' {{ deal.title }} ',
      '',
    ),
  );

  it('forwards the exact Title string to setParam without modification', () => {
    const spy = vi.fn();
    seedAction({ activity_type: 'note', title: '', body: '' }, spy);
    render(<ActionConfigPanel />);

    const titleOnChange = h.props['Title'].onChange as (v: string) => void;

    fc.assert(
      fc.property(verbatimString, (entered) => {
        spy.mockClear();
        titleOnChange(entered);

        expect(spy.mock.calls.length).toBe(1);
        expect(spy.mock.calls[0][0]).toBe(ACTION_ID);
        const stored = spy.mock.calls[0][1].params.title;
        // Character-for-character identical: no trim/escape/reorder.
        expect(stored).toBe(entered);
        expect(spy.mock.calls[0][1]).toEqual({ params: { title: entered } });
      }),
      { numRuns: 150 },
    );
  });

  it('forwards the exact Body string to setParam without modification', () => {
    const spy = vi.fn();
    seedAction({ activity_type: 'note', title: '', body: '' }, spy);
    render(<ActionConfigPanel />);

    const bodyOnChange = h.props['Body'].onChange as (v: string) => void;

    fc.assert(
      fc.property(verbatimString, (entered) => {
        spy.mockClear();
        bodyOnChange(entered);

        expect(spy.mock.calls.length).toBe(1);
        expect(spy.mock.calls[0][0]).toBe(ACTION_ID);
        const stored = spy.mock.calls[0][1].params.body;
        expect(stored).toBe(entered);
        expect(spy.mock.calls[0][1]).toEqual({ params: { body: entered } });
      }),
      { numRuns: 150 },
    );
  });
});

// ═══════════════════════════════════════════════════════════════════════
// Task 6.3 — Property 9: activity type selection is normalized
// ═══════════════════════════════════════════════════════════════════════
describe('ActivityParams — Property 9: activity type selection normalized', () => {
  // Feature: log-activity-action, Property 9: For any value of the activity_type
  // parameter, the panel displays that value as the selected type when it is
  // exactly one of call/meeting/note/email, and otherwise (absent, null, empty,
  // or any other value) displays note as the selected type.

  const activityTypeValue = fc.oneof(
    fc.constantFrom('call', 'meeting', 'note', 'email'),
    fc.constantFrom('Call', 'MEETING', 'Email', 'note ', ' note', 'calls', 'stage_change', 'CALL', ''),
    fc.constant(null),
    fc.constant(undefined),
    fc.string(),
  );

  it('selects the value when valid, otherwise note', () => {
    fc.assert(
      fc.property(activityTypeValue, (value) => {
        // `undefined` models an absent parameter.
        const params = value === undefined ? {} : { activity_type: value };
        seedAction(params);
        render(<ActionConfigPanel />);

        const expected =
          typeof value === 'string' && (VALID_TYPES as readonly string[]).includes(value)
            ? value
            : 'note';
        const actual = selectedType();
        cleanup();

        expect(actual).toBe(expected);
      }),
      { numRuns: 120 },
    );
  });
});

// ═══════════════════════════════════════════════════════════════════════
// Task 6.4 — Example / unit tests
// ═══════════════════════════════════════════════════════════════════════
describe('ActivityParams — example tests', () => {
  it('renders the type picker with exactly the four options Call/Meeting/Note/Email (Req 5.1, 5.2)', () => {
    seedAction({ activity_type: 'note', title: '', body: '' });
    render(<ActionConfigPanel />);

    expect(screen.getByRole('button', { name: /Call/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Meeting/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Note/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Email/i })).toBeInTheDocument();

    // Only the four segmented type buttons render (no schema → no variable
    // buttons; TemplateInput double renders no buttons).
    expect(screen.getAllByRole('button')).toHaveLength(4);
  });

  it('stores the lowercase value when a type is selected (Req 5.3)', () => {
    const spy = vi.fn();
    seedAction({ activity_type: 'note', title: '', body: '' }, spy);
    render(<ActionConfigPanel />);

    fireEvent.click(screen.getByRole('button', { name: /Meeting/i }));
    expect(spy).toHaveBeenLastCalledWith(ACTION_ID, { params: { activity_type: 'meeting' } });

    fireEvent.click(screen.getByRole('button', { name: /Email/i }));
    expect(spy).toHaveBeenLastCalledWith(ACTION_ID, { params: { activity_type: 'email' } });

    fireEvent.click(screen.getByRole('button', { name: /Call/i }));
    expect(spy).toHaveBeenLastCalledWith(ACTION_ID, { params: { activity_type: 'call' } });

    fireEvent.click(screen.getByRole('button', { name: /Note/i }));
    expect(spy).toHaveBeenLastCalledWith(ACTION_ID, { params: { activity_type: 'note' } });
  });

  it('renders a Title input and a multiline Body input bound to params (Req 6.1, 6.2)', () => {
    seedAction({
      activity_type: 'call',
      title: 'Called {{contact.first_name}}',
      body: 'Discussed {{deal.title}}',
    });
    render(<ActionConfigPanel />);

    // Labels render.
    expect(screen.getByText('Title')).toBeInTheDocument();
    expect(screen.getByText('Body')).toBeInTheDocument();

    // Bound to the corresponding params, verbatim.
    expect(h.props['Title'].value).toBe('Called {{contact.first_name}}');
    expect(h.props['Body'].value).toBe('Discussed {{deal.title}}');
    expect((screen.getByLabelText('Title') as HTMLInputElement).value).toBe('Called {{contact.first_name}}');
    expect((screen.getByLabelText('Body') as HTMLTextAreaElement).value).toBe('Discussed {{deal.title}}');

    // Body is the multiline input; Title is not.
    expect(h.props['Body'].multiline).toBe(true);
    expect(h.props['Title'].multiline).toBeFalsy();
  });

  it('renders Title and Body empty when params are absent (Req 6.7)', () => {
    seedAction({}); // no activity_type, title, or body
    render(<ActionConfigPanel />);

    expect(h.props['Title'].value).toBe('');
    expect(h.props['Body'].value).toBe('');
    expect((screen.getByLabelText('Title') as HTMLInputElement).value).toBe('');
    expect((screen.getByLabelText('Body') as HTMLTextAreaElement).value).toBe('');
  });
});
