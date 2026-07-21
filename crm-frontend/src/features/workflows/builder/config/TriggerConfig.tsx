import React, { useMemo, useCallback, useState, useEffect, useRef } from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { Link } from 'react-router-dom';
import type { TriggerSpec } from '../../types';
import { useBuilderStore } from '../../store';
import { getWebhookToken, revealWebhookSecret, regenerateWebhookSecret } from '../../api';
import type { WorkflowSchema, WebhookTokenInfo } from '../../api';
import type { FiresOn } from '../../useSchema';
import { StageDropdown } from './inputs';
import { ScheduleConfig } from './ScheduleConfig';
import { DateFieldConfig } from './DateFieldConfig';
import { DEFAULT_CRON, browserTimeZone } from '../../cron';
import { DEFAULT_AT_TIME } from '../../dateField';
import Modal from '../../../../components/common/Modal';

// ============================================================
// Source Panel — Step 1
// Object dropdown + Fires-on selector
// ============================================================

const FIRES_ON_OPTIONS: { value: FiresOn; label: string }[] = [
  { value: 'created', label: 'Created' },
  { value: 'updated', label: 'Updated' },
  { value: 'deleted', label: 'Deleted' },
  { value: 'any', label: 'Any' },
];

// Entities from schema.entities that are NOT triggerable (e.g., template variable sources)
const NON_TRIGGERABLE_ENTITIES = new Set(['trigger']);

interface EntityOption {
  key: string;
  label: string;
  icon: string;
}

function buildEntityList(schema: WorkflowSchema | null): EntityOption[] {
  const entities: EntityOption[] = [];

  if (schema) {
    for (const ent of schema.entities) {
      if (NON_TRIGGERABLE_ENTITIES.has(ent.key)) continue;
      entities.push({ key: ent.key, label: ent.label, icon: ent.icon || '📦' });
    }
    for (const obj of (schema.custom_objects || [])) {
      entities.push({ key: obj.key, label: obj.label, icon: obj.icon || '📦' });
    }
  } else {
    entities.push(
      { key: 'contact', label: 'Contact', icon: '👤' },
      { key: 'deal', label: 'Deal', icon: '💰' },
    );
  }

  entities.push({ key: 'schedule', label: 'Schedule', icon: '⏰' });
  entities.push({ key: 'date_field', label: 'Date reached', icon: '📅' });
  entities.push({ key: 'webhook', label: 'Webhook', icon: '🔗' });
  return entities;
}

// --- Parse existing trigger into object + firesOn ---
function parseTrigger(trigger: TriggerSpec): { object: string; firesOn: FiresOn } {
  const t = trigger.type;

  // Built-in special types
  if (t === 'deal_stage_changed') return { object: 'deal', firesOn: 'updated' };
  if (t === 'no_activity_days') {
    const ent = (trigger.params?.entity as string) || 'contact';
    return { object: ent, firesOn: 'any' };
  }
  if (t === 'webhook_inbound') return { object: 'webhook', firesOn: 'any' };
  if (t === 'schedule') return { object: 'schedule', firesOn: 'any' };
  if (t === 'date_field') return { object: 'date_field', firesOn: 'any' };

  // Dynamic pattern: {slug}_{event}
  for (const suffix of ['_created', '_updated', '_deleted', '_any'] as const) {
    if (t.endsWith(suffix)) {
      const slug = t.slice(0, -suffix.length);
      const firesOn = suffix.slice(1) as FiresOn;
      return { object: slug, firesOn };
    }
  }

  return { object: '', firesOn: 'created' };
}

