import React, { useMemo, useCallback, useState, useEffect, useRef } from 'react';
import type { TriggerSpec } from '../types';
import { useBuilderStore } from '../store';
import { getWebhookToken, revealWebhookSecret, regenerateWebhookSecret } from '../api';
import type { WorkflowSchema, WebhookTokenInfo } from '../api';
import type { FiresOn } from '../useSchema';
import { StageDropdown } from './inputs';

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
  // deal + updated → deal_stage_changed (the only deal-update trigger the backend supports)
  if (object === 'deal' && firesOn === 'updated') {
    return { type: 'deal_stage_changed', params };
  }
  return { type: `${object}_${firesOn}`, params };
}

// --- Select styling ---
const selectClass =
  'bg-gray-800 border border-gray-700 rounded-lg px-3 py-1.5 text-sm text-white font-medium focus:border-indigo-500 focus:outline-none cursor-pointer appearance-none transition-colors hover:border-gray-600';

// ============================================================
// Main Component
// ============================================================

export const TriggerConfigPanel: React.FC = () => {
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

  // For webhook and deal_stage_changed, firesOn is not relevant
  const showFiresOn = object && object !== 'webhook' && !isDealStageChanged;

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
      <h3 className="text-lg font-semibold text-white">Source</h3>
      <p className="text-xs text-gray-400 -mt-2">Choose which object triggers this workflow.</p>

      {/* Empty state: no readable objects */}
      {hasNoObjects && (
        <div className="p-4 rounded-xl border border-dashed border-gray-700 bg-gray-800/20 text-center space-y-2">
          <p className="text-sm text-gray-400">No objects available</p>
          <p className="text-xs text-gray-500">You don't have read access to any objects. Contact your admin to get access.</p>
        </div>
      )}

      {/* Permission downgrade warning */}
      {objectOrphaned && (
        <div className="flex items-start gap-2 p-2.5 rounded-lg bg-amber-500/10 border border-amber-500/30">
          <span className="text-amber-400 text-sm mt-0.5">⚠</span>
          <div>
            <p className="text-xs text-amber-400 font-medium">Object no longer accessible</p>
            <p className="text-[11px] text-amber-400/70 mt-0.5">
              The object "{object}" is no longer in your readable objects. Select a different object or contact your admin.
            </p>
          </div>
        </div>
      )}

      <div className="p-3 rounded-xl border border-gray-700/50 bg-gray-800/30 space-y-3">
        {/* Object dropdown */}
        <div className="space-y-1.5">
          <label className="text-xs text-gray-500 font-medium uppercase tracking-wider">Object</label>
          <div className="relative">
            <select
              value={object || ''}
              onChange={(e) => handleObjectChange(e.target.value)}
              className={`${selectClass} w-full ${objectError || objectOrphaned ? '!border-red-500' : ''}`}
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
            <ChevronDown />
          </div>
          {objectError && (
            <p className="text-[11px] text-red-400 mt-0.5">⚠ {objectError}</p>
          )}
        </div>

        {/* Deal Stage Changed: From / To stage pickers */}
        {isDealStageChanged && (
          <div className="p-3 rounded-xl border border-gray-700/50 bg-gray-800/30 space-y-3 mt-3">
            <p className="text-xs text-gray-400 font-medium">Stage Transition Filter</p>

            {/* From Stage */}
            <div className="space-y-1.5">
              <label className="text-xs text-gray-500 font-medium uppercase tracking-wider">From Stage</label>
              <StageDropdown
                stages={stages}
                value={fromStage}
                onChange={handleFromStageChange}
                allowAny
                placeholder="Select from stage…"
              />
              <p className="text-[10px] text-gray-600">Only trigger when the deal moves <em>from</em> this stage. "Any" matches all.</p>
            </div>

            {/* To Stage */}
            <div className="space-y-1.5">
              <label className="text-xs text-gray-500 font-medium uppercase tracking-wider">To Stage</label>
              <StageDropdown
                stages={stages}
                value={toStage}
                onChange={handleToStageChange}
                placeholder="Select target stage…"
              />
              {toStageError && (
                <p className="text-[11px] text-red-400 mt-0.5">⚠ {toStageError}</p>
              )}
              <p className="text-[10px] text-gray-600">Required — the stage the deal must move <em>to</em> for this trigger to fire.</p>
            </div>
          </div>
        )}

        {/* Fires on selector */}
        {showFiresOn && (
          <div className="space-y-1.5">
            <label className="text-xs text-gray-500 font-medium uppercase tracking-wider">Fires on</label>
            <div className="flex gap-1">
              {FIRES_ON_OPTIONS.map((opt) => (
                <button
                  key={opt.value}
                  onClick={() => handleFiresOnChange(opt.value)}
                  className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-all duration-150 ${
                    firesOn === opt.value
                      ? 'bg-indigo-500 text-white shadow-md shadow-indigo-500/25'
                      : 'bg-gray-800 text-gray-400 hover:text-white hover:bg-gray-700'
                  }`}
                >
                  {opt.label}
                </button>
              ))}
            </div>
            {firesOnError && (
              <p className="text-[11px] text-red-400 mt-0.5">⚠ {firesOnError}</p>
            )}
          </div>
        )}
      </div>

      {/* Webhook setup instructions (P17) — only for the inbound webhook trigger */}
      {object === 'webhook' && <WebhookSetup />}

      {/* Preview sentence */}
      {object && (
        <div className="px-3 py-2 rounded-lg bg-indigo-500/5 border border-indigo-500/10">
          <p className="text-xs text-indigo-300/70">
            <span className="text-indigo-400 font-medium">Preview: </span>
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
    className="shrink-0 px-2 py-1 rounded-md bg-gray-700 text-[11px] text-gray-300 hover:text-white hover:bg-gray-600 transition-colors"
  >
    {active ? '✓ Copied' : 'Copy'}
  </button>
);

// Rotating caret for the collapsible sub-sections (curl / payload fields).
const DisclosureCaret: React.FC<{ open: boolean }> = ({ open }) => (
  <svg
    className={`w-3 h-3 shrink-0 transition-transform ${open ? 'rotate-90' : ''}`}
    fill="none"
    viewBox="0 0 24 24"
    stroke="currentColor"
    strokeWidth={2}
    aria-hidden="true"
  >
    <path strokeLinecap="round" strokeLinejoin="round" d="M9 5l7 7-7 7" />
  </svg>
);

// Modal confirm for the destructive secret rotation. Module-level (not redefined
// per render). Mirrors the app's dialog convention (see RunNowModal).
const RegenerateConfirmDialog: React.FC<{
  busy: boolean;
  error: string | null;
  onConfirm: () => void;
  onCancel: () => void;
}> = ({ busy, error, onConfirm, onCancel }) => (
  <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
    <div
      className="absolute inset-0 bg-black/50 backdrop-blur-sm"
      onClick={busy ? undefined : onCancel}
      aria-hidden="true"
    />
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Regenerate signing secret"
      className="relative bg-gray-900 border border-gray-700 rounded-2xl shadow-2xl w-full max-w-md overflow-hidden animate-in zoom-in-95 duration-200"
    >
      <div className="p-5 space-y-3">
        <h4 className="text-sm font-semibold text-white">Regenerate signing secret?</h4>
        <p className="text-[12px] text-gray-400 leading-relaxed">
          Existing webhook integrations will break until updated with the new secret. Confirm?
        </p>
        {error && <p role="alert" className="text-[12px] text-red-400">⚠ {error}</p>}
        <div className="flex justify-end gap-2 pt-1">
          <button
            type="button"
            onClick={onCancel}
            disabled={busy}
            className="px-3 py-1.5 rounded-lg bg-gray-700 text-xs text-gray-200 hover:bg-gray-600 transition-colors disabled:opacity-60"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={onConfirm}
            disabled={busy}
            className="px-3 py-1.5 rounded-lg bg-red-600 text-xs text-white font-medium hover:bg-red-500 transition-colors disabled:opacity-60"
          >
            {busy ? 'Rotating…' : 'Regenerate secret'}
          </button>
        </div>
      </div>
    </div>
  </div>
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
      <div className="p-3 rounded-xl border border-gray-700/50 bg-gray-800/30">
        <p className="text-xs text-gray-500">Loading webhook setup…</p>
      </div>
    );
  }

  if (error || !info) {
    return (
      <div className="p-3 rounded-xl border border-amber-500/30 bg-amber-500/10">
        <p className="text-xs text-amber-400 font-medium">Couldn't load webhook setup</p>
        <p className="text-[11px] text-amber-400/70 mt-0.5">
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
    <div className="p-3 rounded-xl border border-gray-700/50 bg-gray-800/30 space-y-3">
      <div className="flex items-center gap-2">
        <span className="text-sm">🔗</span>
        <p className="text-xs text-gray-300 font-medium">Webhook Setup</p>
      </div>
      <p className="text-[11px] text-gray-500 -mt-1">
        POST to this URL to trigger the workflow. Sign the request body with your secret using HMAC-SHA256 and send it as the <code className="text-gray-400">X-Signature</code> header.
      </p>

      {/* Endpoint URL */}
      <div className="space-y-1">
        <label className="text-[10px] text-gray-500 font-medium uppercase tracking-wider">Endpoint URL</label>
        <div className="flex items-center gap-2">
          <code className="flex-1 min-w-0 truncate px-2 py-1.5 rounded-md bg-gray-900 border border-gray-700 text-[11px] text-indigo-300 font-mono" title={info.url}>{info.url}</code>
          <CopyButton active={copied === 'url'} onClick={() => copy('url', info.url)} label="Copy webhook URL" />
        </div>
        <p className="text-[10px] text-gray-600">This URL is permanent — it stays the same when you rotate the secret.</p>
      </div>

      {/* Signing secret: masked by default, reveal (auto-hide 30s) / copy / rotate */}
      <div className="space-y-1">
        <label className="text-[10px] text-gray-500 font-medium uppercase tracking-wider">Signing Secret</label>
        <div className="flex items-center gap-2">
          <code className="flex-1 min-w-0 truncate px-2 py-1.5 rounded-md bg-gray-900 border border-gray-700 text-[11px] text-amber-300/90 font-mono">
            {revealed && fullSecret ? fullSecret : info.secret_masked}
          </code>
          <button
            type="button"
            onClick={revealed ? hideSecret : onReveal}
            disabled={secretBusy}
            className="shrink-0 px-2 py-1 rounded-md bg-gray-700 text-[11px] text-gray-300 hover:text-white hover:bg-gray-600 transition-colors disabled:opacity-60"
          >
            {revealed ? 'Hide' : 'Reveal'}
          </button>
          <CopyButton active={copied === 'secret'} onClick={onCopySecret} label="Copy signing secret" />
          <button
            type="button"
            onClick={() => { setRegenError(null); setShowRegenDialog(true); }}
            className="shrink-0 px-2 py-1 rounded-md bg-gray-700 text-[11px] text-gray-300 hover:text-white hover:bg-gray-600 transition-colors"
          >
            ↻ Regenerate
          </button>
        </div>
        {revealed && (
          <p className="text-[10px] text-gray-600">Visible for 30 seconds — copy it now if you need it.</p>
        )}
        {revealError && (
          <p className="text-[10px] text-red-400">⚠ {revealError}</p>
        )}
      </div>

      {/* curl example (collapsible) — pre-filled with URL, sample body, signature */}
      <div className="space-y-1">
        <div className="flex items-center justify-between">
          <button
            type="button"
            onClick={() => setCurlOpen((o) => !o)}
            aria-expanded={curlOpen}
            className="flex items-center gap-1 text-[10px] text-gray-500 font-medium uppercase tracking-wider hover:text-gray-300 transition-colors"
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
            <pre className="px-2 py-2 rounded-md bg-gray-900 border border-gray-700 text-[11px] text-gray-300 font-mono overflow-x-auto whitespace-pre">{curl}</pre>
            {!sig && (
              <p className="text-[10px] text-gray-600">Reveal your secret to fill in a runnable signature, or sign the request body yourself (HMAC-SHA256) into the <code>X-Signature</code> header.</p>
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
          className="flex items-center gap-1 text-[10px] text-gray-500 font-medium uppercase tracking-wider hover:text-gray-300 transition-colors"
        >
          <DisclosureCaret open={fieldsOpen} />
          Payload Fields
        </button>
        {fieldsOpen && (
          <p className="text-[11px] text-gray-500 leading-relaxed">
            <code className="text-gray-300">email</code> is required and identifies the contact.{' '}
            <code className="text-gray-300">first_name</code>, <code className="text-gray-300">last_name</code>, <code className="text-gray-300">phone</code>, <code className="text-gray-300">company</code> map to contact fields. Any other keys are saved as custom fields.
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

// Chevron icon for select dropdowns
const ChevronDown: React.FC = () => (
  <svg
    className="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-gray-500"
    fill="none"
    viewBox="0 0 24 24"
    stroke="currentColor"
    strokeWidth={2}
  >
    <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
  </svg>
);
