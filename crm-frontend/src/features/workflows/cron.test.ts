import { describe, it, expect } from 'vitest';
import {
  buildCron,
  parseCron,
  describeCron,
  isValidCron,
  formatTime,
  defaultScheduleModel,
  type ScheduleModel,
} from './cron';

function model(patch: Partial<ScheduleModel>): ScheduleModel {
  return { ...defaultScheduleModel(), ...patch };
}

describe('buildCron', () => {
  it('hourly → minute only', () => {
    expect(buildCron(model({ frequency: 'hourly', minute: 15 }))).toBe('15 * * * *');
  });
  it('daily → minute + hour', () => {
    expect(buildCron(model({ frequency: 'daily', minute: 30, hour: 8 }))).toBe('30 8 * * *');
  });
  it('weekly → minute + hour + dow', () => {
    expect(buildCron(model({ frequency: 'weekly', minute: 0, hour: 9, dayOfWeek: 1 }))).toBe('0 9 * * 1');
  });
  it('monthly → minute + hour + dom', () => {
    expect(buildCron(model({ frequency: 'monthly', minute: 0, hour: 6, dayOfMonth: 15 }))).toBe('0 6 15 * *');
  });
  it('custom → raw cron trimmed', () => {
    expect(buildCron(model({ frequency: 'custom', cron: '  */5 * * * *  ' }))).toBe('*/5 * * * *');
  });
  it('clamps out-of-range time fields', () => {
    expect(buildCron(model({ frequency: 'daily', minute: 99, hour: 30 }))).toBe('59 23 * * *');
  });
  it('normalises Sunday-as-7 to 0', () => {
    expect(buildCron(model({ frequency: 'weekly', minute: 0, hour: 9, dayOfWeek: 7 }))).toBe('0 9 * * 0');
  });
});

describe('parseCron', () => {
  it('recognises hourly', () => {
    const m = parseCron('15 * * * *');
    expect(m.frequency).toBe('hourly');
    expect(m.minute).toBe(15);
  });
  it('recognises daily', () => {
    const m = parseCron('30 8 * * *');
    expect(m.frequency).toBe('daily');
    expect(m.hour).toBe(8);
    expect(m.minute).toBe(30);
  });
  it('recognises weekly', () => {
    const m = parseCron('0 9 * * 1');
    expect(m.frequency).toBe('weekly');
    expect(m.dayOfWeek).toBe(1);
  });
  it('recognises monthly', () => {
    const m = parseCron('0 6 15 * *');
    expect(m.frequency).toBe('monthly');
    expect(m.dayOfMonth).toBe(15);
  });
  it('falls back to custom for steps/ranges/lists', () => {
    expect(parseCron('*/5 * * * *').frequency).toBe('custom');
    expect(parseCron('0 9 * * 1-5').frequency).toBe('custom');
    expect(parseCron('0 9,17 * * *').frequency).toBe('custom');
  });
  it('falls back to custom for @descriptors', () => {
    expect(parseCron('@daily').frequency).toBe('custom');
  });
  it('preserves the raw expression for custom', () => {
    expect(parseCron('*/5 * * * *').cron).toBe('*/5 * * * *');
  });
});

describe('round-trip', () => {
  const cases = ['15 * * * *', '30 8 * * *', '0 9 * * 1', '0 6 15 * *'];
  for (const c of cases) {
    it(`buildCron(parseCron("${c}")) === "${c}"`, () => {
      expect(buildCron(parseCron(c))).toBe(c);
    });
  }
});

describe('describeCron', () => {
  it('hourly on the hour', () => {
    expect(describeCron('0 * * * *')).toBe('Every hour, on the hour');
  });
  it('hourly at a minute', () => {
    expect(describeCron('15 * * * *')).toBe('Every hour at :15');
  });
  it('daily', () => {
    expect(describeCron('30 8 * * *')).toBe('Every day at 8:30 AM');
  });
  it('weekly', () => {
    expect(describeCron('0 9 * * 1')).toBe('Every Monday at 9:00 AM');
  });
  it('monthly with ordinal', () => {
    expect(describeCron('0 6 1 * *')).toBe('On the 1st of every month at 6:00 AM');
    expect(describeCron('0 6 22 * *')).toBe('On the 22nd of every month at 6:00 AM');
  });
  it('appends timezone when given', () => {
    expect(describeCron('0 9 * * 1', 'America/New_York')).toBe('Every Monday at 9:00 AM (America/New_York)');
  });
  it('describes @descriptors', () => {
    expect(describeCron('@daily')).toBe('Every day at midnight');
    expect(describeCron('@hourly')).toBe('Every hour');
  });
  it('falls back for arbitrary cron', () => {
    expect(describeCron('*/5 * * * *')).toBe('Custom schedule (*/5 * * * *)');
  });
});

describe('isValidCron', () => {
  it('accepts 5-field expressions', () => {
    expect(isValidCron('0 9 * * 1')).toBe(true);
    expect(isValidCron('*/5 * * * *')).toBe(true);
    expect(isValidCron('0 9 * * 1-5')).toBe(true);
    expect(isValidCron('0 9,17 1,15 * *')).toBe(true);
  });
  it('accepts @descriptors', () => {
    expect(isValidCron('@daily')).toBe(true);
    expect(isValidCron('@every 5m')).toBe(true);
  });
  it('rejects empty / wrong field count', () => {
    expect(isValidCron('')).toBe(false);
    expect(isValidCron('   ')).toBe(false);
    expect(isValidCron('0 9 * *')).toBe(false);
    expect(isValidCron('0 9 * * 1 extra')).toBe(false);
  });
  it('rejects garbage characters', () => {
    expect(isValidCron('0 9 * * !')).toBe(false);
    expect(isValidCron('@bogus')).toBe(false);
  });
});

describe('formatTime', () => {
  it('midnight and noon', () => {
    expect(formatTime(0, 0)).toBe('12:00 AM');
    expect(formatTime(12, 0)).toBe('12:00 PM');
  });
  it('afternoon with minutes', () => {
    expect(formatTime(13, 5)).toBe('1:05 PM');
  });
});
