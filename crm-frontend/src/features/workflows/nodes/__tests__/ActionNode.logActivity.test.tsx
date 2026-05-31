import { describe, it, expect, beforeEach } from 'vitest';
import { render } from '@testing-library/react';
import { DndContext } from '@dnd-kit/core';
import fc from 'fast-check';
import { useBuilderStore } from '../../store';
import { WorkflowStepList } from '../WorkflowStepList';
import type { ActionSpec, WorkflowStep } from '../../types';

/**
 * Property 10 — Node summary equals the verbatim title (Requirements 8.4, 8.5).
 *
 * Approach
 * --------
 * `getStepSummary` in `ActionNode.tsx` is a private (non-exported) helper, so we
 * exercise it through the real node-rendering path rather than calling it
 * directly. Mirroring `__tests__/logActivityBackwardCompat.test.tsx`, the
 * smallest faithful unit that renders a saved action node is
 * `WorkflowStepList → StepRenderer → ActionNode`, wrapped in a `DndContext`
 * (which provides the sortable/droppable context `ActionNode` and the inline
 * `AddNodeButton`s rely on). We seed the builder store with the step the way the
 * builder does after loading a workflow.
 *
 * The summary subtitle is the only `<p class="pl-13">` an `ActionNode` renders,
 * and it is rendered solely from `getStepSummary(step)`. Reading its
 * `textContent` therefore yields the summary verbatim (jsdom preserves the text
 * node exactly; the `truncate` CSS class is presentational only and CSS is
 * disabled in the test environment).
 */

/** A non-empty `{{template}}` token, to prove templates survive verbatim. */
const templateVarArb = fc.constantFrom(
  '{{contact.first_name}}',
  '{{deal.title}}',
  '{{contact.email}}',
  '{{org.name}}',
);

/**
 * Title generator covering the full input space of Requirement 8.4/8.5:
 *  - arbitrary strings (including the empty string),
 *  - strings embedding `{{template}}` variables and delimiters,
 *  - whitespace-only / leading-trailing-whitespace strings,
 *  - and (via fc.option) the *absent* case (undefined).
 */
const titleArb = fc.option(
  fc.oneof(
    fc.string(),
    fc
      .tuple(fc.string(), templateVarArb, fc.string())
      .map(([a, v, b]) => a + v + b),
    fc.constantFrom(
      '   ',
      '\t',
      '\n  ',
      ' leading',
      'trailing ',
      '  both  ',
      '{{contact.first_name}}',
      'Call with {{contact.first_name}} re {{deal.title}}',
    ),
  ),
  { nil: undefined, freq: 6 },
);

/** Build a `log_activity` action step as the store holds it after a load. */
function logActivityStep(title: string | undefined): WorkflowStep {
  const params: Record<string, unknown> =
    title === undefined
      ? { activity_type: 'note', body: '' } // title absent
      : { activity_type: 'note', title, body: '' };
  const action: ActionSpec = { id: 'la_node', type: 'log_activity', params };
  return { id: 'la_node', type: 'action', action };
}

/** Render the node the way the builder canvas does, seeded from the store. */
function renderLogActivityNode(title: string | undefined) {
  const step = logActivityStep(title);
  useBuilderStore.setState({ steps: [step], actions: [step.action!] });
  return render(
    <DndContext>
      <WorkflowStepList steps={[step]} parentId={null} branch={null} />
    </DndContext>,
  );
}

/** The summary subtitle is the lone `p.pl-13` an ActionNode renders. */
function summaryText(container: HTMLElement): string | null {
  const el = container.querySelector('p.pl-13');
  return el ? el.textContent : null;
}

beforeEach(() => {
  useBuilderStore.getState().reset();
});

describe('Property 10: node summary equals the verbatim title', () => {
  // Feature: log-activity-action, Property 10: For any saved log_activity node,
  // the node summary equals the exact title (including {{template}} variables)
  // when the title is a non-empty string, and is absent (no subtitle) when the
  // title is empty or absent.
  // Validates: Requirements 8.4, 8.5
  it('shows the verbatim title when non-empty and no subtitle when empty/absent', () => {
    fc.assert(
      fc.property(titleArb, (title) => {
        const { container, unmount } = renderLogActivityNode(title);
        try {
          const summary = summaryText(container);
          const isNonEmpty = typeof title === 'string' && title.length > 0;
          if (isNonEmpty) {
            // Req 8.4: subtitle present and exactly equal to the title verbatim.
            expect(summary).toBe(title);
          } else {
            // Req 8.5: empty string or absent title => no subtitle element.
            expect(summary).toBeNull();
          }
        } finally {
          unmount();
        }
      }),
      { numRuns: 100 },
    );
  });

  // ── Explicit examples (non-PBT) for clarity ──────────────────────────────
  it('renders a concrete title verbatim, including {{template}} variables (Req 8.4)', () => {
    const title = 'Logged a call with {{contact.first_name}}';
    const { container } = renderLogActivityNode(title);
    expect(summaryText(container)).toBe(title);
  });

  it('renders no summary subtitle when the title is an empty string (Req 8.5)', () => {
    const { container } = renderLogActivityNode('');
    expect(summaryText(container)).toBeNull();
  });

  it('renders no summary subtitle when the title is absent (Req 8.5)', () => {
    const { container } = renderLogActivityNode(undefined);
    expect(summaryText(container)).toBeNull();
  });
});
