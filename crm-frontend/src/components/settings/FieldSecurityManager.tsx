import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Lock } from 'lucide-react';
import {
  getPermissionGrid,
  getFieldPermissionGrid,
  getFieldPermissionSummary,
  setFieldPermission,
  bulkSetFieldPermissions,
  type PermObjectInfo,
  type PermRoleInfo,
  type FieldPermissionGrid,
  type FieldLevel,
} from '../../lib/api';
import { prettyRole } from '../../lib/roles';
import { useConfirm } from '../common/ConfirmDialog';

const LEVELS: { value: FieldLevel; label: string }[] = [
  { value: 'edit', label: 'Edit' },
  { value: 'read', label: 'Read' },
  { value: 'hidden', label: 'Hidden' },
];

const levelLabel = (level: FieldLevel) => LEVELS.find((l) => l.value === level)?.label ?? level;

// One bulk FLS call is capped server-side at 200 field_keys (maxBulkFieldKeys);
// wider grids are applied in sequential chunks of this size.
const BULK_CHUNK_SIZE = 200;

// What a bulk change means for the affected members, phrased per level so the
// confirm dialog states the consequence, not just the mechanics.
const BULK_CONSEQUENCE: Record<FieldLevel, string> = {
  edit: 'Members with this role will regain full access to these fields.',
  read: 'Members with this role will be able to view but not edit these fields.',
  hidden: 'Members with this role will no longer see these fields.',
};

