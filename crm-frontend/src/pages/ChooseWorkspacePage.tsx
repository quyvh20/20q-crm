import { useState } from 'react';
import { useAuth } from '../lib/auth';
import { Building2, Users, Check, Star, Loader2, LogOut } from 'lucide-react';

/**
 * The R2 workspace chooser (P4). Shown when a user belongs to multiple active
 * workspaces and no valid default has resolved one. Picking a card calls
 * switch-workspace (optionally persisting it as the default), after which the
 * user is never asked again.
 */
export default function ChooseWorkspacePage() {
  const { workspaces, switchWorkspace, defaultOrgId, logout } = useAuth();
  const [makeDefault, setMakeDefault] = useState(true);
  const [busyOrg, setBusyOrg] = useState<string | null>(null);
  const [error, setError] = useState('');

  const active = workspaces.filter(w => w.status === 'active');

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
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-slate-900 via-slate-800 to-slate-900 p-4">
      <div className="w-full max-w-lg">
        <div className="text-center mb-8">
          <h1 className="text-3xl font-bold text-white tracking-tight">Choose a workspace</h1>
          <p className="text-slate-400 mt-2">You're a member of several workspaces. Pick one to continue.</p>
        </div>

        {error && (
          <div className="mb-4 p-3 rounded-xl bg-red-500/10 border border-red-500/20 text-red-400 text-sm">
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
                className="w-full text-left group flex items-center gap-4 p-4 rounded-2xl bg-slate-800/50 border border-slate-700/50 hover:border-blue-500/50 hover:bg-slate-800 transition-all disabled:opacity-60"
              >
                <div className="w-11 h-11 rounded-xl bg-gradient-to-tr from-purple-500 to-blue-500 flex items-center justify-center shrink-0">
                  <Building2 className="w-5 h-5 text-white" />
                </div>
                <div className="flex-1 min-w-0">
                  <p className="text-white font-semibold truncate flex items-center gap-2">
                    {ws.org_name}
                    {isDefault && <Star className="w-3.5 h-3.5 text-yellow-400 fill-yellow-400" />}
                  </p>
                  <p className="text-xs text-slate-400 capitalize flex items-center gap-2 mt-0.5">
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
                  <Loader2 className="w-5 h-5 text-blue-400 animate-spin shrink-0" />
                ) : (
                  <span className="text-slate-600 group-hover:text-blue-400 transition-colors shrink-0">→</span>
                )}
              </button>
            );
          })}
        </div>

        <label className="flex items-center gap-2 justify-center mt-6 text-sm text-slate-300 cursor-pointer select-none">
          <span
            className={`w-4 h-4 rounded flex items-center justify-center border ${makeDefault ? 'bg-blue-500 border-blue-500' : 'border-slate-600'}`}
          >
            {makeDefault && <Check className="w-3 h-3 text-white" />}
          </span>
          <input type="checkbox" className="sr-only" checked={makeDefault} onChange={e => setMakeDefault(e.target.checked)} />
          Make this my default workspace
        </label>

        <button
          onClick={() => logout()}
          className="mx-auto mt-6 flex items-center gap-2 text-sm text-slate-500 hover:text-slate-300 transition-colors"
        >
          <LogOut className="w-4 h-4" /> Sign out
        </button>
      </div>
    </div>
  );
}
