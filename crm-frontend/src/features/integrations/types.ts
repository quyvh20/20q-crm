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

/** Source kinds. `api` is the generic capture key; `google_ads` is the webhook an
 *  advertiser pastes into Google's lead-form editor. */
export const KIND_LABELS: Record<string, string> = {
  api: 'Capture API',
  google_ads: 'Google Ads',
  form_embed: 'Website form',
  facebook_form: 'Facebook form',
};

/** One field a form_embed source collects. */
export interface FormField {
  name: string;
  label: string;
  type: string;
  required: boolean;
}

/** A form_embed source's definition. Lives in config.form. */
export interface FormConfig {
  enabled: boolean;
  fields?: FormField[];
  /** The invisible field a bot fills and a human never sees. */
  honeypot?: string;
  thank_you?: string;
  /** The PUBLIC half of a Turnstile pair — it goes in the page, so it lives here.
   *  The secret half is write-only and never returned. */
  turnstile_site_key?: string;
}

export const FORM_FIELD_TYPES = ['text', 'email', 'tel', 'textarea'] as const;

export function kindLabel(kind: string): string {
  return KIND_LABELS[kind] ?? kind;
}

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
  /** The single owner, and the fallback when a rotation is configured but nobody in
   *  it is available. */
  default_owner_id?: string;
  /** The rotation, in order — order IS the rotation, so it is rendered and editable. */
  owner_pool: string[];
  /** Pool members who can no longer receive leads, computed SERVER-side. Never
   *  derive this by intersecting with a member list: a failed member fetch would
   *  then badge a healthy rotation as dead. */
  owner_pool_inactive?: string[];
  /** Whether BATCH deliveries may trigger workflows. Off by default: 100 recovered
   *  leads would otherwise enrol 100 contacts into every contact_created workflow. */
  batch_enroll_automation: boolean;
  /** The google_ads webhook URL's source identifier. Not a secret (the google_key
   *  is) — but only present on google_ads sources. */
  public_token?: string;
  /** Source-scoped settings that are not columns, one key per feature. */
  config: { deal?: DealConfig; form?: FormConfig } & Record<string, unknown>;
  /** Browser origins a form_embed source accepts submissions from. EMPTY denies
   *  every browser — the deliberate default for a new form. */
  allowed_origins?: string[];
  /** Whether a Turnstile secret is set. The key itself is never returned. */
  turnstile_configured?: boolean;
  /** The configured deal stage has been deleted since it was chosen. Computed
   *  SERVER-side, like owner_pool_inactive — deriving it here would badge a healthy
   *  source the moment a stage fetch failed. */
  deal_stage_missing?: boolean;
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
 * The per-source "also create a deal" option.
 *
 * A deal is created only when the lead produces a NEW contact. That is not a
 * simplification — the daily cap counts contact creates, so a deal on the matched
 * path would have no backstop at all. A returning customer is matched, logged, and
 * the delivery says so.
 */
export interface DealConfig {
  enabled: boolean;
  /** Which stage new deals start in. Won/lost stages are refused: deal creation
   *  does not derive is_won/is_lost, so one would sit in the won column reporting
   *  the opposite. */
  stage_id?: string;
  /** Title template over a CLOSED token vocabulary — see DEAL_NAME_TOKENS. */
  name_template?: string;
}

/**
 * The tokens a deal name template may use. Closed on purpose: an open vocabulary
 * would let a title reference fields the admin cannot read, and a deal title
 * outlives an erasure request. Mirrors dealNameTokens in the Go package.
 */
export const DEAL_NAME_TOKENS = [
  'full_name',
  'first_name',
  'last_name',
  'email',
  'company',
  'source_name',
  'date',
] as const;

export const DEFAULT_DEAL_NAME_TEMPLATE = '{{full_name}} — {{source_name}}';

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
  /** The second one-time secret a google_ads source carries — the value pasted
   *  beside the webhook URL in Google's form editor. Never cached; lives in
   *  component state and dies with it, exactly like plaintext_key. */
  google_key?: string;
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

/**
 * What the SERVER decided may be done with a delivery. The client renders this
 * verdict; it never computes one. Retry guards a callerless contact write, so the
 * decision belongs where the guards are.
 */
export type RetryMode = 'refetch' | 'none';

export interface RetryPlan {
  mode: RetryMode;
  reason?: string;
}

/** One page of the org-wide ledger. */
export interface EventPage {
  events: IntegrationEvent[];
  next_cursor?: string;
  /** Source id -> label, INCLUDING soft-deleted sources so no row shows a bare uuid. */
  sources: Record<string, { name: string; kind: string; deleted: boolean }>;
}

export interface EventLogFilters {
  source_id?: string;
  connection_id?: string;
  status?: EventStatus[];
  unresolved?: boolean;
  cursor?: string;
  limit?: number;
}

