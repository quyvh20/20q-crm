import { describe, it, expect } from 'vitest';
import { localDraftFromPrompt } from '../builder/localDraft';
import type { WorkflowSchema } from '../api';

const schema = {
  entities: [],
  custom_objects: [],
  stages: [{ id: 'stage_won', name: 'Won', color: '#0f0' }],
  tags: [],
  users: [],
} as unknown as WorkflowSchema;

const stepTypes = (steps: ReturnType<typeof localDraftFromPrompt>['steps']) =>
  steps.map((s) => (s.type === 'action' ? s.action!.type : s.type));

describe('localDraftFromPrompt (copilot offline fallback)', () => {
  it('parses the deal-won example into trigger + ordered actions', () => {
    const d = localDraftFromPrompt(
      'When a deal moves to Won, notify the owner and create a follow-up task and send him email',
      schema,
    );
    expect(d.trigger.type).toBe('deal_stage_changed');
    expect(d.trigger.params?.to_stage).toBe('stage_won');
    // Actions appear in the order the user described them.
    expect(stepTypes(d.steps)).toEqual(['notify_user', 'create_task', 'send_email']);
  });

  it('detects contact_created + a delay + email in order', () => {
    const d = localDraftFromPrompt('When a contact is created, wait 2 days, then email them a welcome', null);
    expect(d.trigger.type).toBe('contact_created');
    expect(stepTypes(d.steps)).toEqual(['delay', 'send_email']);
    const delay = d.steps.find((s) => s.type === 'delay');
    expect(delay?.delay?.duration_sec).toBe(2 * 86400);
  });

  it('falls back to a deal trigger with empty params when no stage matches', () => {
    const d = localDraftFromPrompt('when a deal reaches a stage, log a note', null);
    expect(d.trigger.type).toBe('deal_stage_changed');
    expect(d.trigger.params?.to_stage).toBeUndefined();
    expect(stepTypes(d.steps)).toEqual(['log_activity']);
  });

  it('always returns at least one step for an unrecognized prompt', () => {
    const d = localDraftFromPrompt('do something cool', null);
    expect(d.trigger.type).toBe('contact_created');
    expect(d.steps.length).toBeGreaterThanOrEqual(1);
  });

  it('produces unique step ids with action.id mirrored', () => {
    const d = localDraftFromPrompt('notify owner and email them and assign it', null);
    const ids = d.steps.map((s) => s.id);
    expect(new Set(ids).size).toBe(ids.length);
    for (const s of d.steps) {
      if (s.type === 'action') expect(s.action!.id).toBe(s.id);
    }
  });
});
