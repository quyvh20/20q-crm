import type { ReportConfig } from '../../lib/api';

// Prebuilt report templates: frontend-only config presets that prefill the
// builder (no DB seeding — picking one just navigates to /reports/new with
// this config). Restricted to registry objects (contact/company/deal) so
// OLS/FLS always apply; tasks/activities aren't reportable yet.
export interface ReportTemplate {
  id: string;
  name: string;
  description: string;
  objectSlug: string;
  config: ReportConfig;
}

export const REPORT_TEMPLATES: ReportTemplate[] = [
  {
    id: 'pipeline-by-stage',
    name: 'Pipeline by Stage',
    description: 'Open deal value in each pipeline stage.',
    objectSlug: 'deal',
    config: {
      chart: 'bar',
      filters: {
        op: 'AND',
        rules: [
          { field: 'is_won', operator: 'eq', value: false },
          { field: 'is_lost', operator: 'eq', value: false },
        ],
      },
      group_by: { field: 'stage' },
      aggregate: { fn: 'sum', field: 'value' },
    },
  },
  {
    id: 'revenue-won-by-month',
    name: 'Revenue Won by Month',
    description: 'Closed-won deal value over time.',
    objectSlug: 'deal',
    config: {
      chart: 'line',
      filters: { op: 'AND', rules: [{ field: 'is_won', operator: 'eq', value: true }] },
      group_by: { field: 'closed_at', bucket: 'month' },
      aggregate: { fn: 'sum', field: 'value' },
    },
  },
  {
    id: 'deals-by-owner',
    name: 'Deals by Owner',
    description: 'How many deals each rep is carrying.',
    objectSlug: 'deal',
    config: {
      chart: 'bar',
      group_by: { field: 'owner_user_id' },
      aggregate: { fn: 'count' },
    },
  },
  {
    id: 'new-contacts-by-month',
    name: 'New Contacts by Month',
    description: 'Contact growth over time.',
    objectSlug: 'contact',
    config: {
      chart: 'line',
      group_by: { field: 'created_at', bucket: 'month' },
      aggregate: { fn: 'count' },
    },
  },
  {
    id: 'open-pipeline-value',
    name: 'Open Pipeline Value',
    description: 'Total value of all open deals, as one number.',
    objectSlug: 'deal',
    config: {
      chart: 'kpi',
      filters: {
        op: 'AND',
        rules: [
          { field: 'is_won', operator: 'eq', value: false },
          { field: 'is_lost', operator: 'eq', value: false },
        ],
      },
      aggregate: { fn: 'sum', field: 'value' },
    },
  },
  {
    id: 'contacts-by-company',
    name: 'Contacts by Company',
    description: 'Where your contacts are concentrated.',
    objectSlug: 'contact',
    config: {
      chart: 'donut',
      group_by: { field: 'company' },
      aggregate: { fn: 'count' },
      sort: { by: 'value', dir: 'desc' },
      limit: 10,
    },
  },
];
