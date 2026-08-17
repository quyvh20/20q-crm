import { useCallback, useEffect, useState } from 'react';
import { Bookmark, ChevronDown, Trash2 } from 'lucide-react';
import { Button, Popover, PopoverAnchor } from '@/components/ui';
import {
  createListView,
  deleteListView,
  listListViews,
  updateListView,
  type ListView,
  type ListViewDefinition,
} from '../../../lib/api';
import { useAuth } from '../../../lib/auth';

// ViewsMenu is the saved-views dropdown: apply a view, save the current list
// state as a new view, update the active view in place (owner only), delete.
// It owns its own data — the parent supplies the current list state as a
// definition and applies a chosen view back into the URL.
// canonicalJSON recursively sorts object keys so two structurally-equal values
// stringify identically. Load-bearing for the filter AST: it round-trips
// through Postgres jsonb, which re-serializes with its OWN key ordering, so a
// plain stringify comparison would flag every reloaded view as modified.
function canonicalJSON(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(canonicalJSON);
  if (v && typeof v === 'object') {
    const out: Record<string, unknown> = {};
    for (const k of Object.keys(v as Record<string, unknown>).sort()) {
      out[k] = canonicalJSON((v as Record<string, unknown>)[k]);
    }
    return out;
  }
  return v;
}

// normalizeDefinition puts a definition into a canonical comparable form so
// "did the list drift from the applied view" is order- and undefined-insensitive.
function normalizeDefinition(d: ListViewDefinition | null | undefined): string {
  return JSON.stringify(canonicalJSON({
    f: d?.filter ?? null,
    q: d?.q ?? '',
    t: [...(d?.tags ?? [])].sort(),
    sb: d?.sort_by ?? '',
    so: d?.sort_order ?? '',
  }));
}

