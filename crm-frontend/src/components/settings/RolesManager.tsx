import { useState, useEffect, useCallback, useMemo } from 'react';
import {
  getRoles,
  getRolesCatalog,
  createRole,
  duplicateRole,
  deleteRole,
  updateRole,
  setRoleCapabilities,
  ALL_CAPABILITIES,
  CAPABILITY_LABELS,
  type RoleDetail,
  type CapabilityInfo,
  type DataScope,
} from '../../lib/api';

// RolesManager is the admin surface for custom roles (P3/P6). Every authorization
// layer keys off role_id, so a role created here works everywhere: its
// capabilities gate admin actions, its OLS/FLS grids (Permissions tab) gate data,
// and its data_scope controls row visibility. Start a role from a template (which
// copies its capabilities + object/field access, avoiding the zero-access trap),
// then tune it. Sensitive ⚠ capabilities are flagged. The owner role is locked.
export default function RolesManager() {
  const [roles, setRoles] = useState<RoleDetail[]>([]);
  const [catalog, setCatalog] = useState<CapabilityInfo[]>([]);
  const [groups, setGroups] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [busyId, setBusyId] = useState('');

  // New-role form state. cloneFrom defaults to the viewer template so a new role
  // starts with safe read-only access rather than the invisible zero-OLS trap.
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

  const load = useCallback(async () => {
    try {
      setLoading(true);
      const [rs, cat] = await Promise.all([getRoles(), getRolesCatalog().catch(() => ({ capabilities: [], groups: [] }))]);
      setRoles(rs);
      setCatalog(cat.capabilities);
      setGroups(cat.groups);
      // Default the create form's template to viewer (safe read-only start).
      setCloneFrom((cur) => cur || rs.find((r) => r.name === 'viewer')?.id || '');
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load roles');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  // Capabilities grouped for display: catalog order (grouped, with ⚠ chips) when
  // the catalog loaded, else a single "Capabilities" group over the flat fallback.
  const grouped = useMemo(() => {
    if (catalog.length === 0) {
      return [{ group: 'Capabilities', caps: ALL_CAPABILITIES.map((code) => ({ code, label: CAPABILITY_LABELS[code] || code, description: '', group: '', sensitive: false })) }];
    }
    return groups
      .map((g) => ({ group: g, caps: catalog.filter((c) => c.group === g) }))
      .filter((s) => s.caps.length > 0);
  }, [catalog, groups]);

  const handleCreate = async () => {
    if (!newName.trim()) return;
    setCreating(true);
    setError('');
    try {
      await createRole({
        name: newName.trim(),
        description: newDesc.trim() || undefined,
        clone_from_id: cloneFrom || undefined,
      });
      setNewName('');
      setNewDesc('');
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to create role');
    } finally {
      setCreating(false);
    }
  };

  const toggleCapability = async (role: RoleDetail, cap: string) => {
    if (role.is_system) return; // system roles are read-only defaults — clone to customize
    const has = role.capabilities.includes(cap);
    // Removing roles.manage is a big deal: everyone holding this role loses the
    // whole Permissions surface. Name the consequence before saving; the server
    // additionally refuses to let you strip it from your OWN role (U0.5).
    if (has && cap === 'roles.manage') {
      const holders = role.member_count === 1 ? '1 member' : `${role.member_count} members`;
      if (!confirm(`Remove "Manage roles & permissions" from ${role.name}? ${role.member_count > 0 ? `${holders} holding this role` : 'Members with this role'} will no longer be able to open the Permissions settings.`)) return;
    }
    const next = has ? role.capabilities.filter((c) => c !== cap) : [...role.capabilities, cap];
    setBusyId(role.id);
    setError('');
    try {
      await setRoleCapabilities(role.id, next);
      setRoles((rs) => rs.map((r) => (r.id === role.id ? { ...r, capabilities: next } : r)));
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to save capability');
    } finally {
      setBusyId('');
    }
  };

  const changeScope = async (role: RoleDetail, scope: DataScope) => {
    if (role.is_system) return;
    setBusyId(role.id);
    setError('');
    try {
      await updateRole(role.id, { data_scope: scope });
      setRoles((rs) => rs.map((r) => (r.id === role.id ? { ...r, data_scope: scope } : r)));
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to change scope');
    } finally {
      setBusyId('');
    }
  };

  // Delete: a role nobody holds is deleted directly; a role with members opens the
  // reassign picker (the backend also enforces this with a 409).
  const startDelete = (role: RoleDetail) => {
    if (role.member_count > 0) {
      setReassignTo('');
      setDelTarget(role);
      return;
    }
    if (!confirm(`Delete the "${role.name}" role? This cannot be undone.`)) return;
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
      await duplicateRole(dupTarget.id, { name: dupName.trim(), reassign_members: dupReassign });
      setDupTarget(null);
      await load();
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
          Create a role from a template (it copies that template's capabilities and object
          access, so a new role isn't invisibly locked out), then tune its capabilities and
          data scope. Object and field access are set in the <strong>Object Permissions</strong>{' '}
          grid below. The <strong>owner</strong> role always has full access.
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
                <option key={r.id} value={r.id}>{r.name}</option>
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
          <p className="text-xs text-amber-600">
            ⚠ A blank role starts with no access to any object — members will see nothing until
            you grant access in the Object Permissions grid.
          </p>
        )}
      </div>

      {/* Role cards */}
      <div className="space-y-3">
        {roles.map((role) => (
          <div key={role.id} className="border rounded-lg p-4">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2 flex-wrap">
                <span className="font-medium">{role.name}</span>
                {role.is_owner && <span title="God-mode — bypasses all checks">🔒</span>}
                <span className="text-xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground">
                  {role.is_system ? 'system' : 'custom'}
                </span>
                <span className="text-xs text-muted-foreground">
                  {role.member_count} member{role.member_count === 1 ? '' : 's'}
                </span>
                {!role.is_system && role.template_key && (
                  <span className="text-xs text-muted-foreground italic">from {role.template_key} template</span>
                )}
                {role.is_system && !role.is_owner && (
                  <span className="text-xs text-muted-foreground italic">read-only · duplicate to customize</span>
                )}
              </div>
              <div className="flex items-center gap-3">
                {/* Data scope */}
                <label className="flex items-center gap-1.5 text-xs">
                  <span className="text-muted-foreground">Row scope</span>
                  <select
                    value={role.data_scope}
                    disabled={role.is_system || role.is_owner || busyId === role.id}
                    onChange={(e) => changeScope(role, e.target.value as DataScope)}
                    className="border rounded-md px-1.5 py-1 text-xs bg-background disabled:opacity-60"
                  >
                    <option value="all">All records</option>
                    <option value="own">Own + shared</option>
                  </select>
                </label>
                {!role.is_owner && (
                  <button
                    onClick={() => openDuplicate(role)}
                    disabled={busyId === role.id}
                    className="text-xs text-blue-600 hover:underline disabled:opacity-50"
                  >
                    Duplicate
                  </button>
                )}
                {!role.is_system && (
                  <button
                    onClick={() => startDelete(role)}
                    disabled={busyId === role.id}
                    className="text-xs text-red-600 hover:underline disabled:opacity-50"
                  >
                    Delete
                  </button>
                )}
              </div>
            </div>

            {role.description && <p className="text-xs text-muted-foreground mt-1">{role.description}</p>}

            {/* Capabilities, grouped with ⚠ chips on sensitive ones */}
            <div className="mt-3 space-y-3">
              {grouped.map((section) => (
                <div key={section.group}>
                  {section.group && catalog.length > 0 && (
                    <div className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground mb-1">{section.group}</div>
                  )}
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-1.5">
                    {section.caps.map((cap) => {
                      const checked = role.is_owner || role.capabilities.includes(cap.code);
                      return (
                        <label key={cap.code} className="flex items-center gap-2 text-sm" title={cap.description}>
                          <input
                            type="checkbox"
                            checked={checked}
                            disabled={role.is_system || busyId === role.id}
                            onChange={() => toggleCapability(role, cap.code)}
                            className="h-4 w-4 cursor-pointer disabled:cursor-not-allowed"
                          />
                          <span className={role.is_system ? 'text-muted-foreground' : ''}>
                            {cap.label}
                            {cap.sensitive && <span title="Sensitive — high blast radius" className="ml-1 text-amber-600">⚠</span>}
                          </span>
                        </label>
                      );
                    })}
                  </div>
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>

      {/* Duplicate modal */}
      {dupTarget && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
          <div className="absolute inset-0 bg-black/50" onClick={() => setDupTarget(null)} />
          <div className="relative bg-card border rounded-2xl shadow-xl w-full max-w-sm p-5">
            <h3 className="text-base font-semibold mb-1">Duplicate “{dupTarget.name}”</h3>
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
            <h3 className="text-base font-semibold mb-1">Delete “{delTarget.name}”</h3>
            <p className="text-sm text-muted-foreground mb-3">
              {delTarget.member_count} member{delTarget.member_count === 1 ? '' : 's'} still {delTarget.member_count === 1 ? 'holds' : 'hold'} this role.
              Move {delTarget.member_count === 1 ? 'them' : 'them'} to another role, then it will be deleted.
            </p>
            <label className="block text-xs font-medium mb-1">Move members to</label>
            <select
              value={reassignTo}
              onChange={(e) => setReassignTo(e.target.value)}
              className="w-full border rounded-md px-2.5 py-1.5 text-sm bg-background mb-4"
            >
              <option value="">-- Select a role --</option>
              {roles.filter((r) => r.id !== delTarget.id && !r.is_owner).map((r) => (
                <option key={r.id} value={r.id}>{r.name}</option>
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
    </div>
  );
}
