import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { useSearchParams } from 'react-router-dom';
import { AlertTriangle, Lock } from 'lucide-react';
import {
  getPermissionGrid,
  setObjectPermission,
  type PermissionGrid,
  type PermissionCell,
  type PermissionAction,
  type PermRoleInfo,
} from '../../lib/api';
import { prettyRole } from '../../lib/roles';

const ACTIONS: { key: PermissionAction; label: string }[] = [
  { key: 'read', label: 'Read' },
  { key: 'create', label: 'Create' },
  { key: 'edit', label: 'Edit' },
  { key: 'delete', label: 'Delete' },
];

const EMPTY_CELL = { read: false, create: false, edit: false, delete: false };

// Dismissed zero-access warnings persist per browser as "role_id:object_slug"
// pairs, so an admin who deliberately keeps a role away from an object isn't
// nagged forever — while a NEW zero-access pair still resurfaces the banner.
const DISMISS_KEY = 'zeroAccessDismissed';

function readDismissed(): Set<string> {
  try {
    const raw = localStorage.getItem(DISMISS_KEY);
    const arr = raw ? JSON.parse(raw) : [];
    return new Set(Array.isArray(arr) ? arr.filter((x): x is string => typeof x === 'string') : []);
  } catch {
    return new Set();
  }
}

function writeDismissed(pairs: Set<string>) {
  try {
    localStorage.setItem(DISMISS_KEY, JSON.stringify([...pairs]));
  } catch {
    // localStorage unavailable (private mode, quota) — dismissal just won't persist.
  }
}

interface ZeroAccessItem {
  role: PermRoleInfo;
  objects: { slug: string; label: string }[];
}

