import { useCallback, useEffect, useState } from 'react';
import {
  listGroups, createGroup, deleteGroup, addGroupMember, removeGroupMember,
  getWorkspaceMembers, type UserGroup, type WorkspaceMember,
} from '../../lib/api';
import { useConfirm } from '../common/ConfirmDialog';

// GroupsManager: create named member groups and manage their membership. Groups
// are a sharing target for reports. Gated by the groups.manage capability at the
// call site (the settings shell's Groups section).
export default function GroupsManager() {
  const [groups, setGroups] = useState<UserGroup[]>([]);
  const [members, setMembers] = useState<WorkspaceMember[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [newName, setNewName] = useState('');
  const [expanded, setExpanded] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const { confirm, dialog } = useConfirm();

  const load = useCallback(async () => {
    try {
      setLoading(true);
      const [g, m] = await Promise.all([listGroups(), getWorkspaceMembers()]);
      setGroups(g);
      setMembers(m.filter((x) => x.status === 'active'));
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load groups');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const create = async () => {
    if (!newName.trim()) return;
    setBusy(true); setError('');
    try {
      await createGroup(newName.trim());
      setNewName('');
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to create group');
    } finally { setBusy(false); }
  };

  const remove = async (id: string, name: string) => {
    if (!(await confirm({ title: `Delete "${name}"`, body: 'Reports shared with this group lose that share. Members themselves are not affected.', confirmLabel: 'Delete group' }))) return;
    setBusy(true); setError('');
    try { await deleteGroup(id); await load(); }
    catch (e) { setError(e instanceof Error ? e.message : 'Failed to delete group'); }
    finally { setBusy(false); }
  };

  const toggleMember = async (group: UserGroup, userId: string, isMember: boolean) => {
    setBusy(true); setError('');
    try {
      if (isMember) await removeGroupMember(group.id, userId);
      else await addGroupMember(group.id, userId);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to update membership');
    } finally { setBusy(false); }
  };

  if (loading) return <div className="h-24 animate-pulse rounded-xl bg-muted/50" />;

  return (
    <div className="space-y-4">
      {error && <div className="text-sm text-red-600">{error}</div>}

      <div className="flex gap-2">
        <input
          aria-label="New group name"
          value={newName}
          onChange={(e) => setNewName(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') create(); }}
          placeholder="New group name (e.g. West Region)"
          className="flex-1 rounded-md border bg-background px-3 py-2 text-sm"
        />
        <button
          onClick={create}
          disabled={busy || !newName.trim()}
          className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
        >
          Create group
        </button>
      </div>

      {groups.length === 0 ? (
        <div className="rounded-xl border border-dashed p-6 text-center text-sm text-muted-foreground">
          No groups yet. Create one above, then add members and share reports with it.
        </div>
      ) : (
        <div className="space-y-2">
          {groups.map((g) => {
            const memberIds = new Set(g.members.map((m) => m.user_id));
            const open = expanded === g.id;
            return (
              <div key={g.id} className="rounded-xl border bg-card">
                <div className="flex items-center gap-3 px-4 py-3">
                  <button
                    onClick={() => setExpanded(open ? null : g.id)}
                    className="flex-1 text-left"
                  >
                    <span className="font-medium">{g.name}</span>
                    <span className="ml-2 text-xs text-muted-foreground">{g.member_count} member{g.member_count === 1 ? '' : 's'}</span>
                  </button>
                  <button onClick={() => setExpanded(open ? null : g.id)} className="text-xs text-muted-foreground hover:text-foreground">
                    {open ? 'Hide members' : 'Manage members'}
                  </button>
                  <button onClick={() => remove(g.id, g.name)} className="rounded px-2 py-1 text-xs text-red-600 hover:bg-red-50 dark:hover:bg-red-950">Delete</button>
                </div>
                {open && (
                  <div className="border-t px-4 py-3">
                    <div className="max-h-64 space-y-1 overflow-auto">
                      {members.map((m) => {
                        const isMember = memberIds.has(m.user_id);
                        return (
                          <label key={m.user_id} className="flex cursor-pointer items-center gap-2 rounded px-2 py-1 text-sm hover:bg-accent">
                            <input
                              type="checkbox"
                              checked={isMember}
                              disabled={busy}
                              onChange={() => toggleMember(g, m.user_id, isMember)}
                            />
                            <span>{m.full_name || `${m.first_name} ${m.last_name}`.trim() || m.email}</span>
                            <span className="text-xs text-muted-foreground">{m.email}</span>
                          </label>
                        );
                      })}
                      {members.length === 0 && <div className="text-sm text-muted-foreground">No active members.</div>}
                    </div>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
      {dialog}
    </div>
  );
}
