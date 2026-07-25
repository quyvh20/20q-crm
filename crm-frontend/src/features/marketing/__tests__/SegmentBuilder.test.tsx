import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/react';
import SegmentBuilder from '../SegmentBuilder';
import type { SegmentFieldDescriptor, SegmentFilter } from '../segmentsApi';
import type { Tag } from '../../../lib/api';

const fields: SegmentFieldDescriptor[] = [
  { key: 'email', label: 'Email', type: 'text' },
  { key: 'score', label: 'Score', type: 'number' },
];
const tags: Tag[] = [{ id: 't1', name: 'VIP', color: '#000' }];

function lastEmit(spy: ReturnType<typeof vi.fn>): { ast: SegmentFilter; runnable: boolean } {
  const call = spy.mock.calls.at(-1)!;
  return { ast: call[0] as SegmentFilter, runnable: call[1] as boolean };
}

afterEach(cleanup);

describe('SegmentBuilder AST round-trip', () => {
  it('wraps a bare field-leaf definition in an AND group and reports runnable', () => {
    const onChange = vi.fn();
    render(<SegmentBuilder fields={fields} tags={tags}
      initial={{ field: 'email', operator: 'eq', value: 'x' }} onChange={onChange} />);
    const { ast, runnable } = lastEmit(onChange);
    expect(ast).toEqual({ op: 'and', rules: [{ field: 'email', operator: 'eq', value: 'x' }] });
    expect(runnable).toBe(true);
  });

  it('round-trips a negated OR group over a tag leaf', () => {
    const onChange = vi.fn();
    const initial: SegmentFilter = { op: 'not', rules: [{ op: 'or', rules: [{ tag_id: 't1' }] }] };
    render(<SegmentBuilder fields={fields} tags={tags} initial={initial} onChange={onChange} />);
    const { ast } = lastEmit(onChange);
    expect(ast).toEqual({ op: 'not', rules: [{ op: 'or', rules: [{ tag_id: 't1' }] }] });
  });

  it('an empty definition matches everyone and is runnable', () => {
    const onChange = vi.fn();
    render(<SegmentBuilder fields={fields} tags={tags} initial={{}} onChange={onChange} />);
    const { ast, runnable } = lastEmit(onChange);
    expect(ast).toEqual({ op: 'and', rules: [] });
    expect(runnable).toBe(true);
  });
});

describe('SegmentBuilder editing', () => {
  it('adding a condition emits a field leaf and marks the tree not-runnable until a value is set', () => {
    const onChange = vi.fn();
    render(<SegmentBuilder fields={fields} tags={tags} initial={{}} onChange={onChange} />);
    fireEvent.click(screen.getByText('+ Condition'));
    const { ast, runnable } = lastEmit(onChange);
    expect(ast.op).toBe('and');
    expect(ast.rules).toHaveLength(1);
    expect(ast.rules![0].field).toBe('email');
    // A text eq with an empty value is incomplete → the live count must stay gated.
    expect(runnable).toBe(false);
  });

  it('the Exclude (NOT) checkbox wraps the group in a not node', () => {
    const onChange = vi.fn();
    render(<SegmentBuilder fields={fields} tags={tags}
      initial={{ field: 'email', operator: 'is_not_empty' }} onChange={onChange} />);
    fireEvent.click(screen.getByLabelText('Exclude (NOT)'));
    const { ast } = lastEmit(onChange);
    expect(ast.op).toBe('not');
    expect(ast.rules).toHaveLength(1);
    expect(ast.rules![0].op).toBe('and');
    expect(ast.rules![0].rules![0]).toEqual({ field: 'email', operator: 'is_not_empty', value: undefined });
  });

  it('adding a tag condition emits a tag leaf', () => {
    const onChange = vi.fn();
    render(<SegmentBuilder fields={fields} tags={tags} initial={{}} onChange={onChange} />);
    fireEvent.click(screen.getByText('+ Tag'));
    const { ast } = lastEmit(onChange);
    expect(ast.rules![0]).toEqual({ tag_id: 't1' });
  });
});