// PermissionsManager is the admin role × object access grid (P5a). It configures
// the Object-Level Security that RecordService enforces on the uniform record API.
// A role is picked at the top; the table below toggles read/create/edit/delete
// per object for that role. The owner role bypasses OLS, so its row is shown
// locked-on. Absence of an explicit grant means no access (default-deny).
export default function PermissionsManager() {
  const [grid, setGrid] = useState<PermissionGrid | null>(null);
  const [selectedRoleId, setSelectedRoleId] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [savingKey, setSavingKey] = useState('');
  const [dismissed, setDismissed] = useState<Set<string>>(readDismissed);
  const [highlightSlug, setHighlightSlug] = useState('');
  const [searchParams, setSearchParams] = useSearchParams();
  const rowRefs = useRef(new Map<string, HTMLTableRowElement>());
  const highlightTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const load = useCallback(async () => {
    try {
      setLoading(true);
      const g = await getPermissionGrid();
      setGrid(g);
      setSelectedRoleId((cur) => {
        if (cur && g.roles.some((r) => r.id === cur)) return cur;
        // Default to the first editable (non-owner) role, else the first role.
        const editable = g.roles.find((r) => !r.is_owner) || g.roles[0];
        return editable ? editable.id : '';
      });
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load permissions');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  useEffect(() => () => {
    if (highlightTimer.current) clearTimeout(highlightTimer.current);
  }, []);

  // Deep link: ?role=<id> preselects a role tab (RoleDetailSection's access
  // links land here). Invalid or absent ids leave the default selection alone.
  useEffect(() => {
    const param = searchParams.get('role');
    if (param && grid?.roles.some((r) => r.id === param)) {
      setSelectedRoleId(param);
    }
  }, [searchParams, grid]);

  const selectRole = (id: string) => {
    setSelectedRoleId(id);
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      next.set('role', id);
      return next;
    }, { replace: true });
  };

  // (role_id, slug) → cell, for O(1) lookups while rendering the table.
  const cellMap = useMemo(() => {
    const m = new Map<string, PermissionCell>();
    grid?.matrix.forEach((c) => m.set(`${c.role_id}:${c.object_slug}`, c));
    return m;
  }, [grid]);

  const selectedRole: PermRoleInfo | undefined = grid?.roles.find((r) => r.id === selectedRoleId);

  // Zero-access pairs: a non-owner role × object where the cell lacks Read (an
  // absent row and an all-false row both count — default-deny). Grouped by role
  // so the banner says exactly WHO can't see WHAT, minus dismissed pairs.
  // Owner is excluded (it bypasses OLS).
  const zeroAccess = useMemo<ZeroAccessItem[]>(() => {
    if (!grid) return [];
    return grid.roles
      .filter((r) => !r.is_owner)
      .map((role) => ({
        role,
        objects: grid.objects
          .filter((o) => !cellMap.get(`${role.id}:${o.slug}`)?.read)
          .filter((o) => !dismissed.has(`${role.id}:${o.slug}`))
          .map((o) => ({ slug: o.slug, label: o.label })),
      }))
      .filter((item) => item.objects.length > 0);
  }, [grid, cellMap, dismissed]);

  const dismissItem = (item: ZeroAccessItem) => {
    setDismissed((prev) => {
      const next = new Set(prev);
      item.objects.forEach((o) => next.add(`${item.role.id}:${o.slug}`));
      writeDismissed(next);
      return next;
    });
  };

  // Review: jump to the role's tab and point at the first offending object row
  // with a short-lived highlight.
  const reviewItem = (item: ZeroAccessItem) => {
    selectRole(item.role.id);
    const slug = item.objects[0]?.slug;
    if (!slug) return;
    setHighlightSlug(slug);
    requestAnimationFrame(() => {
      rowRefs.current.get(slug)?.scrollIntoView?.({ behavior: 'smooth', block: 'center' });
    });
    if (highlightTimer.current) clearTimeout(highlightTimer.current);
    highlightTimer.current = setTimeout(() => setHighlightSlug(''), 2000);
  };

  const cellFor = (roleId: string, slug: string) => {
    if (selectedRole?.is_owner) return { read: true, create: true, edit: true, delete: true };
    const c = cellMap.get(`${roleId}:${slug}`);
    return c ? { read: c.read, create: c.create, edit: c.edit, delete: c.delete } : { ...EMPTY_CELL };
  };

  const toggle = async (slug: string, action: PermissionAction) => {
    if (!selectedRole || selectedRole.is_owner) return; // owner bypasses OLS — not editable
    const current = cellFor(selectedRole.id, slug);
    const next = { ...current, [action]: !current[action] };
    // Read is implied (U0.9): you can't create/edit/delete records you can't
    // see, so granting any of those grants Read, and revoking Read revokes the
    // rest — the grid can no longer express an incoherent combination.
    if (action !== 'read' && next[action]) {
      next.read = true;
    }
    if (action === 'read' && !next.read) {
      next.create = false;
      next.edit = false;
      next.delete = false;
    }
    const key = `${slug}:${action}`;
    setSavingKey(key);
    setError('');
    try {
      await setObjectPermission({
        role_id: selectedRole.id,
        object_slug: slug,
        can_read: next.read,
        can_create: next.create,
        can_edit: next.edit,
        can_delete: next.delete,
      });
      // Update the matrix locally so the toggle is reflected without a refetch.
      setGrid((g) => {
        if (!g) return g;
        const rest = g.matrix.filter((c) => !(c.role_id === selectedRole.id && c.object_slug === slug));
        return { ...g, matrix: [...rest, { role_id: selectedRole.id, object_slug: slug, ...next }] };
      });
      // Granting Read resolves the zero-access pair for good — prune a stale
      // dismissal so a FUTURE revoke surfaces the warning again.
      if (next.read) {
        setDismissed((prev) => {
          const pairKey = `${selectedRole.id}:${slug}`;
          if (!prev.has(pairKey)) return prev;
          const pruned = new Set(prev);
          pruned.delete(pairKey);
          writeDismissed(pruned);
          return pruned;
        });
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to save permission');
    } finally {
      setSavingKey('');
    }
  };

  if (loading) return <div className="text-sm text-muted-foreground py-8">Loading permissions…</div>;
  if (!grid) return <div className="text-sm text-red-600 py-8">{error || 'No permission data.'}</div>;

  return (
    <div className="space-y-4">
      <div>
        <h3 className="text-lg font-semibold">Object Permissions</h3>
        <p className="text-sm text-muted-foreground mt-1">
          Control what each role can do with each object. Changes apply immediately.
          A role with no grant has no access; the <strong>owner</strong> role always has full access.
          Granting Create, Edit, or Delete also grants Read — you can't work with records you can't see.
        </p>
      </div>

      {error && (
        <div className="bg-red-50 text-red-700 text-sm rounded-md px-3 py-2">{error}</div>
      )}

      {zeroAccess.length > 0 && (
        <div className="bg-amber-50 text-amber-800 text-sm rounded-md px-3 py-2 border border-amber-200 space-y-2">
          {zeroAccess.map((item) => {
            const roleName = prettyRole(item.role.name);
            return (
              <div key={item.role.id} className="flex items-start gap-2">
                <AlertTriangle className="h-4 w-4 mt-0.5 shrink-0" aria-hidden="true" />
                <span className="flex-1">
                  <strong>{roleName}</strong> has no access to:{' '}
                  {item.objects.map((o) => o.label).join(', ')} — members with this role
                  won't see {item.objects.length === 1 ? 'it' : 'them'} anywhere.
                </span>
                <button
                  onClick={() => reviewItem(item)}
                  aria-label={`Review ${roleName} access`}
                  className="shrink-0 px-2 py-0.5 text-xs font-medium rounded border border-amber-300 hover:bg-amber-100 transition-colors"
                >
                  Review
                </button>
                <button
                  onClick={() => dismissItem(item)}
                  aria-label={`Dismiss ${roleName} access warning`}
                  className="shrink-0 px-2 py-0.5 text-xs font-medium rounded border border-transparent hover:bg-amber-100 transition-colors"
                >
                  Dismiss
                </button>
              </div>
            );
          })}
        </div>
      )}

      {/* Role selector */}
      <div className="flex flex-wrap gap-1.5" role="tablist" aria-label="Roles">
        {grid.roles.map((r) => (
          <button
            key={r.id}
            role="tab"
            aria-selected={r.id === selectedRoleId}
            onClick={() => selectRole(r.id)}
            className={`inline-flex items-center gap-1 px-3 py-1.5 text-sm rounded-md border transition-colors ${
              r.id === selectedRoleId
                ? 'bg-blue-500 text-white border-blue-500'
                : 'bg-background border-muted-foreground/20 hover:border-muted-foreground/40'
            }`}
          >
            {prettyRole(r.name)}
            {r.is_owner && <Lock className="h-3.5 w-3.5" aria-hidden="true" />}
          </button>
        ))}
      </div>

      {/* Access matrix for the selected role */}
      <div className="border rounded-lg overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-muted/40">
            <tr>
              <th className="text-left font-medium px-3 py-2">Object</th>
              {ACTIONS.map((a) => (
                <th key={a.key} className="font-medium px-3 py-2 text-center w-20">{a.label}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {grid.objects.map((o) => {
              const cell = cellFor(selectedRoleId, o.slug);
              return (
                <tr
                  key={o.slug}
                  ref={(el) => {
                    if (el) rowRefs.current.set(o.slug, el);
                    else rowRefs.current.delete(o.slug);
                  }}
                  className={`border-t transition-colors ${
                    highlightSlug === o.slug ? 'bg-amber-100 dark:bg-amber-900/40' : ''
                  }`}
                >
                  <td className="px-3 py-2">
                    <span className="mr-1.5">{o.icon}</span>{o.label}
                    {!o.is_system && <span className="ml-1.5 text-xs text-muted-foreground">(custom)</span>}
                  </td>
                  {ACTIONS.map((a) => {
                    const checkboxKey = `${o.slug}:${a.key}`;
                    return (
                      <td key={a.key} className="px-3 py-2 text-center">
                        <input
                          type="checkbox"
                          checked={cell[a.key]}
                          disabled={selectedRole?.is_owner || savingKey === checkboxKey}
                          aria-label={`${prettyRole(selectedRole?.name)} ${a.label} ${o.label}`}
                          onChange={() => toggle(o.slug, a.key)}
                          className="h-4 w-4 cursor-pointer disabled:cursor-not-allowed"
                        />
                      </td>
                    );
                  })}
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      {selectedRole?.is_owner && (
        <p className="text-xs text-muted-foreground">
          The owner role bypasses object permissions and always has full access.
        </p>
      )}
    </div>
  );
}