// FieldSecurityManager is the admin per-object field × role visibility grid (P5b).
// It configures the Field-Level Security that RecordService enforces server-side:
// a 'hidden' field is stripped from the API response (not just the UI) and a
// 'read'/'hidden' field rejects writes. FLS is opt-in — every field defaults to
// full Edit access, so this screen only matters once a field is restricted. The
// owner role bypasses FLS, so its column is a static "Full access" cell.
export default function FieldSecurityManager() {
  const [objects, setObjects] = useState<PermObjectInfo[]>([]);
  const [selectedSlug, setSelectedSlug] = useState('');
  const [fieldGrid, setFieldGrid] = useState<FieldPermissionGrid | null>(null);
  const [loadingObjects, setLoadingObjects] = useState(true);
  const [loadingGrid, setLoadingGrid] = useState(false);
  const [error, setError] = useState('');
  const [savingKey, setSavingKey] = useState('');
  const [search, setSearch] = useState('');
  const [restrictedOnly, setRestrictedOnly] = useState(false);
  // Restriction counts per object slug, for the badges on the object pills.
  // Seeded from the summary endpoint; the currently loaded object's count is
  // then kept live from the local matrix (see the fieldGrid effect below).
  const [counts, setCounts] = useState<Record<string, number>>({});
  // Role id a bulk apply is in flight for ('' when idle).
  const [bulkRoleId, setBulkRoleId] = useState('');

  const [searchParams, setSearchParams] = useSearchParams();
  // ?object= deep link — captured once at mount; afterwards the pills drive the
  // param, not the other way around.
  const initialObjectParam = useRef(searchParams.get('object'));
  // ?role= deep link (a role detail page's "Edit field access"): emphasize that
  // role's column so the admin lands looking at the right one. Display only —
  // every column stays editable.
  const roleParam = searchParams.get('role');

  const { confirm, dialog } = useConfirm();

  // Load the object list (reusing the OLS grid, which already returns the org's
  // objects for an admin) and the restriction-count summary once.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const g = await getPermissionGrid();
        if (cancelled) return;
        setObjects(g.objects);
        setSelectedSlug((cur) => {
          if (cur) return cur;
          const want = initialObjectParam.current;
          if (want && g.objects.some((o) => o.slug === want)) return want;
          return g.objects[0]?.slug ?? '';
        });
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : 'Failed to load objects');
      } finally {
        if (!cancelled) setLoadingObjects(false);
      }
    })();
    // Badges are a progressive enhancement — if the summary fails the grid is
    // still fully usable, so don't surface it in the shared error banner.
    getFieldPermissionSummary()
      .then((c) => {
        // Locally derived counts (from a loaded grid) are fresher than the
        // summary snapshot, so they win on merge.
        if (!cancelled) setCounts((prev) => ({ ...c, ...prev }));
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);

  const loadGrid = useCallback(async (slug: string) => {
    if (!slug) return;
    setLoadingGrid(true);
    setError('');
    try {
      setFieldGrid(await getFieldPermissionGrid(slug));
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load field permissions');
      setFieldGrid(null);
    } finally {
      setLoadingGrid(false);
    }
  }, []);

  useEffect(() => {
    setSearch(''); // a field search rarely applies across objects
    loadGrid(selectedSlug);
  }, [selectedSlug, loadGrid]);

  // Keep the current object's badge count in sync with the local matrix after
  // any single-cell or bulk save (fieldGrid is re-created on each patch) —
  // no summary refetch needed. Owner cells (if any ever appear) don't count.
  useEffect(() => {
    if (!fieldGrid) return;
    const ownerIds = new Set(fieldGrid.roles.filter((r) => r.is_owner).map((r) => r.id));
    const n = fieldGrid.matrix.filter((c) => !ownerIds.has(c.role_id)).length;
    setCounts((prev) => (prev[fieldGrid.slug] === n ? prev : { ...prev, [fieldGrid.slug]: n }));
  }, [fieldGrid]);

  const selectObject = (slug: string) => {
    setSelectedSlug(slug);
    setSearchParams(
      (prev) => {
        const p = new URLSearchParams(prev);
        p.set('object', slug);
        return p;
      },
      { replace: true },
    );
  };

  // (role_id, field_key) → level, for O(1) lookups. Only non-default cells exist.
  const cellMap = useMemo(() => {
    const m = new Map<string, FieldLevel>();
    fieldGrid?.matrix.forEach((c) => m.set(`${c.role_id}:${c.field_key}`, c.level));
    return m;
  }, [fieldGrid]);

  // Only highlight when ?role= names a role actually present in the grid.
  const highlightRoleId = useMemo(
    () => (roleParam && fieldGrid?.roles.some((r) => r.id === roleParam) ? roleParam : ''),
    [roleParam, fieldGrid],
  );

  const levelFor = (role: PermRoleInfo, fieldKey: string): FieldLevel => {
    if (role.is_owner) return 'edit'; // owner bypasses FLS
    return cellMap.get(`${role.id}:${fieldKey}`) ?? 'edit';
  };

  // Field keys that carry ANY non-default cell — the "Restricted only" filter.
  const restrictedKeys = useMemo(() => {
    const s = new Set<string>();
    fieldGrid?.matrix.forEach((c) => s.add(c.field_key));
    return s;
  }, [fieldGrid]);

  const query = search.trim().toLowerCase();
  const filtering = query !== '' || restrictedOnly;
  const visibleFields = useMemo(() => {
    if (!fieldGrid) return [];
    return fieldGrid.fields.filter((f) => {
      if (restrictedOnly && !restrictedKeys.has(f.key)) return false;
      if (query && !f.label.toLowerCase().includes(query) && !f.key.toLowerCase().includes(query)) return false;
      return true;
    });
  }, [fieldGrid, query, restrictedOnly, restrictedKeys]);

  const change = async (role: PermRoleInfo, fieldKey: string, level: FieldLevel) => {
    if (role.is_owner || !fieldGrid) return;
    const key = `${role.id}:${fieldKey}`;
    setSavingKey(key);
    setError('');
    try {
      await setFieldPermission({ object_slug: fieldGrid.slug, role_id: role.id, field_key: fieldKey, level });
      // Reflect locally without a refetch. 'edit' is the default, stored as the
      // absence of a cell — mirroring the backend, which deletes the row.
      setFieldGrid((g) => {
        if (!g) return g;
        const rest = g.matrix.filter((c) => !(c.role_id === role.id && c.field_key === fieldKey));
        const matrix = level === 'edit' ? rest : [...rest, { role_id: role.id, field_key: fieldKey, level }];
        return { ...g, matrix };
      });
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to save field permission');
    } finally {
      setSavingKey('');
    }
  };

  // Bulk "set column": one confirmed apply covering the currently VISIBLE
  // (filtered) fields, sent in sequential <=200-key chunks (the server cap);
  // each chunk is one transaction/audit event server-side.
  const bulkApply = async (role: PermRoleInfo, level: FieldLevel) => {
    if (role.is_owner || !fieldGrid || visibleFields.length === 0) return;
    const keys = visibleFields.map((f) => f.key);
    const count = keys.length;
    const ok = await confirm({
      title: 'Set field access',
      body: `Set ${count} field${count === 1 ? '' : 's'} to ${levelLabel(level)} for ${prettyRole(role.name)}? ${BULK_CONSEQUENCE[level]}`,
      confirmLabel: `Set to ${levelLabel(level)}`,
      tone: level === 'hidden' ? 'danger' : 'default',
    });
    if (!ok) return;
    setBulkRoleId(role.id);
    setError('');
    try {
      for (let i = 0; i < keys.length; i += BULK_CHUNK_SIZE) {
        await bulkSetFieldPermissions({
          object_slug: fieldGrid.slug,
          role_id: role.id,
          field_keys: keys.slice(i, i + BULK_CHUNK_SIZE),
          level,
        });
      }
      setFieldGrid((g) => {
        if (!g) return g;
        const keySet = new Set(keys);
        const rest = g.matrix.filter((c) => !(c.role_id === role.id && keySet.has(c.field_key)));
        const matrix =
          level === 'edit' ? rest : [...rest, ...keys.map((k) => ({ role_id: role.id, field_key: k, level }))];
        return { ...g, matrix };
      });
    } catch (e) {
      // A mid-chunk failure leaves the column half-applied server-side: reload
      // the grid so the UI shows what actually landed, THEN surface the error
      // (loadGrid clears the banner on its way in).
      await loadGrid(fieldGrid.slug);
      setError(e instanceof Error ? e.message : 'Failed to save field permissions');
    } finally {
      setBulkRoleId('');
    }
  };

  if (loadingObjects) return <div className="text-sm text-muted-foreground py-8">Loading field security…</div>;

  return (
    <div className="space-y-4">
      <div>
        <h3 className="text-lg font-semibold">Field Security</h3>
        <p className="text-sm text-muted-foreground mt-1">
          Restrict who can see or edit individual fields. Fields default to full access; set a
          role to <strong>Read</strong> (view-only) or <strong>Hidden</strong> (removed from the
          API response, not just the screen) to protect sensitive data. The <strong>owner</strong>{' '}
          role always has full access. Changes apply immediately.
        </p>
      </div>

      {error && <div className="bg-red-50 text-red-700 text-sm rounded-md px-3 py-2">{error}</div>}

      {/* Object selector, with a restriction-count badge per object */}
      <div className="flex flex-wrap gap-1.5" role="tablist" aria-label="Objects">
        {objects.map((o) => {
          const selected = o.slug === selectedSlug;
          const count = counts[o.slug] ?? 0;
          return (
            <button
              key={o.slug}
              role="tab"
              aria-selected={selected}
              onClick={() => selectObject(o.slug)}
              className={`px-3 py-1.5 text-sm rounded-md border transition-colors ${
                selected
                  ? 'bg-blue-500 text-white border-blue-500'
                  : 'bg-background border-muted-foreground/20 hover:border-muted-foreground/40'
              }`}
            >
              <span className="mr-1">{o.icon}</span>
              {o.label}
              {count > 0 && (
                <span
                  aria-label={`${count} field restriction${count === 1 ? '' : 's'}`}
                  className={`ml-1.5 inline-flex items-center justify-center min-w-[1.125rem] px-1 rounded-full text-[10px] font-semibold leading-4 ${
                    selected ? 'bg-white/25 text-white' : 'bg-amber-500/15 text-amber-600'
                  }`}
                >
                  {count}
                </span>
              )}
            </button>
          );
        })}
      </div>

      {loadingGrid || !fieldGrid ? (
        <div className="text-sm text-muted-foreground py-6">Loading fields…</div>
      ) : fieldGrid.fields.length === 0 ? (
        <div className="text-sm text-muted-foreground py-6">This object has no fields to protect.</div>
      ) : (
        <>
          {/* Field filters */}
          <div className="flex flex-wrap items-center gap-3">
            <input
              type="search"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search fields…"
              aria-label="Search fields"
              className="text-sm rounded-md border border-muted-foreground/20 bg-background px-2.5 py-1.5 w-56"
            />
            <label className="flex items-center gap-1.5 text-sm cursor-pointer select-none">
              <input
                type="checkbox"
                checked={restrictedOnly}
                onChange={(e) => setRestrictedOnly(e.target.checked)}
              />
              Restricted only
            </label>
            {filtering && (
              <span className="text-xs text-muted-foreground">
                {visibleFields.length} of {fieldGrid.fields.length} fields
              </span>
            )}
          </div>

          {visibleFields.length === 0 ? (
            <div className="text-sm text-muted-foreground py-6">No fields match your filters.</div>
          ) : (
            <div className="border rounded-lg overflow-x-auto">
              <table className="w-full text-sm">
                <thead className="bg-muted/40">
                  <tr>
                    <th className="text-left font-medium px-3 py-2 align-top">Field</th>
                    {fieldGrid.roles.map((r) => (
                      <th
                        key={r.id}
                        className={`font-medium px-3 py-2 text-center whitespace-nowrap align-top ${
                          r.id === highlightRoleId ? 'bg-blue-500/10 text-blue-700' : ''
                        }`}
                      >
                        {r.is_owner ? (
                          <span className="inline-flex items-center gap-1">
                            {prettyRole(r.name)}
                            <Lock
                              className="w-3.5 h-3.5 text-muted-foreground"
                              role="img"
                              aria-label="Owner — full access"
                            />
                          </span>
                        ) : (
                          <>
                            <div>{prettyRole(r.name)}</div>
                            <select
                              value=""
                              aria-label={`Set all ${prettyRole(r.name)}`}
                              disabled={bulkRoleId !== '' || visibleFields.length === 0}
                              onChange={(e) => {
                                const v = e.target.value as FieldLevel;
                                if (v) bulkApply(r, v);
                              }}
                              className="mt-1 text-xs font-normal text-muted-foreground rounded-md border border-muted-foreground/20 bg-background px-1 py-0.5 disabled:opacity-60 disabled:cursor-not-allowed"
                            >
                              <option value="" disabled>
                                Set all…
                              </option>
                              {LEVELS.map((l) => (
                                <option key={l.value} value={l.value}>
                                  {l.label}
                                </option>
                              ))}
                            </select>
                          </>
                        )}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {visibleFields.map((f) => (
                    <tr key={f.key} className="border-t">
                      <td className="px-3 py-2">
                        {f.label}
                        {f.is_system && <span className="ml-1.5 text-xs text-muted-foreground">(system)</span>}
                      </td>
                      {fieldGrid.roles.map((r) => {
                        if (r.is_owner) {
                          // Owner bypasses FLS — a static cell instead of ~30
                          // pointless disabled selects per grid.
                          return (
                            <td
                              key={r.id}
                              className={`px-3 py-2 text-center text-xs text-muted-foreground ${
                                r.id === highlightRoleId ? 'bg-blue-500/5' : ''
                              }`}
                            >
                              Full access
                            </td>
                          );
                        }
                        const level = levelFor(r, f.key);
                        const key = `${r.id}:${f.key}`;
                        return (
                          <td key={r.id} className={`px-3 py-2 text-center ${r.id === highlightRoleId ? 'bg-blue-500/5' : ''}`}>
                            <select
                              value={level}
                              disabled={savingKey === key || bulkRoleId === r.id}
                              aria-label={`${prettyRole(r.name)} ${f.label}`}
                              onChange={(e) => change(r, f.key, e.target.value as FieldLevel)}
                              className="text-sm rounded-md border border-muted-foreground/20 bg-background px-1.5 py-1 disabled:opacity-60 disabled:cursor-not-allowed"
                            >
                              {LEVELS.map((l) => (
                                <option key={l.value} value={l.value}>
                                  {l.label}
                                </option>
                              ))}
                            </select>
                          </td>
                        );
                      })}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
      {dialog}
    </div>
  );
}
