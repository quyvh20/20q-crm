import { describe, it, expect } from 'vitest';
import {
  humanizeValidationError,
  humanizeValidationErrors,
} from '../validationMessages';

describe('humanizeValidationError', () => {
  it('translates the deal_stage_changed / to_stage error the copilot actually shows', () => {
    // Verbatim from crm-backend/internal/automation/validator.go:252.
    const issue = humanizeValidationError({
      field: 'trigger.params.to_stage',
      message: "deal_stage_changed requires 'to_stage' parameter",
    });

    expect(issue.location).toBe('Trigger');
    expect(issue.text).toBe(
      'Deal Stage Changed needs a To Stage — pick the stage a deal must move into for this to fire.',
    );
    // No raw identifiers survive.
    expect(issue.text).not.toMatch(/deal_stage_changed|to_stage|parameter/);
  });

  it('maps both to_stage messages to the same copy (missing key vs empty value)', () => {
    const missing = humanizeValidationError({
      field: 'trigger.params.to_stage',
      message: "deal_stage_changed requires 'to_stage' parameter",
    });
    const empty = humanizeValidationError({
      field: 'trigger.params.to_stage',
      message: "'to_stage' must not be empty — select a specific pipeline stage",
    });
    // Keyed on field, so a reworded backend message can't change the user-facing copy.
    expect(empty.text).toBe(missing.text);
  });

  it('names the step for indexed action fields', () => {
    const issue = humanizeValidationError({
      field: 'actions[2].params.to',
      message: "send_email requires 'to' parameter",
    });
    expect(issue.location).toBe('Step 3');
    expect(issue.text).toBe('Send Email needs a recipient in the To field.');
  });

  it('maps the steps-based action path the current builder emits', () => {
    // validateActionParams is reached from BOTH the flat actions array and the steps
    // tree, so the same problem arrives as `actions[0].params.to` OR
    // `steps[0].action.params.to`. The builder is steps-based, so this is the common
    // one — it must not fall through to the raw-message fallback.
    const issue = humanizeValidationError({
      field: 'steps[0].action.params.to',
      message: "send_email requires 'to' parameter",
    });
    expect(issue.location).toBe('Step 1');
    expect(issue.text).toBe('Send Email needs a recipient in the To field.');
  });

  it('gives the steps and actions forms of one problem identical copy', () => {
    const viaSteps = humanizeValidationError({
      field: 'steps[3].action.params.url',
      message: "send_webhook requires 'url' parameter",
    });
    const viaActions = humanizeValidationError({
      field: 'actions[3].params.url',
      message: "send_webhook requires 'url' parameter",
    });
    expect(viaSteps.text).toBe(viaActions.text);
    expect(viaSteps.location).toBe('Step 4');
    expect(viaActions.location).toBe('Step 4');
  });

  it('still maps step-level fields that are not action params', () => {
    // `steps[].delay` must NOT be folded onto actions[] — it has its own copy.
    const issue = humanizeValidationError({
      field: 'steps[1].delay',
      message: "delay requires 'delay' with 'duration_sec' or 'until_field'",
    });
    expect(issue.location).toBe('Step 2');
    expect(issue.text).toBe('This Wait step needs a duration, or a date field to wait until.');
  });

  it('maps a Wait step’s own delay params (validator.go emits steps[N].delay.*)', () => {
    const tooLong = humanizeValidationError({
      field: 'steps[1].delay.duration_sec',
      message: 'duration_sec 9999999 exceeds maximum of 2592000 (30 days)',
    });
    expect(tooLong.location).toBe('Step 2');
    expect(tooLong.text).toBe('This Wait step needs a duration between 1 second and 30 days.');
    expect(tooLong.text).not.toMatch(/duration_sec|2592000/);

    const badTime = humanizeValidationError({
      field: 'steps[0].delay.at_time',
      message: 'at_time must be HH:MM (24-hour)',
    });
    expect(badTime.text).toBe('Wait-until time must be in HH:MM, 24-hour format.');
  });

  it('normalizes any index to the same copy', () => {
    const a = humanizeValidationError({ field: 'actions[0].params.url', message: 'x' });
    const b = humanizeValidationError({ field: 'actions[7].params.url', message: 'x' });
    expect(a.text).toBe(b.text);
    expect(a.location).toBe('Step 1');
    expect(b.location).toBe('Step 8');
  });

  it('locates conditions and steps', () => {
    expect(humanizeValidationError({ field: 'conditions', message: 'x' }).location).toBe('Conditions');
    expect(humanizeValidationError({ field: 'steps[0].delay', message: 'x' }).location).toBe('Step 1');
  });

  it('falls back to humanizing identifiers for an unmapped field', () => {
    // A field with no curated entry must still lose its snake_case + quotes,
    // so a NEW backend error is never worse than the raw string.
    const issue = humanizeValidationError({
      field: 'actions[0].params.something_new',
      message: "send_webhook requires 'activity_type' parameter",
    });
    expect(issue.text).toBe('Send Webhook requires Activity Type');
    expect(issue.text).not.toMatch(/send_webhook|activity_type|'/);
  });

  it('leaves an unknown identifier readable rather than dropping it', () => {
    const issue = humanizeValidationError({
      field: 'totally.unknown.path',
      message: "unknown action type: 'frobnicate_thing'",
    });
    // Unknown id is preserved (we must not silently hide it), just de-quoted context.
    expect(issue.text).toContain('frobnicate_thing');
    expect(issue.location).toBeNull();
  });

  it('tolerates missing/empty input without throwing', () => {
    expect(() => humanizeValidationError({ field: '', message: '' })).not.toThrow();
    expect(humanizeValidationError({ field: '', message: '' }).location).toBeNull();
  });
});

describe('humanizeValidationErrors', () => {
  it('drops duplicate user-visible problems', () => {
    // The validator reports both when params is missing entirely; one line is enough.
    const issues = humanizeValidationErrors([
      { field: 'trigger.params.to_stage', message: "deal_stage_changed requires 'to_stage' parameter" },
      { field: 'trigger.params.to_stage', message: "'to_stage' must not be empty — select a specific pipeline stage" },
    ]);
    expect(issues).toHaveLength(1);
  });

  it('keeps distinct problems and preserves order', () => {
    const issues = humanizeValidationErrors([
      { field: 'trigger.params.to_stage', message: "deal_stage_changed requires 'to_stage' parameter" },
      { field: 'actions[0].params.to', message: "send_email requires 'to' parameter" },
    ]);
    expect(issues).toHaveLength(2);
    expect(issues[0].location).toBe('Trigger');
    expect(issues[1].location).toBe('Step 1');
  });

  it('does not collapse the same field across different steps', () => {
    const issues = humanizeValidationErrors([
      { field: 'actions[0].params.to', message: "send_email requires 'to' parameter" },
      { field: 'actions[1].params.to', message: "send_email requires 'to' parameter" },
    ]);
    // Same copy, different steps — both must show, or a user fixes only one.
    expect(issues).toHaveLength(2);
    expect(issues.map((i) => i.location)).toEqual(['Step 1', 'Step 2']);
  });
});
