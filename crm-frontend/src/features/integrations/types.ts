// Types for the lead-integration platform. These mirror the Go structs in
// crm-backend/internal/integrations/models.go — keep the JSON keys in step.

export type LeadSourceStatus = 'active' | 'disabled' | 'error';

/** What an inbound lead may do to a contact that already exists. */
export type UpdatePolicy = 'fill_blank_only' | 'overwrite' | 'create_only';

export const UPDATE_POLICY_LABELS: Record<UpdatePolicy, string> = {
  fill_blank_only: 'Only fill empty fields',
  overwrite: 'Overwrite with the newest submission',
  create_only: 'Never update existing contacts',
};

export const UPDATE_POLICY_HELP: Record<UpdatePolicy, string> = {
  fill_blank_only:
    "Safest. A rep's corrections are never overwritten when someone resubmits a form.",
  overwrite: 'The newest submission wins every mapped field, replacing what is there.',
  create_only: 'Re-submissions are logged and otherwise ignored.',
};

/** One inbound channel: a credential, where its leads land, and how they merge. */
export interface LeadSource {
  id: string;
  org_id: string;
  kind: string;
  name: string;
  /** A recognizable hint ("crm_lead_a1b2…") — never the usable key. */
  token_prefix: string;
  target_slug: string;
  match_fields: string[];
  field_map: Record<string, unknown>;
  update_policy: UpdatePolicy;
  default_owner_id?: string;
  config: Record<string, unknown>;
  status: LeadSourceStatus;
  consecutive_failures: number;
  last_used_at?: string;
  daily_cap: number;
  created_by?: string;
  created_at: string;
  updated_at: string;
  disabled_at?: string;
}

/**
 * A source plus its plaintext key, returned ONLY by create and rotate.
 *
 * Split from LeadSource deliberately — the same split the API tokens use — so the
 * type system makes it awkward to put a one-time secret anywhere it would persist
 * (a query cache, a URL, a log). The key lives in component state and dies with it.
 */
export interface CreatedLeadSource {
  source: LeadSource;
  plaintext_key: string;
}

export type EventStatus =
  | 'pending'
  | 'processing'
  | 'processed'
  | 'duplicate'
  | 'failed'
  | 'quarantined'
  | 'test';

export type EventOutcome = 'created' | 'updated';

/** One inbound delivery: what arrived, what happened, and what it became. */
export interface IntegrationEvent {
  id: string;
  org_id: string;
  source_id?: string;
  connection_id?: string;
  provider_event_id?: string;
  status: EventStatus;
  claimed_at?: string;
  attempts: number;
  raw_payload: Record<string, unknown>;
  context: Record<string, unknown>;
  /** Keys the payload carried that were recorded but deliberately NOT written. */
  quarantined_fields: Record<string, unknown>;
  result_slug?: string;
  result_record_id?: string;
  outcome?: EventOutcome;
  error?: string;
  /** A judgement the pipeline made on a delivery that SUCCEEDED (e.g. refusing to
   *  merge into a phone shared by several contacts). Not an error — rendering it
   *  as one would read as a failure, which is the opposite of what it is. */
  note?: string;
  created_at: string;
  processed_at?: string;
}

export interface CreateSourceInput {
  name: string;
  kind?: string;
  target_slug?: string;
  update_policy?: UpdatePolicy;
  default_owner_id?: string | null;
  daily_cap?: number;
}

export interface UpdateSourceInput {
  name?: string;
  update_policy?: UpdatePolicy;
  default_owner_id?: string | null;
  daily_cap?: number;
  status?: LeadSourceStatus;
}

/** A transform applied to an inbound value before it is written. */
export type Transform = '' | 'split_name' | 'lower' | 'trim';

export const TRANSFORM_LABELS: Record<Transform, string> = {
  '': 'Use as-is',
  split_name: 'Split into first + last name',
  lower: 'Lowercase it',
  trim: 'Trim spaces',
};

export interface FieldMapEntry {
  target_key: string;
  transform?: Transform;
}

/** source_key -> entry. An EMPTY map means identity: keys pass through unchanged. */
export type FieldMap = Record<string, FieldMapEntry>;

export interface MappingTarget {
  key: string;
  label: string;
  type: string;
}

/** Everything the mapping screen needs, in one call. */
export interface MappingView {
  /** The real keys this source has actually sent, read off the delivery log. */
  observed: string[];
  /** The fields a lead may be written into (ownership/relations never appear). */
  target_fields: MappingTarget[];
  field_map: FieldMap;
}
