// React Query data layer for the admin settings screens (U7.3). Before this, every
// settings screen hand-rolled useState + useEffect + fetch, patched its local copy
// after a save and never re-read. Two admins on the same permission grid therefore
// overwrote each other silently: neither saw the other's change, and the next save
// clobbered it. Centralizing the keys here means a mutation in ONE screen
// invalidates the caches every OTHER screen reads from — an object grant saved in
// PermissionsManager refreshes the role detail page's effective-access table, a
// rename in RolesManager shows up on the role's own page, and so on.
//
// Mirrors src/features/workflows/queries.ts (the exported-keys + hooks shape).

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  getPermissionGrid,
  setObjectPermission,
  getFieldPermissionGrid,
  getFieldPermissionSummary,
  setFieldPermission,
  bulkSetFieldPermissions,
  getRoles,
  getRolesCatalog,
  getRoleAccess,
  createRole,
  duplicateRole,
  updateRole,
  deleteRole,
  setRoleCapabilities,
  listGroups,
  createGroup,
  updateGroup,
  deleteGroup,
  addGroupMember,
  removeGroupMember,
  getWorkspaceMembers,
  getCurrentWorkspace,
  updateWorkspace,
  leaveWorkspace,
  deleteWorkspace,
  type PermissionGrid,
  type FieldPermissionGrid,
  type FieldLevel,
  type RoleDetail,
  type RoleAccess,
  type CapabilityInfo,
  type DataScope,
  type UserGroup,
  type WorkspaceMember,
  type WorkspaceDetail,
  type UpdateWorkspaceInput,
} from '../../lib/api';

// Single source of truth for the settings cache keys, so producers (mutations) and
// consumers (queries) can't drift. The plural helpers (`roleAccesses`,
// `fieldSecurities`) are invalidation umbrellas over every per-id/per-slug variant.
export const settingsKeys = {
  all: ['settings'] as const,

  // Roles
  roles: () => [...settingsKeys.all, 'roles'] as const,
  rolesCatalog: () => [...settingsKeys.all, 'roles-catalog'] as const,
  roleAccesses: () => [...settingsKeys.all, 'role-access'] as const,
  roleAccess: (id: string) => [...settingsKeys.roleAccesses(), id] as const,

  // Object-level security (the role × object grid)
  permissionGrid: () => [...settingsKeys.all, 'permission-grid'] as const,

  // Field-level security (per object: field × role)
  fieldSecurities: () => [...settingsKeys.all, 'field-security'] as const,
  fieldSecurity: (slug: string) => [...settingsKeys.fieldSecurities(), slug] as const,
  fieldSecuritySummary: () => [...settingsKeys.all, 'field-security-summary'] as const,

  // User groups (teams) + the member list they're built from
  groups: () => [...settingsKeys.all, 'groups'] as const,
  members: () => [...settingsKeys.all, 'members'] as const,

  // Workspace general
  workspace: () => [...settingsKeys.all, 'workspace'] as const,
};

// One bulk FLS call is capped server-side at 200 field_keys (maxBulkFieldKeys);
// wider grids are applied in sequential chunks of this size.
export const BULK_CHUNK_SIZE = 200;

type SetObjectPermissionInput = Parameters<typeof setObjectPermission>[0];
type SetFieldPermissionInput = Parameters<typeof setFieldPermission>[0];

// ── Object-level security (P5a) ───────────────────────────────────────────────

/** The role × object access grid. Also the org's object list for any settings
 *  screen that needs one (FieldSecurityManager reuses it). Refetched on mount so
 *  returning to the tab shows another admin's changes. */
export function usePermissionGrid() {
  return useQuery<PermissionGrid>({
    queryKey: settingsKeys.permissionGrid(),
    queryFn: () => getPermissionGrid(),
    refetchOnMount: 'always',
  });
}

/** Save one (role, object) cell. Primes the grid cache with the saved cell so the
 *  checkbox doesn't flicker while the re-read is in flight, then invalidates — the
 *  re-read is the point: it surfaces any OTHER cell a concurrent admin changed. */
export function useSetObjectPermission() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: SetObjectPermissionInput) => setObjectPermission(input),
    onSuccess: (_res, input) => {
      qc.setQueryData<PermissionGrid>(settingsKeys.permissionGrid(), (g) =>
        g
          ? {
              ...g,
              matrix: [
                ...g.matrix.filter((c) => !(c.role_id === input.role_id && c.object_slug === input.object_slug)),
                {
                  role_id: input.role_id,
                  object_slug: input.object_slug,
                  read: input.can_read,
                  create: input.can_create,
                  edit: input.can_edit,
                  delete: input.can_delete,
                },
              ],
            }
          : g,
      );
      qc.invalidateQueries({ queryKey: settingsKeys.permissionGrid() });
      // The OLS bits are also the role detail page's effective-access table.
      qc.invalidateQueries({ queryKey: settingsKeys.roleAccesses() });
    },
  });
}

