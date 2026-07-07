import { useState, useRef, useEffect } from 'react';
import { Star } from 'lucide-react';
import { useAuth } from '../../lib/auth';

export default function WorkspaceSwitcher() {
  const { activeWorkspace, workspaces, switchWorkspace, defaultOrgId } = useAuth();
  const [open, setOpen] = useState(false);
  const [switching, setSwitching] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  // The star sets a workspace as the default home (switch-workspace with
  // set_default), so the chooser is never shown again (P4).
  const handleSetDefault = async (e: React.MouseEvent, orgId: string) => {
    e.stopPropagation();
    setSwitching(true);
    try {
      await switchWorkspace(orgId, true);
    } catch {
      setSwitching(false);
    }
  };

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, []);

  const handleSwitch = async (orgId: string) => {
    if (orgId === activeWorkspace?.org_id) {
      setOpen(false);
      return;
    }
    setSwitching(true);
    try {
      await switchWorkspace(orgId);
    } catch {
      setSwitching(false);
    }
  };

  if (workspaces.length <= 1 && activeWorkspace) {
    return (
      <div className="px-3 py-2 text-sm font-semibold text-foreground truncate">
        {activeWorkspace.org_name}
      </div>
    );
  }

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen(!open)}
        disabled={switching}
        className="w-full flex items-center gap-2 px-3 py-2 rounded-lg hover:bg-accent transition-colors text-left"
      >
        <div className="flex-1 min-w-0">
          <p className="text-sm font-semibold truncate text-foreground">
            {activeWorkspace?.org_name || 'Select Workspace'}
          </p>
          <p className="text-xs text-muted-foreground capitalize">
            {activeWorkspace?.org_type} · {activeWorkspace?.role?.replace('_', ' ')}
          </p>
        </div>
        <svg className={`w-4 h-4 text-muted-foreground transition-transform ${open ? 'rotate-180' : ''}`} fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
        </svg>
      </button>

      {open && (
        <div className="absolute left-0 right-0 top-full mt-1 bg-card border border-border rounded-xl shadow-xl z-50 overflow-hidden">
          {workspaces.map(ws => {
            const isDefault = ws.org_id === defaultOrgId;
            return (
              <div
                key={ws.org_id}
                className={`flex items-center gap-2 px-4 py-3 hover:bg-accent transition-colors border-b border-border last:border-0 ${
                  ws.org_id === activeWorkspace?.org_id ? 'bg-accent/50' : ''
                }`}
              >
                <button onClick={() => handleSwitch(ws.org_id)} className="flex-1 min-w-0 text-left">
                  <p className="text-sm font-medium text-foreground truncate">{ws.org_name}</p>
                  <p className="text-xs text-muted-foreground capitalize">
                    {ws.org_type} · {ws.role?.replace('_', ' ')}
                  </p>
                </button>
                <button
                  onClick={e => handleSetDefault(e, ws.org_id)}
                  title={isDefault ? 'Default workspace' : 'Make default'}
                  className={`shrink-0 p-1 rounded-md transition-colors ${isDefault ? 'text-yellow-500' : 'text-muted-foreground hover:text-yellow-500'}`}
                >
                  <Star className={`w-4 h-4 ${isDefault ? 'fill-yellow-500' : ''}`} />
                </button>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
