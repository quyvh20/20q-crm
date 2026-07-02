import { useState, useEffect, useCallback } from 'react';
import {
  getRoles,
  createRole,
  deleteRole,
  updateRole,
  setRoleCapabilities,
  ALL_CAPABILITIES,
  CAPABILITY_LABELS,
  type RoleDetail,
  type DataScope,
} from '../../lib/api';

// RolesManager is the admin surface for custom roles (P3). Every authorization
// layer keys off role_id, so a role created here works everywhere: its
// capabilities gate admin actions, its OLS/FLS grids (Permissions tab) gate data,
// and its data_scope controls row visibility. Clone a system role to start, then
// tune capabilities and scope. The owner role is locked (god-mode).
export default function RolesManager() {
  const [roles, setRoles] = useState<RoleDetail[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [busyId, setBusyId] = useState('');

  // New-role form state.
  const [newName, setNewName] = useState('');
  const [cloneFrom, setCloneFrom] = useState('');
  const [creating, setCreating] = useState(false);

  const load = useCallback(async () => {
    try {
      setLoading(true);
      setRoles(await getRoles());
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
      await createRole({
        name: newName.trim(),
        clone_from_id: cloneFrom || undefined,
      });
      setNewName('');
      setCloneFrom('');
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

  const handleDelete = async (role: RoleDetail) => {
    if (!confirm(`Delete the "${role.name}" role? This cannot be undone.`)) return;
    setBusyId(role.id);
    setError('');
    try {
      await deleteRole(role.id);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to delete role');
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
          Create custom roles by cloning a system role, then tune their capabilities and data scope.
          Object and field access are set in the <strong>Object Permissions</strong> grid below. The{' '}
          <strong>owner</strong> role always has full access.
        </p>
      </div>

      {error && <div className="bg-red-50 text-red-700 text-sm rounded-md px-3 py-2">{error}</div>}

      {/* Create / clone a role */}
      <div className="flex flex-wrap items-end gap-2 border rounded-lg p-3 bg-muted/20">
        <div className="flex flex-col">
          <label className="text-xs font-medium mb-1">New role name</label>
          <input
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            placeholder="e.g. Support Agent"
            className="border rounded-md px-2.5 py-1.5 text-sm bg-background w-52"
          />
        </div>
        <div className="flex flex-col">
          <label className="text-xs font-medium mb-1">Clone from</label>
          <select
            value={cloneFrom}
            onChange={(e) => setCloneFrom(e.target.value)}
            className="border rounded-md px-2.5 py-1.5 text-sm bg-background w-44"
          >
            <option value="">Blank (default-deny)</option>
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

      {/* Role cards */}
      <div className="space-y-3">
        {roles.map((role) => (
          <div key={role.id} className="border rounded-lg p-4">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <span className="font-medium">{role.name}</span>
                {role.is_owner && <span title="God-mode — bypasses all checks">🔒</span>}
                <span className="text-xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground">
                  {role.is_system ? 'system' : 'custom'}
                </span>
                <span className="text-xs text-muted-foreground">
                  {role.member_count} member{role.member_count === 1 ? '' : 's'}
                </span>
                {role.is_system && !role.is_owner && (
                  <span className="text-xs text-muted-foreground italic">read-only · clone to customize</span>
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
                {!role.is_system && (
                  <button
                    onClick={() => handleDelete(role)}
                    disabled={busyId === role.id}
                    className="text-xs text-red-600 hover:underline disabled:opacity-50"
                  >
                    Delete
                  </button>
                )}
              </div>
            </div>

            {/* Capabilities */}
            <div className="mt-3 grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-1.5">
              {ALL_CAPABILITIES.map((cap) => {
                const checked = role.is_owner || role.capabilities.includes(cap);
                return (
                  <label key={cap} className="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={checked}
                      disabled={role.is_system || busyId === role.id}
                      onChange={() => toggleCapability(role, cap)}
                      className="h-4 w-4 cursor-pointer disabled:cursor-not-allowed"
                    />
                    <span className={role.is_system ? 'text-muted-foreground' : ''}>
                      {CAPABILITY_LABELS[cap] || cap}
                    </span>
                  </label>
                );
              })}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
