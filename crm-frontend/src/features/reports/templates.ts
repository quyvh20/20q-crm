import type { ReportConfig } from '../../lib/api';

// Prebuilt report templates: frontend-only config presets that prefill the
// builder (no DB seeding — picking one just navigates to /reports/new with
// this config).
//
// Every object here carries an OLS row, so picking a template the caller's role
// is denied fails at preview like any other report. As of R9.5 that includes
// task and activity, which are report-only objects (no record page, no nav) but
// real OLS subjects — see domain/report_objects.go.
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
  // R9.5 — the two rep-productivity reports the whole phase exists for.
  {
    id: 'activities-per-rep',
    name: 'Activities per Rep',
    description: 'Calls, emails, meetings and notes logged by each rep.',
    objectSlug: 'activity',
    config: {
      chart: 'bar',
      // stage_change activities are written by the deal pipeline, not by a
      // person — including them would rank reps by how often their deals moved
      // and, since three writers leave user_id NULL, pile most of them into a
      // single "(No value)" bar.
      filters: { op: 'AND', rules: [{ field: 'type', operator: 'neq', value: 'stage_change' }] },
      group_by: { field: 'user_id' },
      aggregate: { fn: 'count' },
    },
  },
  {
    id: 'tasks-completed-per-rep',
    name: 'Tasks Completed per Rep',
    description: 'Who is closing out their follow-ups.',
    objectSlug: 'task',
    config: {
      chart: 'bar',
      // completed_at is a nullable timestamp, not a boolean — "completed" is
      // "has one".
      filters: { op: 'AND', rules: [{ field: 'completed_at', operator: 'is_not_empty' }] },
      group_by: { field: 'assigned_to' },
      aggregate: { fn: 'count' },
    },
  },
];