/** One inbound delivery: what arrived, what happened, and what it became. */
export interface IntegrationEvent {
  id: string;
  org_id: string;
  source_id?: string;
  connection_id?: string;
  provider_event_id?: string;
  status: EventStatus;
  /** Present only on the org-wide ledger route, which computes it server-side. */
  retry?: RetryPlan;
  /**
   * Set when this delivery's personal data has been removed — by an erasure request,
   * or by retention once nothing could reach it on request. Without it an empty
   * payload is ambiguous: erased and never-stored render identically, and the second
   * is what a bug looks like.
   */
  redacted_at?: string;
  claimed_at?: string;
  attempts: number;
  raw_payload: Record<string, unknown>;
  context: Record<string, unknown>;
  /** Keys the payload carried that were recorded but deliberately NOT written. */
  quarantined_fields: Record<string, unknown>;
  /** The object the delivery wrote — 'contact' today. The delivery log links
   *  through this, never a hardcoded path. */
  result_slug?: string;
  result_record_id?: string;
  outcome?: EventOutcome;
  error?: string;
  /** A judgement the pipeline made on a delivery that SUCCEEDED (e.g. refusing to
   *  merge into a phone shared by several contacts). Not an error — rendering it
   *  as one would read as a failure, which is the opposite of what it is. */
  note?: string;
  /** The verbatim consent envelope this delivery carried, if any. RECORDED, not
   *  enforced — nothing in the app consults it before sending. Absent when none was
   *  sent; carries `_crm.redacted` once the contact has been deleted. */
  consent?: Record<string, unknown> | null;
  created_at: string;
  processed_at?: string;
}

/**
 * What one "Send test lead" click produced.
 *
 * `uncovered` is as load-bearing as the rest: a result that lists only successes
 * reads as "everything works", and the test deliberately cannot exercise every field
 * (no phone is ever sent; a select/number target gets no guessed value).
 */
export interface TestLeadResult {
  record_id: string;
  event_id: string;
  outcome: EventOutcome;
  /** Payload keys recorded but not written. */
  quarantined?: string[];
  /** A judgement the pipeline made on a delivery that still succeeded. */
  note?: string;
  /** Fields this test could not exercise, and why — rendered, never swallowed. */
  uncovered?: string[];
  /** So the UI can warn that a disabled source rejects real traffic right now, even
   *  though this test — which does not use the capture key — just succeeded. */
  source_status: LeadSourceStatus;
  /** The rep this test lead landed on. Empty means unowned, which is itself the
   *  finding — the panel claims the test proves owner assignment, so it shows the
   *  answer rather than asserting the category. */
  assigned_owner_id?: string;
  /** Routing problems (an unowned lead) said to the admin who clicked. */
  warnings?: string[];
}

export interface CreateSourceInput {
  name: string;
  kind?: string;
  target_slug?: string;
  update_policy?: UpdatePolicy;
  default_owner_id?: string | null;
  /** An explicit [] turns a rotation off; omitting the key leaves it alone. */
  owner_pool?: string[];
  /** An explicit 0 CLEARS the cap; omitting the key leaves it alone. */
  daily_cap?: number;
}

export interface UpdateSourceInput {
  name?: string;
  update_policy?: UpdatePolicy;
  default_owner_id?: string | null;
  /** An explicit [] turns a rotation off; omitting the key leaves it alone. */
  owner_pool?: string[];
  /** An explicit 0 CLEARS the cap; omitting the key leaves it alone. */
  daily_cap?: number;
  batch_enroll_automation?: boolean;
  /** Omitting the key leaves the option alone; sending one replaces it wholesale. */
  deal?: DealConfig;
  form?: FormConfig;
  /** An explicit [] denies every browser origin. */
  allowed_origins?: string[];
  /** Write-only: never returned, and "" clears it. */
  turnstile_secret?: string;
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

// ── Provider connections (L5.2) ──────────────────────────────────────────────
// Mirror crm-backend/internal/integrations connections.go / provider.go.

/** A provider the deployment can connect (has a shipped adapter AND is configured,
 *  AND provider-credential encryption is set up — the backend omits providers the
 *  codec cannot serve, so an empty list means "no connect button to show"). */
export interface ProviderInfo {
  key: string;
  label: string;
  supports_webhooks: boolean;
  uses_pkce: boolean;
}

export type ConnectionStatus = 'connected' | 'degraded' | 'error' | 'revoked' | 'disconnected';

/** One OAuth'd provider account (a Facebook page). Never carries a token — the
 *  backend view type has no credential field. */
export interface Connection {
  id: string;
  provider: string;
  external_account_id: string;
  external_account_label: string;
  status: ConnectionStatus;
  /** Whether provider-side delivery is active (a page subscribed to leadgen). A
   *  connection can be `connected` yet unsubscribed — healthy-looking but silent. */
  subscribed: boolean;
  last_error?: string;
  consecutive_failures: number;
  created_at: string;
  updated_at: string;
  last_synced_at?: string;
}

/** A candidate account in the picker — id + label only, never a token. */
export interface AccountChoice {
  id: string;
  label: string;
  meta?: Record<string, unknown>;
}

/** The account-picker payload: the provider plus its token-free candidates. */
export interface PendingCandidates {
  provider: string;
  accounts: AccountChoice[];
}

/** One provider lead form in the enable-a-form picker (L5.4), with whether it is
 *  already enabled (has a facebook_form source) and that source's id. */
export interface ProviderForm {
  id: string;
  name: string;
  status?: string;
  enabled: boolean;
  source_id?: string;
}


/** One layer of the per-connection diagnose (L6.3). */
export type DiagnoseCheckKey = 'credentials' | 'token' | 'subscription' | 'forms';
export type DiagnoseCheckStatus = 'ok' | 'fail' | 'warn' | 'unknown' | 'skipped';

export interface DiagnoseCheck {
  key: DiagnoseCheckKey;
  status: DiagnoseCheckStatus;
}

export interface DiagnoseResult {
  checks: DiagnoseCheck[];
  healthy: boolean;
}


/** One day of deliveries for a source (L6.6). */
export interface DailyIngestCount {
  day: string;
  written: number;
  failed: number;
  skipped: number;
}
