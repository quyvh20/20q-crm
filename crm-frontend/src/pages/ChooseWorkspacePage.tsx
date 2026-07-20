import { useEffect, useState } from 'react';
import { useAuth } from '../lib/auth';
import { getWorkspaces, type Workspace } from '../lib/api';
import { useDocumentTitle } from '../lib/useDocumentTitle';
import { Building2, Users, Check, Star, Loader2, LogOut, Ban, AlertTriangle, Plus, ArrowRight } from 'lucide-react';
import { Button, Input } from '@/components/ui';
import { markTemplatePickerPending } from '../features/onboarding/templatePickerHandoff';

/**
 * The R2 workspace chooser (P4). Shown when a user belongs to multiple active
 * workspaces and no valid default has resolved one. Picking a card calls
 * switch-workspace (optionally persisting it as the default), after which the
 * user is never asked again.
 *
 * P4 niceties: it also fetches the full membership list so SUSPENDED memberships
 * render as disabled cards (a user understands why an org they remember is gone),
 * and it surfaces a "you no longer have access to X" banner when a refresh 409
 * bounced them here off an org they just lost.
 */
export default function ChooseWorkspacePage() {
  useDocumentTitle('Choose Workspace');
  const { workspaces, switchWorkspace, defaultOrgId, logout, createWorkspace } = useAuth();
  const [makeDefault, setMakeDefault] = useState(true);
  const [busyOrg, setBusyOrg] = useState<string | null>(null);
  const [error, setError] = useState('');
  // Inline "create a new workspace" (U4).
  const [creatingOpen, setCreatingOpen] = useState(false);
  const [newName, setNewName] = useState('');
  const [creating, setCreating] = useState(false);

  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newName.trim()) return;
    setCreating(true);
    setError('');
    try {
      await createWorkspace({ name: newName.trim() });
      // Arm the starter-template picker for the load we are about to trigger —
      // in-memory state does not survive a hard navigation.
      markTemplatePickerPending();
      window.location.assign('/');
    } catch (err: any) {
      setError(err?.message || 'Could not create the workspace.');
      setCreating(false);
    }
  };
  // Seed from auth state (active-only); enrich with the full list (incl. suspended)
  // once the fetch resolves so suspended cards can render.
  const [allWorkspaces, setAllWorkspaces] = useState<Workspace[]>(workspaces);
  // The workspace a 409 just bounced us off (read once, then cleared).
  const [lostWorkspace] = useState<string | null>(() => {
    const name = sessionStorage.getItem('lost_workspace_name');
    if (name) sessionStorage.removeItem('lost_workspace_name');
    return name;
  });

  useEffect(() => {
    getWorkspaces()
      .then((ws) => {
        if (ws.length > 0) setAllWorkspaces(ws);
      })
      .catch(() => {
        /* fall back to the active-only auth-state list */
      });
  }, []);

  const active = allWorkspaces.filter((w) => w.status === 'active');
  const suspended = allWorkspaces.filter((w) => w.status !== 'active');

  const choose = async (orgId: string) => {
    setError('');
    setBusyOrg(orgId);
    try {
      await switchWorkspace(orgId, makeDefault);
      // switchWorkspace hard-reloads on success; nothing more to do here.
    } catch (err: any) {
      setError(err?.message || 'Could not open that workspace.');
      setBusyOrg(null);
    }
  };

  return (
    <div className="min-h-screen bg-muted/30 flex flex-col items-center justify-center px-4 py-10">
      <div className="w-full max-w-lg">
        <div className="text-center mb-8">
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">Choose a workspace</h1>
          <p className="text-sm text-muted-foreground mt-2">You're a member of several workspaces. Pick one to continue.</p>
        </div>

        {lostWorkspace && (
          <div className="mb-4 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm text-amber-600 dark:text-amber-400 flex items-start gap-2">
            <AlertTriangle className="w-4 h-4 mt-0.5 shrink-0" />
            <span>
              You no longer have access to <span className="font-semibold">{lostWorkspace}</span>. Choose another workspace to continue.
            </span>
          </div>
        )}

        {error && (
          <div className="mb-4 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
            {error}
          </div>
        )}

        <div className="space-y-3">
          {active.map(ws => {
            const isDefault = ws.org_id === defaultOrgId;
            const busy = busyOrg === ws.org_id;
            return (
              <button
                key={ws.org_id}
                onClick={() => choose(ws.org_id)}
                disabled={!!busyOrg}
                className="w-full text-left group flex items-center gap-4 rounded-lg border border-border bg-card p-4 hover:bg-accent transition-colors disabled:opacity-60"
              >
                <div className="w-11 h-11 rounded-lg bg-primary/10 flex items-center justify-center shrink-0">
                  <Building2 className="w-5 h-5 text-primary" />
                </div>
                <div className="flex-1 min-w-0">
                  <p className="text-foreground font-semibold truncate flex items-center gap-2">
                    {ws.org_name}
                    {isDefault && <Star className="w-3.5 h-3.5 text-amber-500 fill-amber-500" />}
                  </p>
                  <p className="text-xs text-muted-foreground capitalize flex items-center gap-2 mt-0.5">
                    <span>{ws.role?.replace('_', ' ')}</span>
                    {typeof ws.member_count === 'number' && (
                      <span className="flex items-center gap-1">
                        <Users className="w-3 h-3" />
                        {ws.member_count} member{ws.member_count === 1 ? '' : 's'}
                      </span>
                    )}
                  </p>
                </div>
                {busy ? (
                  <Loader2 className="w-5 h-5 text-primary animate-spin shrink-0" />
                ) : (
                  <ArrowRight aria-hidden="true" className="w-4 h-4 text-muted-foreground group-hover:text-primary transition-colors shrink-0" />
                )}
              </button>
            );
          })}
        </div>

        {/* Create a new workspace (U4) — inline, so an existing user isn't sent to
            /register (which would bounce them). */}
        <div className="mt-3">
          {creatingOpen ? (
            <form onSubmit={create} className="flex gap-2">
              <Input
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                placeholder="New workspace name"
                aria-label="New workspace name"
                autoFocus
                className="flex-1"
              />
              <Button type="submit" disabled={creating || !newName.trim()}>
                {creating ? <Loader2 className="animate-spin" /> : 'Create'}
              </Button>
            </form>
          ) : (
            <button
              onClick={() => setCreatingOpen(true)}
              disabled={!!busyOrg}
              className="w-full flex items-center gap-4 rounded-lg border border-dashed border-border p-4 text-muted-foreground hover:border-primary/50 hover:text-foreground transition-colors disabled:opacity-60"
            >
              <div className="w-11 h-11 rounded-lg bg-muted flex items-center justify-center shrink-0">
                <Plus className="w-5 h-5" />
              </div>
              <span className="font-medium">Create a new workspace</span>
            </button>
          )}
        </div>

        {/* Suspended memberships — shown disabled so the user knows the org exists
            but they can't enter it (contact an admin to be reinstated). */}
        {suspended.length > 0 && (
          <div className="mt-4 space-y-3">
            <p className="text-xs uppercase tracking-wider text-muted-foreground font-semibold px-1">No longer active</p>
            {suspended.map(ws => (
              <div
                key={ws.org_id}
                aria-disabled="true"
                title="Your membership here is suspended — ask a workspace admin to reinstate you."
                className="w-full text-left flex items-center gap-4 rounded-lg border border-border bg-card p-4 opacity-60 cursor-not-allowed"
              >
                <div className="w-11 h-11 rounded-lg bg-muted flex items-center justify-center shrink-0">
                  <Ban className="w-5 h-5 text-muted-foreground" />
                </div>
                <div className="flex-1 min-w-0">
                  <p className="text-foreground font-semibold truncate">{ws.org_name}</p>
                  <p className="text-xs text-muted-foreground capitalize mt-0.5">
                    {ws.role?.replace('_', ' ')} · Suspended
                  </p>
                </div>
                <span className="text-[11px] text-muted-foreground shrink-0">Suspended</span>
              </div>
            ))}
          </div>
        )}

        <label className="flex items-center gap-2 justify-center mt-6 text-sm text-foreground cursor-pointer select-none">
          <span
            className={`w-4 h-4 rounded flex items-center justify-center border ${makeDefault ? 'bg-primary border-primary' : 'border-input'}`}
          >
            {makeDefault && <Check className="w-3 h-3 text-primary-foreground" />}
          </span>
          <input type="checkbox" className="sr-only" checked={makeDefault} onChange={e => setMakeDefault(e.target.checked)} />
          Make this my default workspace
        </label>

        <button
          onClick={() => logout()}
          className="mx-auto mt-6 flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground transition-colors"
        >
          <LogOut className="w-4 h-4" /> Sign out
        </button>
      </div>
    </div>
  );
}
