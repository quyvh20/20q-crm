import { useMemo, useState } from 'react';
import { Pencil, UsersRound } from 'lucide-react';
import type { UserGroup } from '../../lib/api';
import {
  useGroups,
  useWorkspaceMembers,
  useCreateGroup,
  useUpdateGroup,
  useDeleteGroup,
  useToggleGroupMember,
} from '../../features/settings/queries';
import { useConfirm } from '../common/ConfirmDialog';
import { Button, EmptyState, Input, Skeleton } from '@/components/ui';

// GroupsManager: create named member groups, rename them, and manage membership.
// A group IS a team (U6): it defines the 'team' data scope (a team-scoped role
// sees every record owned by anyone in a group they share) AND it is a share
// target for records and reports. Gated by the groups.manage capability at the
// call site (the settings shell's Groups section).
//
// Groups and members are react-query caches (U7.3): every mutation invalidates the
// group list rather than refetching by hand, so a concurrent admin's new group or
// membership change shows up instead of being overwritten by a stale local copy.
export default function GroupsManager() {
  const [error, setError] = useState('');
  const [newName, setNewName] = useState('');
  const [expanded, setExpanded] = useState<string | null>(null);
  // Edit-in-place for a group's name + description (the PATCH route was live but
  // no UI ever called it).
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editName, setEditName] = useState('');
  const [editDesc, setEditDesc] = useState('');
  const { confirm, dialog } = useConfirm();

  const { data: groups = [], isLoading, error: groupsError } = useGroups();
  const { data: allMembers = [], error: membersError } = useWorkspaceMembers();
  const members = useMemo(() => allMembers.filter((m) => m.status === 'active'), [allMembers]);

  const createMut = useCreateGroup();
  const updateMut = useUpdateGroup();
  const deleteMut = useDeleteGroup();
  const toggleMut = useToggleGroupMember();
  const busy = createMut.isPending || updateMut.isPending || deleteMut.isPending || toggleMut.isPending;

  const fail = (fallback: string) => (e: unknown) =>
    setError(e instanceof Error ? e.message : fallback);

  const create = () => {
    if (!newName.trim()) return;
    setError('');
    createMut.mutate(newName.trim(), {
      onSuccess: () => setNewName(''),
      onError: fail('Failed to create group'),
    });
  };

  const startEdit = (g: UserGroup) => {
    setEditingId(g.id);
    setEditName(g.name);
    setEditDesc(g.description || '');
  };

  const saveEdit = (id: string) => {
    const name = editName.trim();
    if (!name) return;
    setError('');
    updateMut.mutate(
      { id, name, description: editDesc.trim() },
      { onSuccess: () => setEditingId(null), onError: fail('Failed to rename group') },
    );
  };

  const remove = async (id: string, name: string) => {
    if (!(await confirm({
      title: `Delete "${name}"`,
      body: 'Members who could only see this team\'s records lose that access, and records/reports shared with the group lose that share. The members themselves are not affected.',
      confirmLabel: 'Delete group',
      tone: 'danger',
    }))) return;
    setError('');
    deleteMut.mutate(id, { onError: fail('Failed to delete group') });
  };

  const toggleMember = (group: UserGroup, userId: string, isMember: boolean) => {
    setError('');
    toggleMut.mutate(
      { groupId: group.id, userId, isMember },
      { onError: fail('Failed to update membership') },
    );
  };

  if (isLoading) return <Skeleton className="h-24 rounded-xl" />;

  const loadError = groupsError || membersError;
  const banner = error || (loadError instanceof Error ? loadError.message : '');

  return (
    <div className="space-y-4">
      {banner && <div className="text-sm text-destructive">{banner}</div>}

      <div className="flex gap-2">
        <Input
          aria-label="New group name"
          value={newName}
          onChange={(e) => setNewName(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') create(); }}
          placeholder="New group name (e.g. West Region)"
          className="flex-1"
        />
        <Button onClick={create} disabled={busy || !newName.trim()}>
          Create group
        </Button>
      </div>

      {groups.length === 0 ? (
        <EmptyState
          icon={UsersRound}
          title="No groups yet."
          description="Create one above, then add members — a group is a team you can scope roles to and share records and reports with."
        />
      ) : (
        <div className="space-y-2">
          {groups.map((g) => {
            // (g.members ?? []): the backend sends `"members": null` for a
            // zero-member group (Go nil slice) — unguarded .map threw on the
            // first render and white-screened the page under the error boundary.
            const memberIds = new Set((g.members ?? []).map((m) => m.user_id));
            const open = expanded === g.id;
            const editing = editingId === g.id;
            return (
              <div key={g.id} className="rounded-xl border border-border bg-card">
                {editing ? (
                  <div className="space-y-2 px-4 py-3">
                    <Input
                      aria-label="Group name"
                      value={editName}
                      onChange={(e) => setEditName(e.target.value)}
                      onKeyDown={(e) => { if (e.key === 'Enter') saveEdit(g.id); }}
                      className="font-medium"
                    />
                    <Input
                      aria-label="Group description"
                      value={editDesc}
                      onChange={(e) => setEditDesc(e.target.value)}
                      onKeyDown={(e) => { if (e.key === 'Enter') saveEdit(g.id); }}
                      placeholder="What this team is for (optional)"
                    />
                    <div className="flex gap-2">
                      <Button size="sm" onClick={() => saveEdit(g.id)} disabled={busy || !editName.trim()}>
                        Save
                      </Button>
                      <Button size="sm" variant="outline" onClick={() => setEditingId(null)} disabled={busy}>
                        Cancel
                      </Button>
                    </div>
                  </div>
                ) : (
                  <div className="flex items-center gap-3 px-4 py-3">
                    <button
                      type="button"
                      onClick={() => setExpanded(open ? null : g.id)}
                      className="min-w-0 flex-1 rounded text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    >
                      <span className="font-medium">{g.name}</span>
                      <span className="ml-2 text-xs text-muted-foreground">{g.member_count} member{g.member_count === 1 ? '' : 's'}</span>
                      {g.description && <div className="truncate text-xs text-muted-foreground">{g.description}</div>}
                    </button>
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => startEdit(g)}
                      aria-label={`Edit ${g.name}`}
                      className="h-7 w-7 text-muted-foreground hover:text-foreground"
                    >
                      <Pencil className="h-3.5 w-3.5" aria-hidden="true" />
                    </Button>
                    <Button variant="ghost" size="sm" onClick={() => setExpanded(open ? null : g.id)} className="text-muted-foreground hover:text-foreground">
                      {open ? 'Hide members' : 'Manage members'}
                    </Button>
                    <Button variant="ghost" size="sm" onClick={() => remove(g.id, g.name)} className="text-destructive hover:bg-destructive/10 hover:text-destructive">Delete</Button>
                  </div>
                )}
                {open && (
                  <div className="border-t border-border px-4 py-3">
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
