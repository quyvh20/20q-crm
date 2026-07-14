import { useState, useEffect, useCallback } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Lock, Users, AlertTriangle, ChevronRight } from 'lucide-react';
import {
  getRoles,
  getRolesCatalog,
  createRole,
  duplicateRole,
  deleteRole,
  ALL_CAPABILITIES,
  type RoleDetail,
  type DataScope,
} from '../../lib/api';
import { useAuth } from '../../lib/auth';
import { prettyRole } from '../../lib/roles';
import { useConfirm } from '../common/ConfirmDialog';

// Row scope, one line per card (U6). Teams are user groups.
const SCOPE_LABELS: Record<DataScope, string> = {
  own: 'Sees own + shared records',
  team: 'Sees their teams’ records',
  all: 'Sees all records',
};

// RolesManager is the roles LIST (U3.1): create/duplicate/delete plus a card per
// role that links into the role detail page, where everything about one role —
// capabilities, object/field access, data scope, members — lives on a single
// pivot. Every authorization layer keys off role_id, so a role created here
// works everywhere. Start a role from a template (which copies its capabilities
// + object/field access, avoiding the invisible no-access trap), then tune it
// on its detail page.
export default function RolesManager() {
  const navigate = useNavigate();
  const { hasCapability } = useAuth();
  const [roles, setRoles] = useState<RoleDetail[]>([]);
  const [totalCaps, setTotalCaps] = useState<number>(ALL_CAPABILITIES.length);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [busyId, setBusyId] = useState('');

  // New-role form state. cloneFrom defaults to the viewer template so a new role
  // starts with safe read-only access rather than no access at all.
  const [newName, setNewName] = useState('');
  const [newDesc, setNewDesc] = useState('');
  const [cloneFrom, setCloneFrom] = useState('');
  const [creating, setCreating] = useState(false);

  // Modals: duplicate a role, or delete a role whose members must be reassigned.
  const [dupTarget, setDupTarget] = useState<RoleDetail | null>(null);
  const [dupName, setDupName] = useState('');
  const [dupReassign, setDupReassign] = useState(false);
  const [delTarget, setDelTarget] = useState<RoleDetail | null>(null);
  const [reassignTo, setReassignTo] = useState('');
  const { confirm: confirmDialog, dialog: confirmDialogEl } = useConfirm();

  const canSeeMembers = hasCapability('members.manage') || hasCapability('members.invite');

  const load = useCallback(async () => {
    try {
      setLoading(true);
      const [rs, cat] = await Promise.all([getRoles(), getRolesCatalog().catch(() => ({ capabilities: [], groups: [] }))]);
      setRoles(rs);
      if (cat.capabilities.length > 0) setTotalCaps(cat.capabilities.length);
      // Default the create form's template to viewer (safe read-only start).
      setCloneFrom((cur) => cur || rs.find((r) => r.name === 'viewer')?.id || '');
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load roles');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const handleCreate = async () => {
    if (!newName.trim()) return;
    setCreating(true);
    setError('');
    try {
      const created = await createRole({
        name: newName.trim(),
        description: newDesc.trim() || undefined,
        clone_from_id: cloneFrom || undefined,
      });
      setNewName('');
      setNewDesc('');
      // Land on the new role's detail page — that's where it gets tuned.
      navigate(`/settings/roles/${created.id}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to create role');
    } finally {
      setCreating(false);
    }
  };

  // Delete: a role nobody holds is deleted directly; a role with members opens the
  // reassign picker (the backend also enforces this with a 409).
  const startDelete = async (role: RoleDetail) => {
    if (role.member_count > 0) {
      setReassignTo('');
      setDelTarget(role);
      return;
    }
    if (!(await confirmDialog({
      title: `Delete the "${prettyRole(role.name)}" role`,
      body: 'This cannot be undone. Nobody currently holds this role.',
      confirmLabel: 'Delete role',
    }))) return;
    runDelete(role, undefined);
  };

  const runDelete = async (role: RoleDetail, reassign?: string) => {
    setBusyId(role.id);
    setError('');
    try {
      await deleteRole(role.id, reassign);
      setDelTarget(null);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to delete role');
    } finally {
      setBusyId('');
    }
  };

  const openDuplicate = (role: RoleDetail) => {
    setDupTarget(role);
    setDupName(`${role.name} copy`);
    setDupReassign(false);
  };

  const runDuplicate = async () => {
    if (!dupTarget || !dupName.trim()) return;
    setBusyId(dupTarget.id);
    setError('');
    try {
      const copy = await duplicateRole(dupTarget.id, { name: dupName.trim(), reassign_members: dupReassign });
      setDupTarget(null);
      navigate(`/settings/roles/${copy.id}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to duplicate role');
    } finally {
      setBusyId('');
    }
  };

  if (loading) return <div className="text-sm text-muted-foreground py-8">Loading roles…</div>;

  return (
    <div className="space-y-5">
      <div>
        <h3 className="text-lg font-semibold">Roles</h3>
        <p className="text-sm text-muted-foreground mt-1">
          A role decides what its members can see and do. Open a role to review or change its
          access; create new roles from a template so they start with sensible access.
        </p>
      </div>

      {error && <div className="bg-red-50 text-red-700 text-sm rounded-md px-3 py-2">{error}</div>}

      {/* Create / clone a role */}
      <div className="border rounded-lg p-3 bg-muted/20 space-y-2">
        <div className="flex flex-wrap items-end gap-2">
          <div className="flex flex-col">
            <label className="text-xs font-medium mb-1">New role name</label>
            <input
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              placeholder="e.g. Support Agent"
              className="border rounded-md px-2.5 py-1.5 text-sm bg-background w-52"
            />
          </div>
          <div className="flex flex-col flex-1 min-w-[12rem]">
            <label className="text-xs font-medium mb-1">Description (optional)</label>
            <input
              value={newDesc}
              onChange={(e) => setNewDesc(e.target.value)}
              placeholder="What this role is for"
              className="border rounded-md px-2.5 py-1.5 text-sm bg-background w-full"
            />
          </div>
          <div className="flex flex-col">
            <label className="text-xs font-medium mb-1">Start from</label>
            <select
              value={cloneFrom}
              onChange={(e) => setCloneFrom(e.target.value)}
              className="border rounded-md px-2.5 py-1.5 text-sm bg-background w-44"
            >
              <option value="">Blank (no access)</option>
              {roles.map((r) => (
                <option key={r.id} value={r.id}>{prettyRole(r.name)}</option>
              ))}
            </select>
          </div>
          <button
            onClick={handleCreate}
            disabled={creating || !newName.trim()}
            className="px-3 py-1.5 text-sm rounded-md bg-blue-500 text-white hover:bg-blue-600 disabled:opacity-50"
          >
            {creating ? 'Creating…' : 'Create role'}
          </button>
        </div>
        {!cloneFrom && (
          <p className="flex items-start gap-1.5 text-xs text-amber-600">
            <AlertTriangle className="h-3.5 w-3.5 mt-px shrink-0" aria-hidden="true" />
            A blank role starts with no access to any object — members will see nothing until
            you grant access on the role's page.
          </p>
        )}
      </div>

      {/* Role cards → detail */}
      <div className="grid gap-3 sm:grid-cols-2">
        {roles.map((role) => {
          // Tri-state row scope (U6): 'team' = every record owned by someone in a
          // user group they belong to.
          const scopeText = role.is_owner
            ? 'Sees everything'
            : SCOPE_LABELS[role.data_scope] ?? SCOPE_LABELS.own;
          const capsText = role.is_owner
            ? 'All permissions'
            : `${role.capabilities.length} of ${totalCaps} permissions`;
          return (
            <div
              key={role.id}
              className="relative border rounded-lg p-4 hover:border-blue-400 transition-colors"
            >
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <Link
                    to={`/settings/roles/${role.id}`}
                    className="font-medium hover:underline after:absolute after:inset-0 after:content-['']"
                  >
                    {prettyRole(role.name)}
                  </Link>
                  <div className="mt-1 flex items-center gap-1.5 flex-wrap">
                    {role.is_owner && (
                      <span className="inline-flex items-center gap-1 text-xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground">
                        <Lock className="w-3 h-3" aria-hidden="true" /> Full access
                      </span>
                    )}
                    <span className="text-xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground">
                      {role.is_system ? 'Built-in' : 'Custom'}
                    </span>
                    {!role.is_system && role.template_key && (
                      <span className="text-xs text-muted-foreground italic">from {role.template_key} template</span>
                    )}
                  </div>
                </div>
                <ChevronRight className="w-4 h-4 text-muted-foreground shrink-0 mt-1" aria-hidden="true" />
              </div>

              {role.description && (
                <p className="text-xs text-muted-foreground mt-2 line-clamp-2">{role.description}</p>
              )}

              <div className="mt-3 flex items-center gap-3 text-xs text-muted-foreground flex-wrap">
                {/* Member count cross-links into Members filtered to this role
                    (U3.3), even at 0 — matching the role detail page; z-elevated
                    above the card's stretched link overlay. */}
                {canSeeMembers ? (
                  <Link
                    to={`/settings/members?role=${role.id}`}
                    className="relative z-10 inline-flex items-center gap-1 text-blue-600 hover:underline"
                  >
                    <Users className="w-3.5 h-3.5" aria-hidden="true" />
                    {role.member_count} member{role.member_count === 1 ? '' : 's'}
                  </Link>
                ) : (
                  <span className="inline-flex items-center gap-1">
                    <Users className="w-3.5 h-3.5" aria-hidden="true" />
                    {role.member_count} member{role.member_count === 1 ? '' : 's'}
                  </span>
                )}
                <span>{capsText}</span>
                <span>{scopeText}</span>
              </div>

              <div className="mt-3 flex items-center gap-3 text-xs">
                {!role.is_owner && (
                  <button
                    onClick={() => openDuplicate(role)}
                    disabled={busyId === role.id}
                    className="relative z-10 text-blue-600 hover:underline disabled:opacity-50"
                  >
                    Duplicate
                  </button>
                )}
                {!role.is_system && (
                  <button
                    onClick={() => startDelete(role)}
                    disabled={busyId === role.id}
                    className="relative z-10 text-red-600 hover:underline disabled:opacity-50"
                  >
                    Delete
                  </button>
                )}
              </div>
            </div>
          );
        })}
      </div>

      {/* Duplicate modal */}
      {dupTarget && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
          <div className="absolute inset-0 bg-black/50" onClick={() => setDupTarget(null)} />
          <div className="relative bg-card border rounded-2xl shadow-xl w-full max-w-sm p-5">
            <h3 className="text-base font-semibold mb-1">Duplicate “{prettyRole(dupTarget.name)}”</h3>
            <p className="text-xs text-muted-foreground mb-3">
              Creates a new custom role with the same capabilities and object/field access, which you can then tune.
            </p>
            <label className="block text-xs font-medium mb-1">New role name</label>
            <input
              value={dupName}
              onChange={(e) => setDupName(e.target.value)}
              className="w-full border rounded-md px-2.5 py-1.5 text-sm bg-background mb-3"
            />
            {dupTarget.member_count > 0 && (
              <label className="flex items-center gap-2 text-sm mb-4">
                <input type="checkbox" checked={dupReassign} onChange={(e) => setDupReassign(e.target.checked)} className="h-4 w-4" />
                Move this role's {dupTarget.member_count} member{dupTarget.member_count === 1 ? '' : 's'} to the copy
              </label>
            )}
            <div className="flex gap-2">
              <button onClick={() => setDupTarget(null)} className="flex-1 px-3 py-1.5 border rounded-md text-sm hover:bg-accent">Cancel</button>
              <button
                onClick={runDuplicate}
                disabled={!dupName.trim() || busyId === dupTarget.id}
                className="flex-1 px-3 py-1.5 bg-blue-500 text-white rounded-md text-sm font-medium hover:bg-blue-600 disabled:opacity-50"
              >
                Duplicate
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Delete-with-reassign modal */}
      {delTarget && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
          <div className="absolute inset-0 bg-black/50" onClick={() => setDelTarget(null)} />
          <div className="relative bg-card border rounded-2xl shadow-xl w-full max-w-sm p-5">
            <h3 className="text-base font-semibold mb-1">Delete “{prettyRole(delTarget.name)}”</h3>
            <p className="text-sm text-muted-foreground mb-3">
              {delTarget.member_count} member{delTarget.member_count === 1 ? '' : 's'} still {delTarget.member_count === 1 ? 'holds' : 'hold'} this role.
              Move them to another role, then it will be deleted.
            </p>
            <label className="block text-xs font-medium mb-1">Move members to</label>
            <select
              value={reassignTo}
              onChange={(e) => setReassignTo(e.target.value)}
              className="w-full border rounded-md px-2.5 py-1.5 text-sm bg-background mb-4"
            >
              <option value="">-- Select a role --</option>
              {roles.filter((r) => r.id !== delTarget.id && !r.is_owner).map((r) => (
                <option key={r.id} value={r.id}>{prettyRole(r.name)}</option>
              ))}
            </select>
            <div className="flex gap-2">
              <button onClick={() => setDelTarget(null)} className="flex-1 px-3 py-1.5 border rounded-md text-sm hover:bg-accent">Cancel</button>
              <button
                onClick={() => runDelete(delTarget, reassignTo)}
                disabled={!reassignTo || busyId === delTarget.id}
                className="flex-1 px-3 py-1.5 bg-red-500 text-white rounded-md text-sm font-medium hover:bg-red-600 disabled:opacity-50"
              >
                Reassign & delete
              </button>
            </div>
          </div>
        </div>
      )}
      {confirmDialogEl}
    </div>
  );
}