// --- Build TriggerSpec from object + firesOn + optional stage params ---
function buildTriggerSpec(object: string, firesOn: FiresOn, params?: Record<string, unknown>): TriggerSpec {
  if (object === 'webhook') {
    return { type: 'webhook_inbound', params: { source: 'custom' } };
  }
  if (object === 'schedule') {
    // Seed a sensible default (every Monday 9am, viewer's tz); preserve params when
    // re-selecting schedule so an existing cron/timezone isn't reset.
    const cron = (params?.cron as string) || DEFAULT_CRON;
    const timezone = (params?.timezone as string) || browserTimeZone();
    return { type: 'schedule', params: { cron, timezone } };
  }
  if (object === 'date_field') {
    // Object/field are chosen inside DateFieldConfig (auto-defaulted to the first
    // date field). Preserve params when re-selecting so an existing config isn't reset.
    return {
      type: 'date_field',
      params: {
        object: (params?.object as string) || '',
        field: (params?.field as string) || '',
        offset_days: (params?.offset_days as number) ?? 0,
        at_time: (params?.at_time as string) || DEFAULT_AT_TIME,
        timezone: (params?.timezone as string) || browserTimeZone(),
      },
    };
  }
  // deal + updated → deal_stage_changed (the only deal-update trigger the backend supports)
  if (object === 'deal' && firesOn === 'updated') {
    return { type: 'deal_stage_changed', params };
  }
  return { type: `${object}_${firesOn}`, params };
}

// --- Select styling ---
const selectClass =
  'bg-background border border-border rounded-lg px-3 py-1.5 text-sm text-foreground font-medium focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring cursor-pointer appearance-none transition-colors hover:border-muted-foreground/40';

// ============================================================
// Main Component
// ============================================================

