import { useState } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { Building2, AlertTriangle, LogOut, Trash2, ShieldCheck } from 'lucide-react';
import type { WorkspaceDetail } from '../../lib/api';
import { Button, Input, Label, Select, SpinnerBlock } from '@/components/ui';
import {
  useWorkspace,
  useUpdateWorkspace,
  useLeaveWorkspace,
  useDeleteWorkspace,
} from '../../features/settings/queries';
import { useAuth } from '../../lib/auth';
import { useConfirm } from '../../components/common/ConfirmDialog';
import { currencyOptions, localeOptions } from '../../lib/intlOptions';

const CURRENCIES = currencyOptions();
const LOCALES = localeOptions();

// The editable half of the workspace, split out from the server payload so the
// form can be compared against (and re-seeded from) the last server snapshot.
interface WorkspaceForm {
  name: string;
  currency: string;
  locale: string;
  timezone: string;
  require2FA: boolean;
}

const toForm = (w: WorkspaceDetail): WorkspaceForm => ({
  name: w.name,
  currency: w.currency,
  locale: w.locale,
  timezone: w.timezone,
  require2FA: !!w.require_two_factor,
});

const sameForm = (a: WorkspaceForm, b: WorkspaceForm) =>
  a.name === b.name && a.currency === b.currency && a.locale === b.locale &&
  a.timezone === b.timezone && a.require2FA === b.require2FA;

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
      className={`relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-40 ${on ? 'bg-primary' : 'bg-muted'}`}
    >
      <span className={`inline-block h-4 w-4 transform rounded-full bg-background shadow transition-transform ${on ? 'translate-x-4' : 'translate-x-0.5'}`} />
    </button>
  );
}

