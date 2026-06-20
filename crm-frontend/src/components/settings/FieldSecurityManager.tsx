import { useState, useEffect, useCallback, useMemo } from 'react';
import {
  getPermissionGrid,
  getFieldPermissionGrid,
  setFieldPermission,
  type PermObjectInfo,
  type PermRoleInfo,
  type FieldPermissionGrid,
  type FieldLevel,
} from '../../lib/api';

const LEVELS: { value: FieldLevel; label: string }[] = [
  { value: 'edit', label: 'Edit' },
  { value: 'read', label: 'Read' },
  { value: 'hidden', label: 'Hidden' },
];

// FieldSecurityManager is the admin per-object field × role visibility grid (P5b).
// It configures the Field-Level Security that RecordService enforces server-side:
// a 'hidden' field is stripped from the API response (not just the UI) and a
// 'read'/'hidden' field rejects writes. FLS is opt-in — every field defaults to
// full Edit access, so this screen only matters once a field is restricted. The
// owner role bypasses FLS, so its column is locked on Edit.
export default function FieldSecurityManager() {
  const [objects, setObjects] = useState<PermObjectInfo[]>([]);
  const [selectedSlug, setSelectedSlug] = useState('');
  const [fieldGrid, setFieldGrid] = useState<FieldPermissionGrid | null>(null);
  const [loadingObjects, setLoadingObjects] = useState(true);
  const [loadingGrid, setLoadingGrid] = useState(false);
  const [error, setError] = useState('');
  const [savingKey, setSavingKey] = useState('');

  // Load the object list once (reusing the OLS grid, which already returns the
  // org's objects for an admin) to populate the object selector.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const g = await getPermissionGrid();
        if (cancelled) return;
        setObjects(g.objects);
        setSelectedSlug((cur) => cur || (g.objects[0]?.slug ?? ''));
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : 'Failed to load objects');
      } finally {
        if (!cancelled) setLoadingObjects(false);
      }
    })();
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
    loadGrid(selectedSlug);
  }, [selectedSlug, loadGrid]);

  // (role_id, field_key) → level, for O(1) lookups. Only non-default cells exist.
  const cellMap = useMemo(() => {
    const m = new Map<string, FieldLevel>();
    fieldGrid?.matrix.forEach((c) => m.set(`${c.role_id}:${c.field_key}`, c.level));
    return m;
  }, [fieldGrid]);

  const levelFor = (role: PermRoleInfo, fieldKey: string): FieldLevel => {
    if (role.is_owner) return 'edit'; // owner bypasses FLS
    return cellMap.get(`${role.id}:${fieldKey}`) ?? 'edit';
  };

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

      {/* Object selector */}
      <div className="flex flex-wrap gap-1.5" role="tablist" aria-label="Objects">
        {objects.map((o) => (
          <button
            key={o.slug}
            role="tab"
            aria-selected={o.slug === selectedSlug}
            onClick={() => setSelectedSlug(o.slug)}
            className={`px-3 py-1.5 text-sm rounded-md border transition-colors ${
              o.slug === selectedSlug
                ? 'bg-blue-500 text-white border-blue-500'
                : 'bg-background border-muted-foreground/20 hover:border-muted-foreground/40'
            }`}
          >
            <span className="mr-1">{o.icon}</span>
            {o.label}
          </button>
        ))}
      </div>

      {loadingGrid || !fieldGrid ? (
        <div className="text-sm text-muted-foreground py-6">Loading fields…</div>
      ) : fieldGrid.fields.length === 0 ? (
        <div className="text-sm text-muted-foreground py-6">This object has no fields to protect.</div>
      ) : (
        <div className="border rounded-lg overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-muted/40">
              <tr>
                <th className="text-left font-medium px-3 py-2">Field</th>
                {fieldGrid.roles.map((r) => (
                  <th key={r.id} className="font-medium px-3 py-2 text-center whitespace-nowrap">
                    {r.name}
                    {r.is_owner ? ' 🔒' : ''}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {fieldGrid.fields.map((f) => (
                <tr key={f.key} className="border-t">
                  <td className="px-3 py-2">
                    {f.label}
                    {f.is_system && <span className="ml-1.5 text-xs text-muted-foreground">(system)</span>}
                  </td>
                  {fieldGrid.roles.map((r) => {
                    const level = levelFor(r, f.key);
                    const key = `${r.id}:${f.key}`;
                    return (
                      <td key={r.id} className="px-3 py-2 text-center">
                        <select
                          value={level}
                          disabled={r.is_owner || savingKey === key}
                          aria-label={`${r.name} ${f.label}`}
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
    </div>
  );
}
