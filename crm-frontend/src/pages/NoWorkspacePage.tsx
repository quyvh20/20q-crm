import { useState } from 'react';
import { useAuth } from '../lib/auth';
import { Inbox, LogOut } from 'lucide-react';

/**
 * The R2 zero-membership dead-end (P4). A user who authenticates but belongs to
 * no active workspace used to get a token bound to uuid.Nil and silent 403s
 * everywhere; now they land here with an explanation instead.
 */
export default function NoWorkspacePage() {
  const { user, logout } = useAuth();
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

        <div className="flex flex-col gap-3">
          <a
            href="/register"
            className="w-full py-3 px-4 bg-gradient-to-r from-blue-600 to-blue-500 hover:from-blue-500 hover:to-blue-400 text-white font-semibold rounded-xl transition-all"
          >
            Create a workspace
          </a>
          <button
            onClick={() => logout()}
            className="mx-auto flex items-center gap-2 text-sm text-slate-500 hover:text-slate-300 transition-colors"
          >
            <LogOut className="w-4 h-4" /> Sign out
          </button>
        </div>
      </div>
    </div>
  );
}
