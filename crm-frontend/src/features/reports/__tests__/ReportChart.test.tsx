import { describe, it, expect, beforeEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import type { ReportResult } from '../../../lib/api';
import ReportChart from '../charts/ReportChart';

// Recharts' ResponsiveContainer measures 0×0 under jsdom, so the SVG chart
// kinds render nothing to assert on. These tests cover the DOM-rendered kinds
// (KPI, table, empty state); the grouped kinds are exercised in the browser.

beforeEach(cleanup);

describe('ReportChart', () => {
  it('renders a KPI scalar with formatted value and record count', () => {
    const result: ReportResult = { kind: 'scalar', value: 1234567.5, row_count: 42 };
    render(<ReportChart chart="kpi" result={result} />);
    expect(screen.getByText('1,234,567.5')).toBeTruthy();
    expect(screen.getByText('42 records')).toBeTruthy();
  });

  it('renders table rows with column labels and formatted cells', () => {
    const result: ReportResult = {
      kind: 'rows',
      columns: ['title', 'value', 'closed_at'],
      rows: [
        { id: 'r1', title: 'Acme renewal', value: 1500, closed_at: '2026-03-05T10:00:00Z' },
        { id: 'r2', title: 'Globex upsell', value: null, closed_at: null },
      ],
      value: 0,
      row_count: 2,
    };
    render(
      <ReportChart
        chart="table"
        result={result}
        columnLabels={{ title: 'Title', value: 'Value', closed_at: 'Closed At' }}
      />,
    );
    expect(screen.getByText('Title')).toBeTruthy();
    expect(screen.getByText('Acme renewal')).toBeTruthy();
    expect(screen.getByText('1,500')).toBeTruthy();
    // Timestamps show as bare dates; empty cells as an em dash.
    expect(screen.getByText('2026-03-05')).toBeTruthy();
    expect(screen.getAllByText('—').length).toBe(2);
  });

  it('shows an empty state when a grouped result has no data', () => {
    const result: ReportResult = { kind: 'groups', groups: [], value: 0, row_count: 0 };
    render(<ReportChart chart="bar" result={result} />);
    expect(screen.getByText(/No data for this report/)).toBeTruthy();
  });
});
