// The merge-tag catalog for the composer's "insert variable" picker, keyed by the
// campaign's declared merge scope. Mirrors the backend's known-field sets
// (content_validate.go). Custom fields (contact.custom_fields.<key>) are org-defined
// and not enumerable here — authors type those directly; the backend validates them.

export interface VariableField {
  path: string;
  label: string;
}
export interface VariableGroup {
  key: string;
  label: string;
  fields: VariableField[];
}

const CATALOG: Record<string, VariableGroup> = {
  contact: {
    key: 'contact',
    label: 'Contact',
    fields: [
      { path: 'contact.first_name', label: 'First name' },
      { path: 'contact.last_name', label: 'Last name' },
      { path: 'contact.name', label: 'Full name' },
      { path: 'contact.email', label: 'Email' },
      { path: 'contact.phone', label: 'Phone' },
    ],
  },
  company: {
    key: 'company',
    label: 'Company',
    fields: [
      { path: 'company.name', label: 'Company name' },
      { path: 'company.industry', label: 'Industry' },
      { path: 'company.website', label: 'Website' },
    ],
  },
  org: {
    key: 'org',
    label: 'Workspace',
    fields: [{ path: 'org.name', label: 'Workspace name' }],
  },
  campaign: {
    key: 'campaign',
    label: 'Campaign',
    fields: [
      { path: 'campaign.name', label: 'Campaign name' },
      { path: 'campaign.unsubscribe_url', label: 'Unsubscribe URL' },
    ],
  },
};

/** GUARANTEED_PATHS never render blank, so the composer doesn't force a fallback on
 *  them (mirrors the backend's guaranteed-exempt set). */
export const GUARANTEED_PATHS = new Set(['contact.email', 'org.name']);

export function isGuaranteed(path: string): boolean {
  return GUARANTEED_PATHS.has(path);
}

/** variableGroupsForScope returns the pickable variable groups for a declared scope. */
export function variableGroupsForScope(scope: string[]): VariableGroup[] {
  return scope.map((r) => CATALOG[r]).filter((g): g is VariableGroup => !!g);
}

/** ALL_SCOPES is the full set a campaign may declare (contact/org/campaign default,
 *  +company optional). Matches the backend validMergeRoots. */
export const SELECTABLE_SCOPES: { root: string; label: string; fixed?: boolean }[] = [
  { root: 'contact', label: 'Contact', fixed: true },
  { root: 'org', label: 'Workspace', fixed: true },
  { root: 'campaign', label: 'Campaign', fixed: true },
  { root: 'company', label: 'Company (contact’s company)' },
];
