import { describe, it, expect, beforeEach } from 'vitest';
import { useBuilderStore } from '../store';
import { resolvableObjectsForTrigger, triggerPrimaryObject, triggerOwnerObject } from '../dateField';
import { entityKindForTrigger } from '../RunNowModal';
import { buildTriggerItems, findTriggerItem } from '../builder/catalog';
import { triggerTitle, triggerDescription, triggerLabel } from '../builder/nodeMeta';
import { TRIGGER_LABELS } from '../types';
import type { WorkflowStep } from '../types';

// The email_opened trigger (arc G): a contact-hydrating engagement trigger.
// These pin the FE/BE mirror surfaces the plan flags as lockstep-critical.

const TRIGGER = { type: 'email_opened', params: {} };

function mkAction(id: string): WorkflowStep {
  return { id, type: 'action', action: { id, type: 'create_task', params: { title: 't' } } };
}

beforeEach(() => {
  useBuilderStore.getState().reset();
});

describe('email_opened — contact hydration surfaces', () => {
  it('resolves contact + company objects (mirrors backend buildEvalContext)', () => {
    const set = resolvableObjectsForTrigger(TRIGGER);
    expect(set.has('contact')).toBe(true);
    expect(set.has('company')).toBe(true);
    expect(set.has('deal')).toBe(false);
  });

  it('has contact as primary and owner object (notify_user owner mode works)', () => {
    expect(triggerPrimaryObject(TRIGGER)).toBe('contact');
    expect(triggerOwnerObject(TRIGGER)).toBe('contact');
  });

  it('maps to the contact sample kind for Run Now / Test (mirrors backend)', () => {
    expect(entityKindForTrigger('email_opened')).toBe('contact');
  });
});

describe('email_opened — validate()', () => {
  function seed(params: Record<string, unknown>) {
    useBuilderStore.setState({
      name: 'wf',
      trigger: { type: 'email_opened', params },
    });
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
  }

  it('accepts an empty and wildcard campaign filter', () => {
    for (const params of [{}, { campaign_id: '' }, { campaign_id: '*' }]) {
      useBuilderStore.getState().reset();
      seed(params);
      expect(useBuilderStore.getState().validate()).toBe(true);
    }
  });

  it('accepts a UUID campaign filter', () => {
    seed({ campaign_id: 'f47ac10b-58cc-4372-a567-0e02b2c3d479' });
    expect(useBuilderStore.getState().validate()).toBe(true);
  });

  it('rejects a malformed campaign filter', () => {
    seed({ campaign_id: 'not-a-uuid' });
    expect(useBuilderStore.getState().validate()).toBe(false);
    expect(useBuilderStore.getState().errors['trigger.params.campaign_id']).toBeDefined();
  });
});

describe('email_opened — catalog + labels', () => {
  it('is a palette trigger item in the Email category', () => {
    const item = findTriggerItem('email_opened', null);
    expect(item).toBeDefined();
    expect(item!.category).toBe('Email');
    expect(item!.build()).toEqual({ type: 'email_opened', params: {} });
  });

  it('does not offer webhook_inbound (false affordance) but does offer email_opened', () => {
    const ids = buildTriggerItems(null).map((i) => i.id);
    expect(ids).toContain('email_opened');
    expect(ids).not.toContain('webhook_inbound');
  });

  it('renders human labels everywhere', () => {
    expect(TRIGGER_LABELS.email_opened).toBe('Email Opened');
    expect(triggerLabel(TRIGGER)).toBe('Email Opened');
    expect(triggerTitle(TRIGGER)).toBe('Email opened');
    expect(triggerDescription(TRIGGER)).toMatch(/any campaign email is opened/);
    expect(triggerDescription({ type: 'email_opened', params: { campaign_id: 'f47ac10b-58cc-4372-a567-0e02b2c3d479' } }))
      .toMatch(/specific campaign/);
  });
});