export default function ViewsMenu({
  slug,
  currentDefinition,
  activeViewId,
  onApply,
  onReset,
}: {
  slug: string;
  /** The list's current state (filter/q/tags/sort) as a saveable definition. */
  currentDefinition: ListViewDefinition;
  activeViewId: string | null;
  onApply: (view: ListView) => void;
  onReset: () => void;
}) {
  const { user } = useAuth();
  const [open, setOpen] = useState(false);
  const [views, setViews] = useState<ListView[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [nameDraft, setNameDraft] = useState('');
  const [sharedDraft, setSharedDraft] = useState(false);
  const [naming, setNaming] = useState(false);

  const reload = useCallback(() => {
    listListViews(slug)
      .then((v) => {
        setViews(v);
        setLoaded(true);
      })
      .catch(() => {
        // A backend without the feature yet (or a transient failure) simply
        // renders no saved views — the menu still offers Save.
        setViews([]);
        setLoaded(true);
      });
  }, [slug]);

  useEffect(() => {
    setViews([]);
    setLoaded(false);
    setNaming(false);
    setError('');
  }, [slug]);

  // Load on open, and EAGERLY when a deep link names an active view — the
  // button label and the "modified" dot both need the view's definition before
  // the menu has ever been opened.
  useEffect(() => {
    if ((open || activeViewId) && !loaded) reload();
  }, [open, activeViewId, loaded, reload]);

  const active = views.find((v) => v.id === activeViewId) ?? null;
  const canUpdateActive = !!active && active.owner_user_id === user?.id;
  const modified = !!active && normalizeDefinition(active.definition) !== normalizeDefinition(currentDefinition);

  const hasAnythingToSave =
    !!currentDefinition.filter || !!currentDefinition.q || !!currentDefinition.tags?.length;

  const saveNew = async () => {
    const name = nameDraft.trim();
    if (!name) return;
    setSaving(true);
    setError('');
    try {
      const created = await createListView(slug, { name, shared: sharedDraft, definition: currentDefinition });
      setViews((prev) => [...prev, created].sort((a, b) => a.name.localeCompare(b.name)));
      setNaming(false);
      setNameDraft('');
      setSharedDraft(false);
      onApply(created);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to save view');
    } finally {
      setSaving(false);
    }
  };

  const updateActive = async () => {
    if (!active) return;
    setSaving(true);
    setError('');
    try {
      const updated = await updateListView(active.id, { definition: currentDefinition });
      setViews((prev) => prev.map((v) => (v.id === updated.id ? updated : v)));
      onApply(updated);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to update view');
    } finally {
      setSaving(false);
    }
  };

  const remove = async (view: ListView) => {
    setError('');
    try {
      await deleteListView(view.id);
      setViews((prev) => prev.filter((v) => v.id !== view.id));
      if (view.id === activeViewId) onReset();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to delete view');
    }
  };

  return (
    <PopoverAnchor>
      <Button
        variant="outline"
        size="sm"
        onClick={() => setOpen((o) => !o)}
        className={activeViewId ? 'border-primary/40 bg-primary/10 text-primary hover:bg-primary/15 hover:text-primary' : 'text-muted-foreground'}
      >
        <Bookmark aria-hidden />
        {active ? `${active.name}${modified ? ' •' : ''}` : 'Views'}
        <ChevronDown aria-hidden className="h-3.5 w-3.5 opacity-60" />
      </Button>
      <Popover open={open} onClose={() => setOpen(false)} className="w-72 p-2">
        <div className="flex flex-col gap-0.5">
          <button
            type="button"
            onClick={() => {
              onReset();
              setOpen(false);
            }}
            className={`rounded px-2 py-1.5 text-left text-sm hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${
              activeViewId ? '' : 'font-medium text-primary'
            }`}
          >
            All {slug === 'contact' ? 'contacts' : 'records'}
          </button>
          {views.map((v) => (
            <div key={v.id} className="group flex items-center gap-1">
              <button
                type="button"
                onClick={() => {
                  onApply(v);
                  setOpen(false);
                }}
                className={`min-w-0 flex-1 truncate rounded px-2 py-1.5 text-left text-sm hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${
                  v.id === activeViewId ? 'font-medium text-primary' : ''
                }`}
                title={v.name}
              >
                {v.name}
                {v.shared && <span className="ml-1.5 text-xs text-muted-foreground">shared</span>}
              </button>
              {v.owner_user_id === user?.id && (
                <button
                  type="button"
                  onClick={() => remove(v)}
                  aria-label={`Delete view ${v.name}`}
                  className="rounded p-1 text-muted-foreground opacity-0 hover:bg-destructive/10 hover:text-destructive focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring group-hover:opacity-100"
                >
                  <Trash2 aria-hidden className="h-3.5 w-3.5" />
                </button>
              )}
            </div>
          ))}
          {loaded && views.length === 0 && (
            <span className="px-2 py-1.5 text-xs text-muted-foreground">No saved views yet.</span>
          )}

          <div className="mt-1 border-t border-border pt-2">
            {canUpdateActive && modified && (
              <Button variant="ghost" size="sm" disabled={saving} onClick={updateActive} className="w-full justify-start">
                Update “{active?.name}”
              </Button>
            )}
            {naming ? (
              <div className="flex flex-col gap-2 px-1 pt-1">
                <input
                  type="text"
                  value={nameDraft}
                  onChange={(e) => setNameDraft(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') void saveNew();
                  }}
                  placeholder="View name…"
                  aria-label="View name"
                  className="h-8 rounded-lg border border-input bg-background px-2.5 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                />
                <label className="flex cursor-pointer items-center gap-2 text-xs text-muted-foreground">
                  <input
                    type="checkbox"
                    checked={sharedDraft}
                    onChange={(e) => setSharedDraft(e.target.checked)}
                    className="h-3.5 w-3.5 cursor-pointer rounded border-input accent-primary"
                  />
                  Share with everyone in the workspace
                </label>
                <div className="flex justify-end gap-2">
                  <Button variant="ghost" size="sm" onClick={() => setNaming(false)}>Cancel</Button>
                  <Button size="sm" disabled={saving || !nameDraft.trim()} onClick={saveNew}>Save</Button>
                </div>
              </div>
            ) : (
              <Button
                variant="ghost"
                size="sm"
                disabled={!hasAnythingToSave}
                onClick={() => setNaming(true)}
                className="w-full justify-start"
                title={hasAnythingToSave ? undefined : 'Apply a filter or search first'}
              >
                Save current view…
              </Button>
            )}
            {error && <p className="px-2 pt-1 text-xs text-destructive">{error}</p>}
          </div>
        </div>
      </Popover>
    </PopoverAnchor>
  );
}
