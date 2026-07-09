import { describe, it, expect } from 'vitest';
import {
  offsetToDirection,
  directionToOffset,
  fieldPathLabel,
  describeDateField,
  describeWaitUntil,
  defaultDateFieldParams,
  resolvableObjectsForTrigger,
  objectKeyOfPath,
} from './dateField';

describe('offsetToDirection / directionToOffset', () => {
  it('splits a negative offset into before', () => {
    expect(offsetToDirection(-3)).toEqual({ direction: 'before', days: 3 });
  });
  it('splits a positive offset into after', () => {
    expect(offsetToDirection(2)).toEqual({ direction: 'after', days: 2 });
  });
  it('splits zero into on', () => {
    expect(offsetToDirection(0)).toEqual({ direction: 'on', days: 0 });
  });
  it('recombines round-trip', () => {
    expect(directionToOffset('before', 3)).toBe(-3);
    expect(directionToOffset('after', 2)).toBe(2);
    expect(directionToOffset('on', 5)).toBe(0);
  });
  it('floors and clamps negatives', () => {
    expect(directionToOffset('before', 2.9)).toBe(-2);
    expect(directionToOffset('after', -5)).toBe(0);
  });
});

describe('fieldPathLabel', () => {
  it('prettifies a dotted path tail', () => {
    expect(fieldPathLabel('deal.expected_close_at')).toBe('Expected Close At');
  });
  it('handles a bare field', () => {
    expect(fieldPathLabel('close_date')).toBe('Close Date');
  });
});

describe('describeDateField', () => {
  it('before with a schema label', () => {
    expect(
      describeDateField({ field: 'deal.expected_close_at', offset_days: -3, at_time: '09:00', timezone: 'UTC' }, 'Expected Close'),
    ).toBe('3 days before Expected Close at 9:00 AM (UTC)');
  });
  it('singular day', () => {
    expect(describeDateField({ field: 'deal.closed_at', offset_days: -1, at_time: '17:00' }, 'Closed At')).toBe(
      '1 day before Closed At at 5:00 PM',
    );
  });
  it('after', () => {
    expect(describeDateField({ field: 'deal.closed_at', offset_days: 2, at_time: '08:30' }, 'Closed At')).toBe(
      '2 days after Closed At at 8:30 AM',
    );
  });
  it('on the date', () => {
    expect(describeDateField({ field: 'deal.expected_close_at', offset_days: 0, at_time: '09:00' }, 'Expected Close')).toBe(
      'On Expected Close at 9:00 AM',
    );
  });
  it('falls back to the path tail without a schema label', () => {
    expect(describeDateField({ field: 'deal.expected_close_at', offset_days: -3 })).toContain('before Expected Close At');
  });
  it('prompts when no field is set', () => {
    expect(describeDateField({ field: '' })).toBe('Pick a date field');
  });
  it('defaults time to 9am when at_time is blank', () => {
    expect(describeDateField({ field: 'x.d', offset_days: 0, at_time: '' })).toContain('at 9:00 AM');
  });
});

describe('describeWaitUntil', () => {
  it('phrases an offset as a wait', () => {
    expect(
      describeWaitUntil({ field: 'deal.expected_close_at', offset_days: -3, at_time: '09:00', timezone: 'UTC' }, 'Expected Close'),
    ).toBe('Wait until 3 days before Expected Close at 9:00 AM (UTC)');
  });
  it('on the date', () => {
    expect(describeWaitUntil({ field: 'deal.closed_at', offset_days: 0, at_time: '17:00' }, 'Closed At')).toBe(
      'Wait until Closed At at 5:00 PM',
    );
  });
  it('after the date', () => {
    expect(describeWaitUntil({ field: 'deal.closed_at', offset_days: 2, at_time: '08:00' }, 'Closed At')).toBe(
      'Wait until 2 days after Closed At at 8:00 AM',
    );
  });
  it('prompts when no field', () => {
    expect(describeWaitUntil({ field: '' })).toBe('Pick a date field');
  });
});

describe('resolvableObjectsForTrigger', () => {
  it('contact trigger resolves contact + company', () => {
    const s = resolvableObjectsForTrigger({ type: 'contact_created' });
    expect([...s].sort()).toEqual(['company', 'contact']);
  });
  it('deal trigger resolves deal + contact + company (hydration chain)', () => {
    const s = resolvableObjectsForTrigger({ type: 'deal_stage_changed' });
    expect([...s].sort()).toEqual(['company', 'contact', 'deal']);
  });
  it('deal_updated resolves the same chain', () => {
    const s = resolvableObjectsForTrigger({ type: 'deal_updated' });
    expect(s.has('deal')).toBe(true);
    expect(s.has('contact')).toBe(true);
    expect(s.has('company')).toBe(true);
  });
  it('company trigger resolves only company', () => {
    expect([...resolvableObjectsForTrigger({ type: 'company_updated' })]).toEqual(['company']);
  });
  it('custom object trigger resolves only that slug (no company hydration)', () => {
    expect([...resolvableObjectsForTrigger({ type: 'ticket_created' })]).toEqual(['ticket']);
  });
  it('does NOT match a deal-lookalike custom slug (dealership) to deal', () => {
    // Regression: naive startsWith("deal") would wrongly resolve dealership → deal.
    expect([...resolvableObjectsForTrigger({ type: 'dealership_updated' })]).toEqual(['dealership']);
  });
  it('schedule trigger has no resolvable record', () => {
    expect(resolvableObjectsForTrigger({ type: 'schedule' }).size).toBe(0);
  });
  it('webhook_inbound resolves contact + company', () => {
    const s = resolvableObjectsForTrigger({ type: 'webhook_inbound' });
    expect(s.has('contact')).toBe(true);
    expect(s.has('company')).toBe(true);
  });
  it('no_activity_days honors the entity param', () => {
    expect(resolvableObjectsForTrigger({ type: 'no_activity_days', params: { entity: 'deal' } }).has('deal')).toBe(true);
    expect(resolvableObjectsForTrigger({ type: 'no_activity_days' }).has('contact')).toBe(true);
  });
  it('date_field honors the object param', () => {
    const s = resolvableObjectsForTrigger({ type: 'date_field', params: { object: 'deal' } });
    expect(s.has('deal')).toBe(true);
    expect(s.has('contact')).toBe(true);
  });
  it('defaults to contact when no trigger is chosen yet', () => {
    expect([...resolvableObjectsForTrigger(null)]).toEqual(['contact']);
  });
});

describe('objectKeyOfPath', () => {
  it('extracts the object key from a dotted path', () => {
    expect(objectKeyOfPath('deal.expected_close_at')).toBe('deal');
    expect(objectKeyOfPath('ticket.custom_fields.due')).toBe('ticket');
  });
  it('returns the whole string when unprefixed', () => {
    expect(objectKeyOfPath('close_date')).toBe('close_date');
  });
});

describe('defaultDateFieldParams', () => {
  it('starts empty with a 9am default and a timezone', () => {
    const p = defaultDateFieldParams();
    expect(p.object).toBe('');
    expect(p.field).toBe('');
    expect(p.offset_days).toBe(0);
    expect(p.at_time).toBe('09:00');
    expect(p.timezone).toBeTruthy();
  });
});
