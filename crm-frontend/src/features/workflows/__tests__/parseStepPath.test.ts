import { describe, it, expect } from 'vitest';
import { parseStepPath, getStepAtPath } from '../store';
import type { WorkflowStep } from '../types';

// parseStepPath backs the A3.6 run-history → canvas deep link: it turns a backend
// action_path string (BuildStepPath format `idx(|branch|idx)*`) into a StepPath that
// getStepAtPath resolves to a builder node.

describe('parseStepPath', () => {
  it('parses a root-level index', () => {
    expect(parseStepPath('0')).toEqual([{ index: 0 }]);
    expect(parseStepPath('3')).toEqual([{ index: 3 }]);
  });

  it('parses a branch descent', () => {
    expect(parseStepPath('1|yes|2')).toEqual([{ index: 1 }, { branch: 'yes', index: 2 }]);
    expect(parseStepPath('0|no|0')).toEqual([{ index: 0 }, { branch: 'no', index: 0 }]);
  });

  it('parses a deep nested path', () => {
    expect(parseStepPath('0|yes|1|no|0')).toEqual([
      { index: 0 },
      { branch: 'yes', index: 1 },
      { branch: 'no', index: 0 },
    ]);
  });

  it('returns null for empty or malformed paths', () => {
    expect(parseStepPath('')).toBeNull();
    expect(parseStepPath('abc')).toBeNull();
    expect(parseStepPath('0|bad|1')).toBeNull(); // branch must be yes/no
    expect(parseStepPath('0|yes')).toBeNull(); // dangling branch, missing index
    expect(parseStepPath('|yes|0')).toBeNull(); // empty first segment (Number('')===0 trap)
    expect(parseStepPath('1e2')).toBeNull(); // non-decimal index form
    expect(parseStepPath('0|yes|1x')).toBeNull(); // non-decimal branch index
  });
});

describe('parseStepPath + getStepAtPath (deep-link resolution)', () => {
  const action = (id: string): WorkflowStep => ({ id, type: 'action', action: { id, type: 'create_task', params: {} } });
  const steps: WorkflowStep[] = [
    action('a0'),
    {
      id: 'c1',
      type: 'condition',
      condition: { op: 'AND', rules: [] },
      yes_steps: [action('y0'), action('y1')],
      no_steps: [action('n0')],
    },
  ];

  it('resolves a root path to the right step', () => {
    const step = getStepAtPath(steps, parseStepPath('0')!);
    expect(step?.id).toBe('a0');
  });

  it('resolves a branch path to the right nested step', () => {
    expect(getStepAtPath(steps, parseStepPath('1|yes|1')!)?.id).toBe('y1');
    expect(getStepAtPath(steps, parseStepPath('1|no|0')!)?.id).toBe('n0');
  });

  it('resolves the condition node itself', () => {
    expect(getStepAtPath(steps, parseStepPath('1')!)?.id).toBe('c1');
  });
});