export const TriggerConfig: React.FC = () => {
  const { trigger, setTrigger, schema, errors } = useBuilderStore();

  const entityList = useMemo(() => buildEntityList(schema), [schema]);

  const { object, firesOn } = useMemo(() => {
    if (!trigger) return { object: '', firesOn: 'created' as FiresOn };
    return parseTrigger(trigger);
  }, [trigger]);

  const entityLabel = useMemo(() => {
    const e = entityList.find((e) => e.key === object);
    return e?.label || object || '…';
  }, [object, entityList]);

  const handleObjectChange = (newObject: string) => {
    const currentFiresOn = firesOn || 'created';
    setTrigger(buildTriggerSpec(newObject, currentFiresOn));
  };

  const handleFiresOnChange = (newFiresOn: FiresOn) => {
    if (!object) return;
    // Preserve existing stage params if staying on deal
    const currentParams = trigger?.params || {};
    setTrigger(buildTriggerSpec(object, newFiresOn, currentParams));
  };

  // --- Deal Stage Changed: detect and expose stage params ---
  const isDealStageChanged = trigger?.type === 'deal_stage_changed';
  const stages = schema?.stages || [];

  const fromStage = (trigger?.params?.from_stage as string) || '';
  const toStage = (trigger?.params?.to_stage as string) || '';

  const handleFromStageChange = useCallback((val: string) => {
    if (!trigger) return;
    const params = { ...(trigger.params || {}), from_stage: val };
    setTrigger({ ...trigger, params });
  }, [trigger, setTrigger]);

  const handleToStageChange = useCallback((val: string) => {
    if (!trigger) return;
    const params = { ...(trigger.params || {}), to_stage: val };
    setTrigger({ ...trigger, params });
  }, [trigger, setTrigger]);

  // For webhook, schedule, date_field, and deal_stage_changed, firesOn is not relevant
  const showFiresOn =
    object && object !== 'webhook' && object !== 'schedule' && object !== 'date_field' && !isDealStageChanged;

  // Fires-on label for preview
  const firesOnLabel = FIRES_ON_OPTIONS.find((o) => o.value === firesOn)?.label?.toLowerCase() || firesOn;

  // Inline errors
  const objectError = errors['trigger.object']?.[0];
  const firesOnError = errors['trigger.firesOn']?.[0];
  const toStageError = errors['trigger.params.to_stage']?.[0];

  // Edge case: no objects with read access
  const hasNoObjects = entityList.length === 0;

  // Edge case: selected object no longer in entity list (permission downgrade)
  const objectOrphaned = object && !entityList.some((e) => e.key === object);

  return (
    <div className="space-y-4">
      <h3 className="text-lg font-semibold text-foreground">Source</h3>
      <p className="text-xs text-muted-foreground -mt-2">Choose which object triggers this workflow.</p>

      {/* Empty state: no readable objects */}
      {hasNoObjects && (
        <div className="p-4 rounded-xl border border-dashed border-border bg-muted/40 text-center space-y-2">
          <p className="text-sm text-muted-foreground">No objects available</p>
          <p className="text-xs text-muted-foreground">You don't have read access to any objects. Contact your admin to get access.</p>
        </div>
      )}

      {/* Permission downgrade warning */}
      {objectOrphaned && (
        <div className="flex items-start gap-2 p-2.5 rounded-lg bg-amber-500/10 border border-amber-500/30">
          <span className="text-amber-600 dark:text-amber-400 text-sm mt-0.5">⚠</span>
          <div>
            <p className="text-xs text-amber-600 dark:text-amber-400 font-medium">Object no longer accessible</p>
            <p className="text-[11px] text-amber-600/70 dark:text-amber-400/70 mt-0.5">
              The object "{object}" is no longer in your readable objects. Select a different object or contact your admin.
            </p>
          </div>
        </div>
      )}

      <div className="p-3 rounded-xl border border-border/60 bg-muted/40 space-y-3">
        {/* Object dropdown */}
        <div className="space-y-1.5">
          <label className="text-xs text-muted-foreground font-medium uppercase tracking-wider">Object</label>
          <div className="relative">
            <select
              value={object || ''}
              onChange={(e) => handleObjectChange(e.target.value)}
              className={`${selectClass} w-full ${objectError || objectOrphaned ? '!border-destructive' : ''}`}
              style={{ paddingRight: '2rem' }}
              disabled={hasNoObjects}
            >
              <option value="" disabled>Select object…</option>
              {/* Show orphaned object as a disabled option so UI doesn't break */}
              {objectOrphaned && (
                <option value={object} disabled>⚠ {object} (no longer accessible)</option>
              )}
              {entityList.map((e) => (
                <option key={e.key} value={e.key}>{e.icon} {e.label}</option>
              ))}
            </select>
            <ChevronDown className="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
          </div>
          {objectError && (
            <p className="text-[11px] text-destructive mt-0.5">⚠ {objectError}</p>
          )}
        </div>

        {/* Deal Stage Changed: From / To stage pickers */}
        {isDealStageChanged && (
          <div className="p-3 rounded-xl border border-border/60 bg-muted/40 space-y-3 mt-3">
            <p className="text-xs text-muted-foreground font-medium">Stage Transition Filter</p>

            {/* From Stage */}
            <div className="space-y-1.5">
              <label className="text-xs text-muted-foreground font-medium uppercase tracking-wider">From Stage</label>
              <StageDropdown
                stages={stages}
                value={fromStage}
                onChange={handleFromStageChange}
                allowAny
                placeholder="Select from stage…"
              />
              <p className="text-[10px] text-muted-foreground/70">Only trigger when the deal moves <em>from</em> this stage. "Any" matches all.</p>
            </div>

            {/* To Stage */}
            <div className="space-y-1.5">
              <label className="text-xs text-muted-foreground font-medium uppercase tracking-wider">To Stage</label>
              <StageDropdown
                stages={stages}
                value={toStage}
                onChange={handleToStageChange}
                placeholder="Select target stage…"
              />
              {toStageError && (
                <p className="text-[11px] text-destructive mt-0.5">⚠ {toStageError}</p>
              )}
              <p className="text-[10px] text-muted-foreground/70">Required — the stage the deal must move <em>to</em> for this trigger to fire.</p>
            </div>
          </div>
        )}

        {/* Schedule (cron) trigger form */}
        {object === 'schedule' && <ScheduleConfig />}

        {/* Date-field trigger form */}
        {object === 'date_field' && <DateFieldConfig />}

        {/* Fires on selector */}
        {showFiresOn && (
          <div className="space-y-1.5">
            <label className="text-xs text-muted-foreground font-medium uppercase tracking-wider">Fires on</label>
            <div className="flex gap-1">
              {FIRES_ON_OPTIONS.map((opt) => (
                <button
                  key={opt.value}
                  onClick={() => handleFiresOnChange(opt.value)}
                  className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-all duration-150 ${
                    firesOn === opt.value
                      ? 'bg-primary text-primary-foreground shadow-md shadow-primary/25'
                      : 'bg-background text-muted-foreground hover:bg-accent hover:text-accent-foreground'
                  }`}
                >
                  {opt.label}
                </button>
              ))}
            </div>
            {firesOnError && (
              <p className="text-[11px] text-destructive mt-0.5">⚠ {firesOnError}</p>
            )}
          </div>
        )}
      </div>

      {/* Webhook setup instructions (P17) — only for the inbound webhook trigger */}
      {object === 'webhook' && <WebhookSetup />}

      {/* Preview sentence — schedule/date_field render their own preview inline */}
      {object && object !== 'schedule' && object !== 'date_field' && (
        <div className="px-3 py-2 rounded-lg bg-primary/5 border border-primary/20">
          <p className="text-xs text-primary/70">
            <span className="text-primary font-medium">Preview: </span>
            {isDealStageChanged
              ? (() => {
                  const fromLabel = fromStage === '*'
                    ? 'any stage'
                    : stages.find((s) => s.id === fromStage)?.name || (fromStage || '…');
                  const toLabel = toStage === '*'
                    ? 'any stage'
                    : stages.find((s) => s.id === toStage)?.name || (toStage || '…');
                  return `When a Deal moves from ${fromLabel} → ${toLabel}`;
                })()
              : object === 'webhook'
                ? `When a Webhook receives data`
                : `When a ${entityLabel} is ${firesOnLabel}`}
          </p>
        </div>
      )}
    </div>
  );
};

// ============================================================
// Webhook Setup (P17)
// Shown when the trigger object is "webhook". Fetches the org's inbound URL +
// MASKED signing secret, and lets an admin rotate the secret. The full secret is
// only ever held client-side immediately after a rotation (reveal-once), which is
// also the only time the curl example can carry a real, runnable signature.
// ============================================================

// Canonical example body. The SAME string is both signed and sent in the curl
// example, so the precomputed signature stays valid (signing a pretty-printed
// variant would not match the bytes curl transmits).
const WEBHOOK_EXAMPLE_BODY = '{"email":"jane@example.com","first_name":"Jane","last_name":"Doe"}';

async function hmacSha256Hex(message: string, secret: string): Promise<string> {
  const enc = new TextEncoder();
  const key = await crypto.subtle.importKey(
    'raw',
    enc.encode(secret),
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign'],
  );
  const sig = await crypto.subtle.sign('HMAC', key, enc.encode(message));
  return Array.from(new Uint8Array(sig))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
}

// Mirror of the backend maskSecret (12 bullets + last 4) so the masked display can
// update locally right after a rotation without an extra round-trip.
function maskWebhookSecret(secret: string): string {
  if (secret.length <= 4) return '•'.repeat(secret.length);
  return '•'.repeat(12) + secret.slice(-4);
}

// Module-level so it isn't re-created on every WebhookSetup render.
const CopyButton: React.FC<{ active: boolean; onClick: () => void; label?: string }> = ({ active, onClick, label }) => (
  <button
    type="button"
    onClick={onClick}
    aria-label={label}
    title={label}
    className="shrink-0 px-2 py-1 rounded-md bg-muted text-[11px] text-foreground hover:bg-accent hover:text-accent-foreground transition-colors"
  >
    {active ? '✓ Copied' : 'Copy'}
  </button>
);

// Rotating caret for the collapsible sub-sections (curl / payload fields).
const DisclosureCaret: React.FC<{ open: boolean }> = ({ open }) => (
  <ChevronRight
    className={`h-3 w-3 shrink-0 transition-transform ${open ? 'rotate-90' : ''}`}
    aria-hidden="true"
  />
);

// Modal confirm for the destructive secret rotation. Module-level (not redefined
// per render). Mirrors the app's dialog convention (see RunNowModal).
const RegenerateConfirmDialog: React.FC<{
  busy: boolean;
  error: string | null;
  onConfirm: () => void;
  onCancel: () => void;
}> = ({ busy, error, onConfirm, onCancel }) => (
  // Shared Radix modal (U7) — this destructive confirm previously had no Escape,
  // no focus trap and no close affordance at all. onCancel already no-ops while
  // busy, so every dismissal path inherits that guard.
  <Modal
    open
    onClose={onCancel}
    title="Regenerate signing secret?"
    size="md"
    dismissable={!busy}
  >
    <div className="space-y-3">
      <p className="text-[12px] text-muted-foreground leading-relaxed">
        Existing webhook integrations will break until updated with the new secret. Confirm?
      </p>
      {error && <p role="alert" className="text-[12px] text-destructive">⚠ {error}</p>}
      <div className="flex justify-end gap-2 pt-1">
        <button
          type="button"
          onClick={onCancel}
          disabled={busy}
          className="px-3 py-1.5 rounded-lg bg-muted text-xs text-foreground hover:bg-accent hover:text-accent-foreground transition-colors disabled:opacity-60"
        >
          Cancel
        </button>
        <button
          type="button"
          onClick={onConfirm}
          disabled={busy}
          className="px-3 py-1.5 rounded-lg bg-destructive text-xs text-destructive-foreground font-medium hover:bg-destructive/90 transition-colors disabled:opacity-60"
        >
          {busy ? 'Rotating…' : 'Regenerate secret'}
        </button>
      </div>
    </div>
  </Modal>
);

const WebhookSetup: React.FC = () => {
  const [info, setInfo] = useState<WebhookTokenInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState<string | null>(null);
  // Full secret, held only while it's shown (reveal or just-rotated). Never cached
  // across hides — `hideSecret` clears it. Auto-hides after 30s.
  const [fullSecret, setFullSecret] = useState<string | null>(null);
  const [revealed, setRevealed] = useState(false);
  const [secretBusy, setSecretBusy] = useState(false);
  const [revealError, setRevealError] = useState<string | null>(null);
  const [sig, setSig] = useState<string | null>(null);
  const [curlOpen, setCurlOpen] = useState(false);
  const [fieldsOpen, setFieldsOpen] = useState(false);
  const [showRegenDialog, setShowRegenDialog] = useState(false);
  const [regenerating, setRegenerating] = useState(false);
  const [regenError, setRegenError] = useState<string | null>(null);
  const hideTimerRef = useRef<number | null>(null);

  // Fetch (and lazily provision) the org's webhook token on mount. `loading`
  // starts true, so the spinner shows without a synchronous setState here.
  useEffect(() => {
    let alive = true;
    getWebhookToken()
      .then((data) => { if (alive) { setInfo(data); setError(null); } })
      .catch((e) => { if (alive) setError(e instanceof Error ? e.message : 'Failed to load webhook setup'); })
      .finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, []);

  // While the secret is shown, compute a real signature so the curl example is
  // copy-paste runnable. It clears with the secret on hide → curl reverts to a
  // placeholder.
  useEffect(() => {
    if (!fullSecret || !crypto?.subtle) return;
    let alive = true;
    hmacSha256Hex(WEBHOOK_EXAMPLE_BODY, fullSecret)
      .then((s) => { if (alive) setSig(s); })
      .catch(() => { /* leave sig null → placeholder */ });
    return () => { alive = false; };
  }, [fullSecret]);

  // Clear any pending auto-hide timer when the panel unmounts.
  useEffect(() => () => { if (hideTimerRef.current) window.clearTimeout(hideTimerRef.current); }, []);

  const copy = useCallback((key: string, text: string) => {
    navigator.clipboard?.writeText(text);
    setCopied(key);
    window.setTimeout(() => setCopied((c) => (c === key ? null : c)), 1500);
  }, []);

  // Fetch the full secret from the server on demand. Deliberately NOT cached in
  // state: each reveal/copy is a fresh, explicit (auditable) call, so the plaintext
  // is held only while it's actually being shown.
  const fetchSecret = useCallback(async (): Promise<string> => {
    setSecretBusy(true);
    try {
      return await revealWebhookSecret();
    } finally {
      setSecretBusy(false);
    }
  }, []);

  // Hide the secret and drop it (and its derived signature) from memory.
  const hideSecret = useCallback(() => {
    if (hideTimerRef.current) window.clearTimeout(hideTimerRef.current);
    setRevealed(false);
    setFullSecret(null);
    setSig(null);
  }, []);

  // (Re)start the 30s window after which a revealed secret auto-hides.
  const scheduleHide = useCallback(() => {
    if (hideTimerRef.current) window.clearTimeout(hideTimerRef.current);
    hideTimerRef.current = window.setTimeout(hideSecret, 30000);
  }, [hideSecret]);

  const onReveal = useCallback(() => {
    setRevealError(null);
    fetchSecret()
      .then((s) => { setFullSecret(s); setRevealed(true); scheduleHide(); })
      .catch((e) => setRevealError(e instanceof Error ? e.message : 'Failed to reveal secret'));
  }, [fetchSecret, scheduleHide]);

  // Copy the full secret WITHOUT showing it on screen. The value lives only in
  // this local scope for the clipboard write — it is never stored in state.
  const onCopySecret = useCallback(() => {
    setRevealError(null);
    fetchSecret()
      .then((s) => copy('secret', s))
      .catch((e) => setRevealError(e instanceof Error ? e.message : 'Failed to copy secret'));
  }, [fetchSecret, copy]);

  const doRegenerate = useCallback(() => {
    setRegenerating(true);
    setRegenError(null);
    regenerateWebhookSecret()
      .then((res) => {
        setFullSecret(res.secret);
        setInfo((prev) => (prev ? { ...prev, token: res.token, url: res.url, secret_masked: maskWebhookSecret(res.secret) } : prev));
        setShowRegenDialog(false);
        setRevealed(true); // show the new secret so it can be copied; auto-hides in 30s
        scheduleHide();
      })
      .catch((e) => setRegenError(e instanceof Error ? e.message : 'Failed to regenerate secret'))
      .finally(() => setRegenerating(false));
  }, [scheduleHide]);

  if (loading) {
    return (
      <div className="p-3 rounded-xl border border-border/60 bg-muted/40">
        <p className="text-xs text-muted-foreground">Loading webhook setup…</p>
      </div>
    );
  }

  if (error || !info) {
    return (
      <div className="p-3 rounded-xl border border-amber-500/30 bg-amber-500/10">
        <p className="text-xs text-amber-600 dark:text-amber-400 font-medium">Couldn't load webhook setup</p>
        <p className="text-[11px] text-amber-600/70 dark:text-amber-400/70 mt-0.5">
          {error || 'No data returned.'} Webhook setup requires admin or manager access.
        </p>
      </div>
    );
  }

  const curl = [
    `curl -X POST '${info.url}' \\`,
    `  -H 'Content-Type: application/json' \\`,
    `  -H 'X-Signature: sha256=${sig ?? '<hmac-sha256-of-body>'}' \\`,
    `  -d '${WEBHOOK_EXAMPLE_BODY}'`,
  ].join('\n');

  return (
    <>
    <div className="p-3 rounded-xl border border-border/60 bg-muted/40 space-y-3">
      <div className="flex items-center gap-2">
        <span className="text-sm">🔗</span>
        <p className="text-xs text-foreground font-medium">Webhook Setup</p>
      </div>
      {/* The old copy here said "POST to this URL to trigger the workflow", and it was
          not true: the endpoint upserts a contact and emits contact_created /
          contact_updated, and workflow lookup matches on the trigger TYPE — so a
          workflow saved with this Webhook trigger is the one kind of workflow an
          inbound POST can never start. This panel is also the only place the URL and
          secret are shown, so the misdescription lived exactly where someone was
          setting the integration up. */}
      <p className="text-[11px] text-muted-foreground -mt-1">
        POST a contact to this URL to create or update it. Sign the request body with your secret using HMAC-SHA256 and send it as the <code className="text-muted-foreground">X-Signature</code> header.
      </p>
      <p className="text-[11px] text-amber-700 dark:text-amber-400 -mt-1">
        Deliveries here run <span className="font-medium">Contact created</span> and{' '}
        <span className="font-medium">Contact updated</span> workflows — not this one. Change this
        trigger to one of those to act on inbound leads.
      </p>
      <p className="text-[11px] text-muted-foreground -mt-1">
        For new integrations use{' '}
        <Link to="/settings/integrations" className="text-primary underline underline-offset-2">
          Settings → Integrations
        </Link>
        : per-source keys you can rotate individually, field mapping, owner routing and a
        delivery log. This endpoint is one shared key per workspace with none of that.
      </p>

      {/* Endpoint URL */}
      <div className="space-y-1">
        <label className="text-[10px] text-muted-foreground font-medium uppercase tracking-wider">Endpoint URL</label>
        <div className="flex items-center gap-2">
          <code className="flex-1 min-w-0 truncate px-2 py-1.5 rounded-md bg-muted border border-border text-[11px] text-primary font-mono" title={info.url}>{info.url}</code>
          <CopyButton active={copied === 'url'} onClick={() => copy('url', info.url)} label="Copy webhook URL" />
        </div>
        <p className="text-[10px] text-muted-foreground/70">This URL is permanent — it stays the same when you rotate the secret.</p>
      </div>

      {/* Signing secret: masked by default, reveal (auto-hide 30s) / copy / rotate */}
      <div className="space-y-1">
        <label className="text-[10px] text-muted-foreground font-medium uppercase tracking-wider">Signing Secret</label>
        <div className="flex items-center gap-2">
          <code className="flex-1 min-w-0 truncate px-2 py-1.5 rounded-md bg-muted border border-border text-[11px] text-amber-600 dark:text-amber-400 font-mono">
            {revealed && fullSecret ? fullSecret : info.secret_masked}
          </code>
          <button
            type="button"
            onClick={revealed ? hideSecret : onReveal}
            disabled={secretBusy}
            className="shrink-0 px-2 py-1 rounded-md bg-muted text-[11px] text-foreground hover:bg-accent hover:text-accent-foreground transition-colors disabled:opacity-60"
          >
            {revealed ? 'Hide' : 'Reveal'}
          </button>
          <CopyButton active={copied === 'secret'} onClick={onCopySecret} label="Copy signing secret" />
          <button
            type="button"
            onClick={() => { setRegenError(null); setShowRegenDialog(true); }}
            className="shrink-0 px-2 py-1 rounded-md bg-muted text-[11px] text-foreground hover:bg-accent hover:text-accent-foreground transition-colors"
          >
            ↻ Regenerate
          </button>
        </div>
        {revealed && (
          <p className="text-[10px] text-muted-foreground/70">Visible for 30 seconds — copy it now if you need it.</p>
        )}
        {revealError && (
          <p className="text-[10px] text-destructive">⚠ {revealError}</p>
        )}
      </div>

      {/* curl example (collapsible) — pre-filled with URL, sample body, signature */}
      <div className="space-y-1">
        <div className="flex items-center justify-between">
          <button
            type="button"
            onClick={() => setCurlOpen((o) => !o)}
            aria-expanded={curlOpen}
            className="flex items-center gap-1 text-[10px] text-muted-foreground font-medium uppercase tracking-wider hover:text-foreground transition-colors"
          >
            <DisclosureCaret open={curlOpen} />
            Test with curl
          </button>
          {curlOpen && (
            <CopyButton active={copied === 'curl'} onClick={() => copy('curl', curl)} label="Copy curl command" />
          )}
        </div>
        {curlOpen && (
          <>
            <pre className="px-2 py-2 rounded-md bg-muted border border-border text-[11px] text-foreground font-mono overflow-x-auto whitespace-pre">{curl}</pre>
            {!sig && (
              <p className="text-[10px] text-muted-foreground/70">Reveal your secret to fill in a runnable signature, or sign the request body yourself (HMAC-SHA256) into the <code>X-Signature</code> header.</p>
            )}
          </>
        )}
      </div>

      {/* Payload fields (accordion) */}
      <div className="space-y-1">
        <button
          type="button"
          onClick={() => setFieldsOpen((o) => !o)}
          aria-expanded={fieldsOpen}
          className="flex items-center gap-1 text-[10px] text-muted-foreground font-medium uppercase tracking-wider hover:text-foreground transition-colors"
        >
          <DisclosureCaret open={fieldsOpen} />
          Payload Fields
        </button>
        {fieldsOpen && (
          <p className="text-[11px] text-muted-foreground leading-relaxed">
            <code className="text-foreground">email</code> is required and identifies the contact.{' '}
            <code className="text-foreground">first_name</code>, <code className="text-foreground">last_name</code> and <code className="text-foreground">phone</code> map to contact fields. Any other keys are saved as custom fields, merged with what the contact already has.{' '}
            {/* company was listed here as a mapped field for years. It is read and passed
                to the workflow, but contacts hold a company RELATION and there is no
                text column for it, so it has never been stored. Saying so is cheaper
                than an admin concluding their data is being dropped at random. */}
            <code className="text-foreground">company</code> is accepted and visible to workflows, but is not saved on the contact.
          </p>
        )}
      </div>
    </div>

    {showRegenDialog && (
      <RegenerateConfirmDialog
        busy={regenerating}
        error={regenError}
        onConfirm={doRegenerate}
        onCancel={() => { if (!regenerating) setShowRegenDialog(false); }}
      />
    )}
    </>
  );
};
