import { useState } from 'react';
import { useAuth } from '../lib/auth';
import { Inbox, LogOut, Loader2, ArrowRight } from 'lucide-react';

/**
 * The R2 zero-membership dead-end (P4). A user who authenticates but belongs to
 * no active workspace used to get a token bound to uuid.Nil and silent 403s
 * everywhere; now they land here with an explanation instead.
 */
export default function NoWorkspacePage() {
  const { user, logout, createWorkspace } = useAuth();
  // Inline create (U4) — the old "Create a workspace" link went to /register,
  // which PublicRoute bounced right back here for an already-authenticated user
  // (the redirect loop). Creating in place via POST /workspaces switches them
  // straight into the new workspace.
  const [name, setName] = useState('');
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState('');

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

  return (
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-slate-900 via-slate-800 to-slate-900 p-4">
      <div className="w-full max-w-md text-center">
        <div className="w-16 h-16 mx-auto mb-6 rounded-2xl bg-slate-800/70 border border-slate-700/50 flex items-center justify-center">
          <Inbox className="w-8 h-8 text-slate-400" />
        </div>
        <h1 className="text-2xl font-bold text-white tracking-tight mb-2">You're not in any workspace</h1>
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

        <form onSubmit={submit} className="flex flex-col gap-3">
          {error && (
            <div className="bg-red-500/10 border border-red-500/20 text-red-300 text-sm rounded-xl px-3 py-2">{error}</div>
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
            disabled={creating || !name.trim()}
            className="w-full py-3 px-4 bg-gradient-to-r from-blue-600 to-blue-500 hover:from-blue-500 hover:to-blue-400 text-white font-semibold rounded-xl transition-all disabled:opacity-60 flex items-center justify-center gap-2"
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