// ── Field-level security (P5b) ────────────────────────────────────────────────

/** One object's field × role level grid. Disabled until an object is selected. */
export function useFieldPermissionGrid(slug: string) {
  return useQuery<FieldPermissionGrid>({
    queryKey: settingsKeys.fieldSecurity(slug),
    queryFn: () => getFieldPermissionGrid(slug),
    enabled: Boolean(slug),
    refetchOnMount: 'always',
  });
}

/** Restriction counts per object slug, for the badges on the object pills. A
 *  progressive enhancement: a failure leaves the grid fully usable, so callers
 *  fall back to {} rather than surfacing it in the error banner. */
export function useFieldPermissionSummary() {
  return useQuery<Record<string, number>>({
    queryKey: settingsKeys.fieldSecuritySummary(),
    queryFn: () => getFieldPermissionSummary(),
  });
}

// Invalidate everything a field-level change is visible in: the object's own grid,
// the badge summary, and the role detail page's field-limits column.
function invalidateFieldSecurity(qc: ReturnType<typeof useQueryClient>, slug: string) {
  qc.invalidateQueries({ queryKey: settingsKeys.fieldSecurity(slug) });
  qc.invalidateQueries({ queryKey: settingsKeys.fieldSecuritySummary() });
  qc.invalidateQueries({ queryKey: settingsKeys.roleAccesses() });
}

/** Save one (role, field) level. */
export function useSetFieldPermission() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: SetFieldPermissionInput) => setFieldPermission(input),
    onSuccess: (_res, input) => invalidateFieldSecurity(qc, input.object_slug),
  });
}

/** Bulk "set column": one apply covering the given field keys, sent in sequential
 *  <=200-key chunks (the server cap); each chunk is one transaction/audit event.
 *  A mid-chunk failure leaves the column half-applied server-side, so the grid is
 *  invalidated on SETTLE (not just success) — the UI then shows what actually
 *  landed while the caller surfaces the error. */
export function useBulkSetFieldPermissions() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (vars: { object_slug: string; role_id: string; field_keys: string[]; level: FieldLevel }) => {
      for (let i = 0; i < vars.field_keys.length; i += BULK_CHUNK_SIZE) {
        await bulkSetFieldPermissions({
          object_slug: vars.object_slug,
          role_id: vars.role_id,
          field_keys: vars.field_keys.slice(i, i + BULK_CHUNK_SIZE),
          level: vars.level,
        });
      }
    },
    onSettled: (_res, _err, vars) => invalidateFieldSecurity(qc, vars.object_slug),
  });
}

// ── Roles ─────────────────────────────────────────────────────────────────────

/** Every role with its member count + capabilities (roles.manage gated). Shared by
 *  the roles list and the role detail page, so a rename in one shows in the other. */
export function useRoles() {
  return useQuery<RoleDetail[]>({
    queryKey: settingsKeys.roles(),
    queryFn: () => getRoles(),
    refetchOnMount: 'always',
  });
}

/** Capability metadata (labels/descriptions/groups/sensitive flags) + group order.
 *  Org-level config that rarely changes; a failure is non-fatal (the callers fall
 *  back to the built-in ALL_CAPABILITIES list), so it resolves to an empty catalog
 *  rather than erroring the screen. */
export function useRolesCatalog() {
  return useQuery<{ capabilities: CapabilityInfo[]; groups: string[] }>({
    queryKey: settingsKeys.rolesCatalog(),
    queryFn: () => getRolesCatalog().catch(() => ({ capabilities: [], groups: [] })),
    staleTime: 5 * 60_000,
  });
}

/** One role's whole story: identity + capabilities + effective object/field access
 *  + layouts (U3.2). */
export function useRoleAccess(id: string) {
  return useQuery<RoleAccess>({
    queryKey: settingsKeys.roleAccess(id),
    queryFn: () => getRoleAccess(id),
    enabled: Boolean(id),
    refetchOnMount: 'always',
  });
}

export function useCreateRole() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: { name: string; description?: string; clone_from_id?: string }) => createRole(input),
    onSuccess: () => qc.invalidateQueries({ queryKey: settingsKeys.roles() }),
  });
}

export function useDuplicateRole() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { role: RoleDetail; name: string; reassign_members: boolean }) =>
      duplicateRole(vars.role.id, { name: vars.name, reassign_members: vars.reassign_members }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: settingsKeys.roles() });
      // reassign_members moves people off the source role: its member count and the
      // copy's both changed.
      qc.invalidateQueries({ queryKey: settingsKeys.roleAccesses() });
    },
  });
}

