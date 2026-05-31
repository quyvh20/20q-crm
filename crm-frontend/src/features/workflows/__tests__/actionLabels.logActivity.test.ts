import { describe, it, expect } from 'vitest';
import { ACTION_LABELS, ACTION_ICONS } from '../types';
import type { ActionType } from '../types';

// Validates: Requirements 8.2, 8.3
describe('log_activity action labels and icons', () => {
  it('exposes the correct human-readable label', () => {
    // Validates: Requirements 8.2
    expect(ACTION_LABELS.log_activity).toBe('Log Activity');
  });

  it('exposes a non-empty string icon', () => {
    // Validates: Requirements 8.3
    expect(ACTION_ICONS.log_activity).toBeTruthy();
    expect(typeof ACTION_ICONS.log_activity).toBe('string');
    expect(ACTION_ICONS.log_activity.length).toBeGreaterThan(0);
  });

  it('treats "log_activity" as a valid ActionType', () => {
    // Referencing the value as an ActionType compiles only if it is part of the union.
    const actionType: ActionType = 'log_activity';
    expect(actionType).toBe('log_activity');
  });
});