// WorkspaceGeneralSection (U4): the org.settings-gated Workspace General page —
// rename the workspace, set its defaults (currency/locale/timezone), and the
// danger zone (leave / delete). Leaving and deleting both re-establish auth
// afterwards (the active workspace changes) instead of leaving a stale session.
//
// The workspace is server state read through react-query (U7.3). The form is
// re-seeded whenever the server copy changes AND the admin hasn't started editing
// — so another admin's rename lands on this screen instead of being silently
// overwritten by a stale form, while in-progress typing is never clobbered.
export default function WorkspaceGeneralSection() {
  const navigate = useNavigate();
  const { refreshAuth } = useAuth();
  const { confirm, dialog } = useConfirm();

  const [error, setError] = useState('');
  const [saveMsg, setSaveMsg] = useState('');
  // Type-to-confirm text for the destructive delete.
  const [deleteConfirmText, setDeleteConfirmText] = useState('');
  const [showDelete, setShowDelete] = useState(false);

  const { data: ws, isLoading, error: loadError } = useWorkspace();
  const saveMut = useUpdateWorkspace();
  const leaveMut = useLeaveWorkspace();
  const deleteMut = useDeleteWorkspace();
  const saving = saveMut.isPending;
  const busy = leaveMut.isPending || deleteMut.isPending;

  // `draft` holds ONLY the fields this admin has actually touched; the rest render
  // straight off the server copy. That's what makes the screen safe to re-read: a
  // concurrent change to an untouched field shows up immediately, while in-progress
  // typing is never clobbered by a background refetch. (An overlay also needs no
  // seeding effect — there's no local mirror of the server to keep in sync.)
  const [draft, setDraft] = useState<Partial<WorkspaceForm>>({});
  const serverForm = ws ? toForm(ws) : null;
  const form = serverForm ? { ...serverForm, ...draft } : null;
  const dirty = !!serverForm && !!form && !sameForm(form, serverForm);
  const patch = <K extends keyof WorkspaceForm>(key: K, value: WorkspaceForm[K]) =>
    setDraft((d) => ({ ...d, [key]: value }));

  const save = async () => {
    if (!ws || !form || !form.name.trim()) return;
    // Turning the 2FA policy ON is a change that hits every OTHER member, not just
    // this page — they're locked out of the app until they enrol. Say so first.
    if (form.require2FA && !ws.require_two_factor) {
      if (!(await confirm({
        title: 'Require two-factor authentication?',
        body: 'Every member who has not set up two-factor authentication will be locked out of the workspace on their next request until they enrol. Check the Members list to see who has it on. Existing sessions are not exempt.',
        confirmLabel: 'Require it',
        tone: 'danger',
      }))) return;
    }
    setError('');
    setSaveMsg('');
    const name = form.name.trim();
    const timezone = form.timezone.trim();
    const nameChanged = name !== ws.name;
    saveMut.mutate(
      { name, currency: form.currency, locale: form.locale, timezone, require_two_factor: form.require2FA },
      {
        onSuccess: async () => {
          // The mutation primes the workspace cache with what was saved, so dropping
          // the overlay here shows the saved values (not a flash of the old ones) and
          // leaves the next re-read free to surface someone else's change.
          setDraft({});
          setSaveMsg('Saved.');
          // Propagate a rename to the sidebar/switcher (which read the name from the
          // auth session) — only re-establish auth when the name actually changed.
          if (nameChanged) await refreshAuth();
        },
        onError: (e) => setError(e instanceof Error ? e.message : 'Failed to save'),
      },
    );
  };

  const handleLeave = async () => {
    if (!(await confirm({
      title: 'Leave this workspace?',
      body: `You'll lose access to ${ws?.name ?? 'this workspace'} and need a new invitation to return. Records you own stay in the workspace.`,
      confirmLabel: 'Leave workspace',
      tone: 'danger',
    }))) return;
    setError('');
    leaveMut.mutate(undefined, {
      onSuccess: async () => {
        await refreshAuth(); // switches into another workspace, or lands on /no-workspace
        navigate('/');
      },
      onError: (e) => setError(e instanceof Error ? e.message : 'Failed to leave workspace'),
    });
  };

  const handleDelete = () => {
    if (!ws || deleteConfirmText !== ws.name) return;
    setError('');
    deleteMut.mutate(undefined, {
      onSuccess: async () => {
        await refreshAuth();
        navigate('/');
      },
      onError: (e) => setError(e instanceof Error ? e.message : 'Failed to delete workspace'),
    });
  };

  if (isLoading) return <SpinnerBlock />;

  const banner = error || (loadError instanceof Error ? loadError.message : '');
  if (banner && !ws) return <div className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">{banner}</div>;
  if (!ws || !form) return null;

  return (
    <div className="space-y-8 max-w-2xl">
      <div>
        <h2 className="text-lg font-semibold flex items-center gap-2"><Building2 className="w-5 h-5" /> Workspace</h2>
        <p className="text-sm text-muted-foreground mt-1">
          {ws.member_count} member{ws.member_count === 1 ? '' : 's'} · created {new Date(ws.created_at).toLocaleDateString()}
        </p>
      </div>

      {banner && <div className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">{banner}</div>}

      {/* General settings */}
      <div className="space-y-4">
        <div>
          <Label htmlFor="ws-name" className="mb-1.5 block text-sm">Workspace name</Label>
          <Input id="ws-name" value={form.name} onChange={(e) => patch('name', e.target.value)} className="max-w-md" />
        </div>
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 max-w-md">
          <div>
            <Label htmlFor="ws-currency" className="mb-1.5 block text-sm">Currency</Label>
            <Select id="ws-currency" value={form.currency} onChange={(e) => patch('currency', e.target.value)}>
              <option value="">— No default —</option>
              {CURRENCIES.map((c) => <option key={c.value} value={c.value}>{c.label}</option>)}
            </Select>
          </div>
          <div>
            <Label htmlFor="ws-locale" className="mb-1.5 block text-sm">Locale</Label>
            <Select id="ws-locale" value={form.locale} onChange={(e) => patch('locale', e.target.value)}>
              <option value="">— No default —</option>
              {LOCALES.map((l) => <option key={l.value} value={l.value}>{l.label}</option>)}
            </Select>
          </div>
          <div>
            <Label htmlFor="ws-timezone" className="mb-1.5 block text-sm">Timezone</Label>
            <Input id="ws-timezone" value={form.timezone} onChange={(e) => patch('timezone', e.target.value)} placeholder="e.g. America/New_York" />
          </div>
        </div>
        <div className="flex items-center gap-3">
          <Button onClick={save} disabled={!dirty || saving || !form.name.trim()}>
            {saving ? 'Saving…' : 'Save changes'}
          </Button>
          {saveMsg && <span className="text-sm text-emerald-600 dark:text-emerald-400">{saveMsg}</span>}
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
              <Link to="/settings/members" className="text-primary hover:underline">See who has it on</Link>.
            </p>
          </div>
          <Toggle
            on={form.require2FA}
            onChange={(v) => patch('require2FA', v)}
            disabled={saving || busy}
            label="Require two-factor authentication"
          />
        </div>
        {form.require2FA && !ws.require_two_factor && (
          <p className="text-xs text-amber-600 dark:text-amber-400">Not applied yet — hit “Save changes” above to turn the policy on.</p>
        )}
      </div>

      {/* Danger zone */}
      <div className="border border-destructive/30 rounded-xl p-5 space-y-4">
        <h3 className="text-sm font-semibold text-destructive flex items-center gap-1.5"><AlertTriangle className="w-4 h-4" /> Danger zone</h3>

        <div className="flex items-center justify-between gap-4 flex-wrap">
          <div>
            <p className="text-sm font-medium">Leave workspace</p>
            <p className="text-xs text-muted-foreground">Remove yourself from this workspace. The sole owner must transfer ownership first.</p>
          </div>
          <Button variant="outline" onClick={handleLeave} disabled={busy} className="whitespace-nowrap">
            <LogOut aria-hidden /> Leave
          </Button>
        </div>

        {ws.is_owner && (
          <div className="pt-4 border-t border-destructive/20">
            {!showDelete ? (
              <div className="flex items-center justify-between gap-4 flex-wrap">
                <div>
                  <p className="text-sm font-medium">Delete workspace</p>
                  <p className="text-xs text-muted-foreground">Permanently remove this workspace and revoke everyone's access. This can't be undone.</p>
                </div>
                <Button
                  onClick={() => setShowDelete(true)}
                  className="whitespace-nowrap bg-destructive/10 text-destructive shadow-none hover:bg-destructive/20"
                >
                  <Trash2 aria-hidden /> Delete
                </Button>
              </div>
            ) : (
              <div className="space-y-2">
                <p className="text-sm">Type <strong className="font-semibold">{ws.name}</strong> to confirm deletion:</p>
                <Input
                  value={deleteConfirmText}
                  onChange={(e) => setDeleteConfirmText(e.target.value)}
                  aria-label="Type the workspace name to confirm"
                  className="max-w-md"
                  autoFocus
                />
                <div className="flex gap-2">
                  <Button variant="destructive" onClick={handleDelete} disabled={busy || deleteConfirmText !== ws.name}>
                    {deleteMut.isPending ? 'Deleting…' : 'Delete this workspace'}
                  </Button>
                  <Button variant="outline" onClick={() => { setShowDelete(false); setDeleteConfirmText(''); }} disabled={busy}>
                    Cancel
                  </Button>
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