export function useDeleteRole() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { role: RoleDetail; reassignTo?: string }) => deleteRole(vars.role.id, vars.reassignTo),
    onSuccess: (_res, vars) => {
      qc.removeQueries({ queryKey: settingsKeys.roleAccess(vars.role.id) });
      qc.invalidateQueries({ queryKey: settingsKeys.roles() });
      // Reassigned members land on another role, changing ITS member count.
      qc.invalidateQueries({ queryKey: settingsKeys.roleAccesses() });
      // A deleted role disappears from the OLS grid's role tabs.
      qc.invalidateQueries({ queryKey: settingsKeys.permissionGrid() });
    },
  });
}

/** Rename/describe a role or change its data scope. Primes the role's own cache so
 *  the field the admin just changed doesn't flicker back, then re-reads both it and
 *  the roles list. */
export function useUpdateRole() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { id: string; patch: { name?: string; description?: string; data_scope?: DataScope } }) =>
      updateRole(vars.id, vars.patch),
    onSuccess: (_res, vars) => {
      qc.setQueryData<RoleAccess>(settingsKeys.roleAccess(vars.id), (d) =>
        d ? { ...d, role: { ...d.role, ...vars.patch } } : d,
      );
      qc.invalidateQueries({ queryKey: settingsKeys.roleAccess(vars.id) });
      qc.invalidateQueries({ queryKey: settingsKeys.roles() });
    },
  });
}

/** Replace a role's capability set. */
export function useSetRoleCapabilities() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { id: string; capabilities: string[] }) => setRoleCapabilities(vars.id, vars.capabilities),
    onSuccess: (_res, vars) => {
      qc.setQueryData<RoleAccess>(settingsKeys.roleAccess(vars.id), (d) =>
        d ? { ...d, role: { ...d.role, capabilities: vars.capabilities } } : d,
      );
      qc.invalidateQueries({ queryKey: settingsKeys.roleAccess(vars.id) });
      qc.invalidateQueries({ queryKey: settingsKeys.roles() }); // the list shows "N of M permissions"
    },
  });
}

// ── User groups (teams) ───────────────────────────────────────────────────────

export function useGroups() {
  return useQuery<UserGroup[]>({
    queryKey: settingsKeys.groups(),
    queryFn: () => listGroups(),
    refetchOnMount: 'always',
  });
}

/** The workspace's members — the pool a group picks from. */
export function useWorkspaceMembers() {
  return useQuery<WorkspaceMember[]>({
    queryKey: settingsKeys.members(),
    queryFn: () => getWorkspaceMembers(),
  });
}

export function useCreateGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => createGroup(name),
    onSuccess: () => qc.invalidateQueries({ queryKey: settingsKeys.groups() }),
  });
}

export function useUpdateGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { id: string; name: string; description: string }) =>
      updateGroup(vars.id, vars.name, vars.description),
    onSuccess: () => qc.invalidateQueries({ queryKey: settingsKeys.groups() }),
  });
}

export function useDeleteGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteGroup(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: settingsKeys.groups() }),
  });
}

/** Add/remove one member. A group IS a team, so membership changes what a
 *  team-scoped role can see — but that's enforced server-side per request; only the
 *  group list needs re-reading here. */
export function useToggleGroupMember() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { groupId: string; userId: string; isMember: boolean }) =>
      vars.isMember ? removeGroupMember(vars.groupId, vars.userId) : addGroupMember(vars.groupId, vars.userId),
    onSuccess: () => qc.invalidateQueries({ queryKey: settingsKeys.groups() }),
  });
}

// ── Workspace general ─────────────────────────────────────────────────────────

export function useWorkspace() {
  return useQuery<WorkspaceDetail>({
    queryKey: settingsKeys.workspace(),
    queryFn: () => getCurrentWorkspace(),
    refetchOnMount: 'always',
  });
}

/** Save the workspace's general settings. Primes the cache with what was saved (so
 *  the form doesn't flash the old values while the re-read is in flight), then
 *  invalidates — the re-read is what surfaces a concurrent admin's change. */
export function useUpdateWorkspace() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (patch: UpdateWorkspaceInput) => updateWorkspace(patch),
    onSuccess: (_res, patch) => {
      qc.setQueryData<WorkspaceDetail>(settingsKeys.workspace(), (w) => (w ? { ...w, ...patch } : w));
      qc.invalidateQueries({ queryKey: settingsKeys.workspace() });
    },
  });
}

// Leaving or deleting the workspace changes which org the session is in — every
// cached settings query belongs to the OLD org, so drop them all rather than let a
// stale grid render against the new one.
function useWorkspaceExit(fn: () => Promise<void>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => qc.removeQueries({ queryKey: settingsKeys.all }),
  });
}

export function useLeaveWorkspace() {
  return useWorkspaceExit(() => leaveWorkspace());
}

export function useDeleteWorkspace() {
  return useWorkspaceExit(() => deleteWorkspace());
}
