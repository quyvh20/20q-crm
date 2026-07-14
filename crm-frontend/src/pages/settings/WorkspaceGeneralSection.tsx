import { useState, useEffect, useCallback } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { Loader2, Building2, AlertTriangle, LogOut, Trash2, ShieldCheck } from 'lucide-react';
import {
  getCurrentWorkspace, updateWorkspace, leaveWorkspace, deleteWorkspace,
  type WorkspaceDetail,
} from '../../lib/api';
import { useAuth } from '../../lib/auth';
import { useConfirm } from '../../components/common/ConfirmDialog';

const CURRENCIES = ['', 'USD', 'EUR', 'GBP', 'CAD', 'AUD', 'JPY', 'INR', 'BRL'];
const LOCALES = ['', 'en-US', 'en-GB', 'fr-FR', 'de-DE', 'es-ES', 'pt-BR', 'ja-JP'];

// Same accessible switch as the notification preference center — a real
// role="switch" button, not a styled checkbox.
function Toggle({ on, onChange, disabled, label }: { on: boolean; onChange: (v: boolean) => void; disabled?: boolean; label: string }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!on)}
      className={`relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors disabled:opacity-40 ${on ? 'bg-primary' : 'bg-muted'}`}
    >
      <span className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${on ? 'translate-x-4' : 'translate-x-0.5'}`} />
    </button>
  );
}

// WorkspaceGeneralSection (U4): the org.settings-gated Workspace General page —
// rename the workspace, set its defaults (currency/locale/timezone), and the
// danger zone (leave / delete). Leaving and deleting both re-establish auth
// afterwards (the active workspace changes) instead of leaving a stale session.
export default function WorkspaceGeneralSection() {
  const navigate = useNavigate();
  const { refreshAuth } = useAuth();
  const { confirm, dialog } = useConfirm();

  const [ws, setWs] = useState<WorkspaceDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [saveMsg, setSaveMsg] = useState('');
  const [saving, setSaving] = useState(false);
  const [busy, setBusy] = useState(false);

  // Editable form fields.
  const [name, setName] = useState('');
  const [currency, setCurrency] = useState('');
  const [locale, setLocale] = useState('');
  const [timezone, setTimezone] = useState('');
  // The workspace 2FA policy (U6.4).
  const [require2FA, setRequire2FA] = useState(false);
  // Type-to-confirm text for the destructive delete.
  const [deleteConfirmText, setDeleteConfirmText] = useState('');
  const [showDelete, setShowDelete] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const d = await getCurrentWorkspace();
      setWs(d);
      setName(d.name);
      setCurrency(d.currency);
      setLocale(d.locale);
      setTimezone(d.timezone);
      setRequire2FA(!!d.require_two_factor);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load workspace');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const dirty = !!ws && (
    name !== ws.name || currency !== ws.currency || locale !== ws.locale ||
    timezone !== ws.timezone || require2FA !== !!ws.require_two_factor
  );

  const save = async () => {
    if (!ws || !name.trim()) return;
    // Turning the 2FA policy ON is a change that hits every OTHER member, not just
    // this page — they're locked out of the app until they enrol. Say so first.
    if (require2FA && !ws.require_two_factor) {
      if (!(await confirm({
        title: 'Require two-factor authentication?',
        body: 'Every member who has not set up two-factor authentication will be locked out of the workspace on their next request until they enrol. Check the Members list to see who has it on. Existing sessions are not exempt.',
        confirmLabel: 'Require it',
        tone: 'danger',
      }))) return;
    }
    setSaving(true);
    setError('');
    setSaveMsg('');
    const nameChanged = name.trim() !== ws.name;
    try {
      await updateWorkspace({ name: name.trim(), currency, locale, timezone: timezone.trim(), require_two_factor: require2FA });
      setWs({ ...ws, name: name.trim(), currency, locale, timezone: timezone.trim(), require_two_factor: require2FA });
      setSaveMsg('Saved.');
      // Propagate a rename to the sidebar/switcher (which read the name from the
      // auth session) — only re-establish auth when the name actually changed.
      if (nameChanged) await refreshAuth();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to save');
    } finally {
      setSaving(false);
    }
  };

  const handleLeave = async () => {
    if (!(await confirm({
      title: 'Leave this workspace?',
      body: `You'll lose access to ${ws?.name ?? 'this workspace'} and need a new invitation to return. Records you own stay in the workspace.`,
      confirmLabel: 'Leave workspace',
      tone: 'danger',
    }))) return;
    setBusy(true);
    setError('');
    try {
      await leaveWorkspace();
      await refreshAuth(); // switches into another workspace, or lands on /no-workspace
      navigate('/');
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to leave workspace');
      setBusy(false);
    }
  };

  const handleDelete = async () => {
    if (!ws || deleteConfirmText !== ws.name) return;
    setBusy(true);
    setError('');
    try {
      await deleteWorkspace();
      await refreshAuth();
      navigate('/');
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to delete workspace');
      setBusy(false);
    }
  };

  if (loading) return <div className="flex justify-center py-16"><Loader2 className="w-7 h-7 animate-spin text-muted-foreground" /></div>;
  if (error && !ws) return <div className="bg-red-500/10 text-red-500 text-sm rounded-lg px-3 py-2">{error}</div>;
  if (!ws) return null;

  const inputCls = 'w-full max-w-md px-3 py-2 text-sm bg-background border border-border rounded-lg text-foreground focus:outline-none focus:ring-1 focus:ring-primary';

  return (
    <div className="space-y-8 max-w-2xl">
      <div>
        <h2 className="text-lg font-semibold flex items-center gap-2"><Building2 className="w-5 h-5" /> Workspace</h2>
        <p className="text-sm text-muted-foreground mt-1">
          {ws.member_count} member{ws.member_count === 1 ? '' : 's'} · created {new Date(ws.created_at).toLocaleDateString()}
        </p>
      </div>

      {error && <div className="bg-red-500/10 text-red-500 text-sm rounded-lg px-3 py-2">{error}</div>}

      {/* General settings */}
      <div className="space-y-4">
        <div>
          <label className="block text-sm font-medium mb-1.5">Workspace name</label>
          <input value={name} onChange={(e) => setName(e.target.value)} className={inputCls} />
        </div>
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 max-w-md">
          <div>
            <label className="block text-sm font-medium mb-1.5">Currency</label>
            <select value={currency} onChange={(e) => setCurrency(e.target.value)} className="w-full px-3 py-2 text-sm bg-background border border-border rounded-lg text-foreground focus:outline-none focus:ring-1 focus:ring-primary">
              {CURRENCIES.map((c) => <option key={c} value={c}>{c || '—'}</option>)}
            </select>
          </div>
          <div>
            <label className="block text-sm font-medium mb-1.5">Locale</label>
            <select value={locale} onChange={(e) => setLocale(e.target.value)} className="w-full px-3 py-2 text-sm bg-background border border-border rounded-lg text-foreground focus:outline-none focus:ring-1 focus:ring-primary">
              {LOCALES.map((l) => <option key={l} value={l}>{l || '—'}</option>)}
            </select>
          </div>
          <div>
            <label className="block text-sm font-medium mb-1.5">Timezone</label>
            <input value={timezone} onChange={(e) => setTimezone(e.target.value)} placeholder="e.g. America/New_York" className="w-full px-3 py-2 text-sm bg-background border border-border rounded-lg text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-primary" />
          </div>
        </div>
        <div className="flex items-center gap-3">
          <button onClick={save} disabled={!dirty || saving || !name.trim()} className="px-4 py-2 text-sm rounded-lg bg-blue-500 text-white hover:bg-blue-600 disabled:opacity-50">
            {saving ? 'Saving…' : 'Save changes'}
          </button>
          {saveMsg && <span className="text-sm text-green-500">{saveMsg}</span>}
        </div>
      </div>

      {/* Security policy (U6.4) — org-wide 2FA. Saved with the form above, but
          gated by a confirm because turning it ON locks out every member who
          hasn't enrolled. */}
      <div className="border border-border rounded-xl p-5 space-y-3">
        <h3 className="text-sm font-semibold flex items-center gap-1.5"><ShieldCheck className="w-4 h-4" /> Security</h3>
        <div className="flex items-start justify-between gap-4">
          <div>
            <p className="text-sm font-medium">Require two-factor authentication</p>
            <p className="text-xs text-muted-foreground mt-0.5">
              Every member must sign in with a code from their authenticator app. Members who haven't set it up are
              locked out of the workspace until they enrol — including anyone already signed in.{' '}
              <Link to="/settings/members" className="text-blue-500 hover:underline">See who has it on</Link>.
            </p>
          </div>
          <Toggle
            on={require2FA}
            onChange={setRequire2FA}
            disabled={saving || busy}
            label="Require two-factor authentication"
          />
        </div>
        {require2FA && !ws.require_two_factor && (
          <p className="text-xs text-amber-500">Not applied yet — hit “Save changes” above to turn the policy on.</p>
        )}
      </div>

      {/* Danger zone */}
      <div className="border border-red-500/30 rounded-xl p-5 space-y-4">
        <h3 className="text-sm font-semibold text-red-500 flex items-center gap-1.5"><AlertTriangle className="w-4 h-4" /> Danger zone</h3>

        <div className="flex items-center justify-between gap-4 flex-wrap">
          <div>
            <p className="text-sm font-medium">Leave workspace</p>
            <p className="text-xs text-muted-foreground">Remove yourself from this workspace. The sole owner must transfer ownership first.</p>
          </div>
          <button onClick={handleLeave} disabled={busy} className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm rounded-lg border border-border hover:bg-accent disabled:opacity-50 whitespace-nowrap">
            <LogOut className="w-4 h-4" /> Leave
          </button>
        </div>

        {ws.is_owner && (
          <div className="pt-4 border-t border-red-500/20">
            {!showDelete ? (
              <div className="flex items-center justify-between gap-4 flex-wrap">
                <div>
                  <p className="text-sm font-medium">Delete workspace</p>
                  <p className="text-xs text-muted-foreground">Permanently remove this workspace and revoke everyone's access. This can't be undone.</p>
                </div>
                <button onClick={() => setShowDelete(true)} className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm rounded-lg bg-red-500/10 text-red-500 hover:bg-red-500/20 whitespace-nowrap">
                  <Trash2 className="w-4 h-4" /> Delete
                </button>
              </div>
            ) : (
              <div className="space-y-2">
                <p className="text-sm">Type <strong className="font-semibold">{ws.name}</strong> to confirm deletion:</p>
                <input
                  value={deleteConfirmText}
                  onChange={(e) => setDeleteConfirmText(e.target.value)}
                  aria-label="Type the workspace name to confirm"
                  className={inputCls}
                  autoFocus
                />
                <div className="flex gap-2">
                  <button onClick={handleDelete} disabled={busy || deleteConfirmText !== ws.name} className="px-3 py-1.5 text-sm rounded-lg bg-red-500 text-white hover:bg-red-600 disabled:opacity-50">
                    {busy ? 'Deleting…' : 'Delete this workspace'}
                  </button>
                  <button onClick={() => { setShowDelete(false); setDeleteConfirmText(''); }} disabled={busy} className="px-3 py-1.5 text-sm rounded-lg border border-border hover:bg-accent disabled:opacity-50">
                    Cancel
                  </button>
                </div>
              </div>
            )}
          </div>
        )}
      </div>
      {dialog}
    </div>
  );
}
