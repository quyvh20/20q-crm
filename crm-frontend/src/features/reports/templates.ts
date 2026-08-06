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
    description: 'Open deal value in each stage, across every pipeline.',
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
  // R9.3 gave the deal catalog a `pipeline` field, and the multi-board org needs
  // it — but as a SECOND template, not as a filter on the one above.
  //
  // A filter would have to name a pipeline by id, and these are static frontend
  // presets: there is no org to read one from at the time this array is written,
  // and a rule with an empty value is a broken report rather than a scoped one.
  // Grouping needs no such value. The two then answer the two different questions
  // an org with several boards actually has — "how is each board doing" and, once
  // one is picked in the builder, "where inside it is the value sitting".
  //
  // The by-stage template above is left unscoped deliberately: the backend
  // qualifies stage labels with their board name ("Discovery (Enterprise)"), so
  // it reads as a legible all-boards view rather than duplicate bars.
  {
    id: 'open-value-by-pipeline',
    name: 'Open Value by Pipeline',
    description: 'Which board your open value is sitting on.',
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
      group_by: { field: 'pipeline' },
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
