import { useEffect, useState } from 'react';
import { useAuth } from '../lib/auth';
import { getMyInvitations, type IncomingInvitation } from '../lib/api';
import { prettyRole } from '../lib/roles';
import { useDocumentTitle } from '../lib/useDocumentTitle';
import { Inbox, LogOut, Loader2, ArrowRight, Building2 } from 'lucide-react';

/**
 * The R2 zero-membership dead-end (P4). A user who authenticates but belongs to
 * no active workspace used to get a token bound to uuid.Nil and silent 403s
 * everywhere; now they land here with an explanation instead.
 *
 * U4 item 6: this is also where a brand-new Google invitee lands (no junk personal
 * org is forked for them) — so we surface the invitations addressed to their email
 * for EXPLICIT acceptance (never a silent auto-join), alongside the create-your-own
 * path. Accepting joins that workspace and lands them in it.
 */
export default function NoWorkspacePage() {
  useDocumentTitle('No Workspace');
  const { user, logout, createWorkspace, acceptMyInvitation } = useAuth();

  // Pending invitations addressed to this user's email (U4 item 6).
  const [invites, setInvites] = useState<IncomingInvitation[]>([]);
  const [invitesLoading, setInvitesLoading] = useState(true);
  const [invitesError, setInvitesError] = useState(false);
  const [acceptingId, setAcceptingId] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    getMyInvitations()
      .then((list) => { if (!cancelled) { setInvites(list); setInvitesError(false); } })
      // Distinguish a fetch error from a genuinely-empty list: collapsing both to
      // "no invites" would tell a real invitee (a transient blip on this one call)
      // they have none and nudge them to create the junk workspace this feature
      // exists to prevent. On error we flag it and prompt a reload instead.
      .catch(() => { if (!cancelled) setInvitesError(true); })
      .finally(() => { if (!cancelled) setInvitesLoading(false); });
    return () => { cancelled = true; };
  }, []);

  // Inline create (U4) — the old "Create a workspace" link went to /register,
  // which PublicRoute bounced right back here for an already-authenticated user
  // (the redirect loop). Creating in place via POST /workspaces switches them
  // straight into the new workspace.
  const [name, setName] = useState('');
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState('');

  const accept = async (inv: IncomingInvitation) => {
    setError('');
    setAcceptingId(inv.id);
    try {
      await acceptMyInvitation(inv.id);
      // saveAuth switched us into the joined workspace; go to the app.
      window.location.assign('/');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to accept invitation');
      setAcceptingId(null);
    }
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;
    setCreating(true);
    setError('');
    try {
      await createWorkspace({ name: name.trim() });
      // saveAuth switched the active org; go to the app.
      window.location.assign('/');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create workspace');
      setCreating(false);
    }
  };
  // If a refresh 409 bounced us here off an org we just lost (and it was our only
  // one), name it so the message isn't a mysterious "you're in no workspace".
  const [lostWorkspace] = useState<string | null>(() => {
    const name = sessionStorage.getItem('lost_workspace_name');
    if (name) sessionStorage.removeItem('lost_workspace_name');
    return name;
  });

  const hasInvites = invites.length > 0;
  const busy = acceptingId !== null || creating;

  return (
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-slate-900 via-slate-800 to-slate-900 p-4">
      <div className="w-full max-w-md text-center">
        <div className="w-16 h-16 mx-auto mb-6 rounded-2xl bg-slate-800/70 border border-slate-700/50 flex items-center justify-center">
          <Inbox className="w-8 h-8 text-slate-400" />
        </div>
        <h1 className="text-2xl font-bold text-white tracking-tight mb-2">
          {hasInvites ? "You've been invited" : "You're not in any workspace"}
        </h1>
        {hasInvites ? (
          <p className="text-slate-400 mb-6">
            {user?.email && <>You're signed in as <span className="text-slate-200">{user.email}</span>. </>}
            Join a workspace you've been invited to, or create your own.
          </p>
        ) : (
          <>
            <p className="text-slate-400 mb-2">
              {lostWorkspace ? (
                <>You no longer have access to <span className="text-slate-200">{lostWorkspace}</span>, and you're </>
              ) : user?.email ? (
                <>You're signed in as <span className="text-slate-200">{user.email}</span>, but you're </>
              ) : (
                'You '
              )}
              not currently a member of any active workspace.
            </p>
            <p className="text-slate-400 mb-8">
              Ask a workspace admin to invite you, or create your own to get started.
            </p>
          </>
        )}

        {invitesError && (
          <div className="bg-amber-500/10 border border-amber-500/20 text-amber-300 text-sm rounded-xl px-3 py-2 mb-4 text-left">
            We couldn't check your invitations right now.{' '}
            <button type="button" onClick={() => window.location.reload()} className="underline hover:text-amber-200">
              Reload to try again
            </button>{' '}
            before creating a new workspace.
          </div>
        )}

        {error && (
          <div className="bg-red-500/10 border border-red-500/20 text-red-300 text-sm rounded-xl px-3 py-2 mb-4 text-left">{error}</div>
        )}

        {invitesLoading ? (
          <div className="flex items-center justify-center gap-2 text-slate-400 py-6">
            <Loader2 className="w-4 h-4 animate-spin" /> Checking for invitations…
          </div>
        ) : (
          hasInvites && (
            <div className="flex flex-col gap-3 mb-6">
              {invites.map((inv) => (
                <div
                  key={inv.id}
                  className="flex items-center gap-3 bg-slate-800/60 border border-slate-700 rounded-xl px-4 py-3 text-left"
                >
                  <div className="w-9 h-9 shrink-0 rounded-lg bg-blue-500/15 border border-blue-500/20 flex items-center justify-center">
                    <Building2 className="w-4 h-4 text-blue-300" />
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="text-white font-medium truncate">{inv.org_name}</div>
                    {inv.role_name && (
                      <div className="text-xs text-slate-400">Join as {prettyRole(inv.role_name)}</div>
                    )}
                  </div>
                  <button
                    type="button"
                    onClick={() => accept(inv)}
                    disabled={busy}
                    className="shrink-0 py-2 px-4 bg-gradient-to-r from-blue-600 to-blue-500 hover:from-blue-500 hover:to-blue-400 text-white text-sm font-semibold rounded-lg transition-all disabled:opacity-60 flex items-center gap-2"
                  >
                    {acceptingId === inv.id ? <Loader2 className="w-4 h-4 animate-spin" /> : 'Join'}
                  </button>
                </div>
              ))}
            </div>
          )
        )}

        <form onSubmit={submit} className="flex flex-col gap-3">
          {hasInvites && (
            <div className="flex items-center gap-3 text-xs text-slate-500 my-1">
              <span className="h-px flex-1 bg-slate-700/60" /> or create your own <span className="h-px flex-1 bg-slate-700/60" />
            </div>
          )}
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Name your workspace"
            aria-label="Workspace name"
            className="w-full px-4 py-3 bg-slate-800/60 border border-slate-700 rounded-xl text-white placeholder-slate-500 focus:outline-none focus:border-blue-500 transition-colors"
          />
          <button
            type="submit"
            disabled={busy || !name.trim()}
            className="w-full py-3 px-4 bg-slate-700/70 hover:bg-slate-700 text-white font-semibold rounded-xl transition-all disabled:opacity-60 flex items-center justify-center gap-2"
          >
            {creating ? <><Loader2 className="w-4 h-4 animate-spin" /> Creating…</> : <>Create a workspace <ArrowRight className="w-4 h-4" /></>}
          </button>
          <button
            type="button"
            onClick={() => logout()}
            className="mx-auto flex items-center gap-2 text-sm text-slate-500 hover:text-slate-300 transition-colors"
          >
            <LogOut className="w-4 h-4" /> Sign out
          </button>
        </form>
      </div>
    </div>
  );
}
