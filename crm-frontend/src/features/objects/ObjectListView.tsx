import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { useNavigate, useSearchParams, Link } from 'react-router-dom';
import {
  ArrowDown,
  ArrowUp,
  ArrowUpDown,
  Check,
  Download,
  LayoutGrid,
  List,
  Plus,
  Search,
  Sparkles,
  Tag as TagIcon,
  Trash2,
  Upload,
  X,
} from 'lucide-react';
import {
  getObjectSchema,
  listObjectRecordsUnified,
  exportRecordsCsv,
  getPipelines,
  getTags,
  getStages,
  bulkContactAction,
  type ObjectSchema,
  type ObjectFieldDescriptor,
  type UniformRecord,
  type RecordSort,
  type Tag,
  type Pipeline,
} from '../../lib/api';
import { useAuth } from '../../lib/auth';
import { formatFieldValue } from './fieldHelpers';
import ObjectForm from './ObjectForm';
import ObjectKanban from './ObjectKanban';
import ImportModal from '../../components/contacts/ImportModal';
import GenericImportModal from './GenericImportModal';
import Modal from '../../components/common/Modal';
import { useConfirm } from '../../components/common/ConfirmDialog';
import {
  Button,
  EmptyState,
  ErrorState,
  Input,
  PageHeader,
  Select,
  Skeleton,
  SpinnerBlock,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  TableShell,
} from '@/components/ui';
import { recordPath } from './recordRoutes';
import { usePermissions } from '../../lib/auth';

interface ObjectListViewProps {
  slug: string;
  /** Called when the object/schema can't be loaded (e.g. unknown slug). */
  onNotFound?: () => void;
  /**
   * Called with the object's schema once it loads. Lets the wrapping PAGE name
   * itself — /objects/:slug only knows a slug, and the human label ("Invoices")
   * lives in the schema this component already fetches (U7.2). Handing it up
   * avoids a second request for the same document.
   * Must be stable (useCallback) — it is an effect dependency.
   */
  onSchemaLoaded?: (schema: ObjectSchema) => void;
}

// Viewing/editing/deleting a record now happens on its own URL-addressable page
// (ObjectRecordPage); the list only opens the shared create form in a slide-over.
type Panel =
  | { mode: 'create' }
  | null;

interface RelationOption {
  id: string;
  label: string;
}

const LIMIT = 25;
const MAX_COLUMNS = 4;
// The backend clamps any limit above 100 down to 100 (usecase.maxRecordLimit),
// so 100 is the largest page a single request can return. Relation dropdowns
// want more than that, so they PAGE to a cap rather than asking for one huge
// page the server would silently shrink.
const RELATION_PAGE_SIZE = 100;
const RELATION_OPTION_CAP = 200;
// Request-count stop condition, independent of how many records come back, so a
// cursor that never terminates cannot turn this into an endless fetch loop.
const RELATION_MAX_PAGES = Math.ceil(RELATION_OPTION_CAP / RELATION_PAGE_SIZE);

// ── URL state (R7.2) ────────────────────────────────────────────────────────
//
// Every knob that shapes the list lives in the query string, so a filtered view
// survives reload and back/forward and can be pasted to a colleague. What is in
// and what is out, and why:
//
//   in   q, f.<field>, tags, ai, view, sort_by, sort_order
//   out  cursor      — opaque, and R4 made a stale one fail SOFT to page 1. A
//                      URL-borne cursor would therefore be silently wrong: the
//                      link's owner sees page 3, the recipient sees page 1 with
//                      no indication anything was dropped. Refresh returning to
//                      page 1 is the honest behaviour, not a regression.
//   out  selection   — a link that pre-selects rows for a bulk delete is a
//                      footgun, and selection is meaningless without the exact
//                      page set that produced it (which `cursor` is not in).
//   out  create/import panel — transient modals; deep-linking them was not asked
//                      for and would make every share reopen a blank form.
//
// Relation filters are PREFIXED. The backend's own list route learned this the
// hard way: any unreserved query param there becomes a jsonb field filter, so
// reserved names and admin-defined field keys share one namespace. An object
// with a field keyed `view` or `q` would otherwise fight the view toggle.
const FILTER_PREFIX = 'f.';
const SEARCH_DEBOUNCE_MS = 300;

// One shared empty set, so "cleared" is always the same identity and clearing an
// already-empty selection never re-renders the table for free. Typed readonly and
// only ever replaced, never mutated.
const EMPTY_SELECTION: ReadonlySet<string> = new Set<string>();

// ObjectListView renders any object — system or custom — from its registry
// schema: one table, one schema-driven filter bar, one search box, one
// create/edit/detail slide-over. It is the component every object page now points
// at (P3 "one renderer"); the filter bar brings it to parity with the legacy
// per-object pages (P7) — relation filters, tag filter, and semantic search all
// driven by the schema, with zero per-object code.
export default function ObjectListView({ slug, onNotFound, onSchemaLoaded }: ObjectListViewProps) {
  const navigate = useNavigate();
  // OLS-aware buttons (U3.7): a role without create access doesn't get an Add
  // button that would only 403. Fails open while permissions are still loading —
  // the server enforces regardless.
  const { canAccess } = usePermissions();
  const { hasCapability } = useAuth();
  const canCreate = canAccess(slug, 'create');
  const canExport = hasCapability('data.export');
  const [exporting, setExporting] = useState(false);
  const openRecord = useCallback(
    (rec: UniformRecord) => navigate(recordPath(slug, rec.id)),
    [navigate, slug],
  );
  const [schema, setSchema] = useState<ObjectSchema | null>(null);
  const [records, setRecords] = useState<UniformRecord[]>([]);
  const [nextCursor, setNextCursor] = useState<string | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  // A failed list is NOT an empty list. Held separately from `records` so the
  // renderer can tell "the org has no deals" apart from "we could not ask".
  const [loadError, setLoadError] = useState<unknown>(null);
  const [moreError, setMoreError] = useState<unknown>(null);
  const [panel, setPanel] = useState<Panel>(null);
  const [showImport, setShowImport] = useState(false);
  // The ordering the SERVER says it applied, plus the keys it will accept. Read
  // off every page rather than assumed, so an object that cannot sort simply
  // renders no sort affordance (see api.ts RecordSort).
  const [sortInfo, setSortInfo] = useState<RecordSort | null>(null);

  // CSV import is a contact-specific affordance (the bulk importer is contact-aware).
  // Contacts keep the dedup/company-resolution-aware ImportModal; every other
  // object gets the generic column-mapped importer (R8.2).
  const supportsImport = true;
  const useGenericImport = slug !== 'contact';

  const [searchParams, setSearchParams] = useSearchParams();

  // Patch the query string while preserving sibling params, so search, filters,
  // tags, sort and view never clobber one another. A null/'' value drops the key.
  const updateParams = useCallback(
    (patch: Record<string, string | null>, opts?: { replace?: boolean }) => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          for (const [k, v] of Object.entries(patch)) {
            if (v === null || v === '') next.delete(k);
            else next.set(k, v);
          }
          return next;
        },
        { replace: opts?.replace },
      );
    },
    [setSearchParams],
  );

  const q = (searchParams.get('q') ?? '').trim();
  const semantic = searchParams.get('ai') === '1';
  const view: 'table' | 'board' = searchParams.get('view') === 'board' ? 'board' : 'table';
  const sortBy = searchParams.get('sort_by') ?? '';
  const sortOrder: 'asc' | 'desc' = searchParams.get('sort_order') === 'asc' ? 'asc' : 'desc';

  // Both derived sets are memoised off a STRING, not off `searchParams`, whose
  // identity changes when any unrelated param does. Without that, toggling the
  // board view would hand `filters` a new identity and refetch the whole list.
  const tagsParam = searchParams.get('tags') ?? '';
  const tagIds = useMemo(
    () => tagsParam.split(',').map((s) => s.trim()).filter(Boolean),
    [tagsParam],
  );
  // Serialised as JSON, not as a `k=v&k=v` string: a filter value is arbitrary
  // text once a non-relation field becomes filterable, and a `&` or `=` inside one
  // would re-split into two filters. JSON round-trips unambiguously, and sorting
  // the keys first keeps the string stable under param reordering.
  const filterQuery = useMemo(() => {
    const keys: string[] = [];
    searchParams.forEach((value, key) => {
      if (key.startsWith(FILTER_PREFIX) && value) keys.push(key);
    });
    keys.sort();
    const out: Record<string, string> = {};
    for (const key of keys) out[key.slice(FILTER_PREFIX.length)] = searchParams.get(key) as string;
    return JSON.stringify(out);
  }, [searchParams]);
  const filters = useMemo(() => JSON.parse(filterQuery) as Record<string, string>, [filterQuery]);

  // ── Selection (R7.1) ──────────────────────────────────────────────────────
  //
  // Bulk actions are CONTACTS ONLY: /api/contacts/bulk-action is the app's one
  // bulk surface, and RecordService deliberately has none (R7 cut it — R8.2 owns
  // the real bulk-write design). So this is not "the list has bulk actions", it
  // is "the contact list does".
  const [selectedIds, setSelectedIds] = useState<ReadonlySet<string>>(EMPTY_SELECTION);
  const clearSelection = useCallback(() => setSelectedIds(EMPTY_SELECTION), []);
  const [bulkBusy, setBulkBusy] = useState(false);
  const [bulkError, setBulkError] = useState<unknown>(null);
  const [bulkNotice, setBulkNotice] = useState('');
  const { confirm, dialog: confirmDialog } = useConfirm();

  // The search box's debounce state. Declared here, above patchQuery, rather
  // than down with the rest of the search-box code, because patchQuery has to be
  // able to cancel a commit that is still in flight. Full design in the "Search
  // box" section below.
  const searchRef = useRef<HTMLInputElement>(null);
  const searchTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const committedQ = useRef(q);

  // Every query-shaping control goes through here, so "changing what the list
  // shows clears the selection" is one rule in one place rather than a habit.
  //
  // It is also where a pending search debounce dies — but ONLY when the patch
  // names `q`, and both halves of that are load-bearing:
  //
  //   - Cancelling in `clearFilters` alone would fix exactly one button. The
  //     bug is not "Clear filters forgot something", it is "a control rewrote
  //     `q` while a keystroke was still in flight", and the sync effect below
  //     cannot cover for it: its guard (`q === committedQ.current`) is TRUE when
  //     both are '', which is precisely the reproduction — clear with `?tags=…`
  //     set and a term typed but not yet committed, and 300ms later the debounce
  //     APPLIES a search the user pressed "clear" to get rid of. Any future
  //     control that resets the search (a clear-search X in the box, a saved
  //     view, a preset chip) would have to remember the same incantation.
  //   - Cancelling unconditionally would be too broad in the other direction:
  //     picking a filter or a tag mid-word would silently drop the term and
  //     leave the box showing text that was never applied. A patch that does not
  //     mention `q` has no opinion about the search, so it leaves it alone.
  const patchQuery = useCallback(
    (patch: Record<string, string | null>, opts?: { replace?: boolean }) => {
      if ('q' in patch) {
        clearTimeout(searchTimer.current);
        const next = patch.q ?? '';
        committedQ.current = next;
        // Write the box back only when it actually disagrees. `commitSearch`
        // lands here too, and rewriting the node's value mid-typing would jump
        // the caret to the end; compared trimmed, because trimmed is what gets
        // committed.
        const box = searchRef.current;
        if (box && box.value.trim() !== next) box.value = next;
      }
      updateParams(patch, opts);
      clearSelection();
      setBulkNotice('');
      setBulkError(null);
    },
    [updateParams, clearSelection],
  );

  // Relation fields the schema says we can filter on (a resolvable target object).
  const relationFields = useMemo(
    () => (schema?.fields ?? []).filter((f) => f.type === 'relation' && f.target_slug),
    [schema],
  );
  // Semantic search is wired for contacts (native vector index).
  const supportsSemantic = slug === 'contact';
  // A relation field named "stage" makes the object board-able (deals today).
  const stageField = useMemo(
    () => (schema?.fields ?? []).find((f) => f.key === 'stage' && f.type === 'relation'),
    [schema],
  );

  const [relationOptions, setRelationOptions] = useState<Record<string, RelationOption[]>>({});
  const [tags, setTags] = useState<Tag[]>([]);
  // R9.3: the org's boards, loaded only for a board-able object (deals today).
  const [pipelines, setPipelines] = useState<Pipeline[]>([]);

  // Reset the state that is NOT in the URL when switching objects. Filters, q,
  // tags, sort and view need no reset here: they live in the query string, and
  // navigating to another object is a new path with its own (empty) search — the
  // URL resets them for free, which is one of the quieter wins of R7.2.
  useEffect(() => {
    setSchema(null);
    setPanel(null);
    setRelationOptions({});
    setSelectedIds(EMPTY_SELECTION);
    setSortInfo(null);
    setBulkNotice('');
    setBulkError(null);
  }, [slug]);

  useEffect(() => {
    let cancelled = false;
    getObjectSchema(slug)
      .then((s) => {
        if (cancelled) return;
        setSchema(s);
        onSchemaLoaded?.(s);
      })
      .catch(() => {
        if (!cancelled) onNotFound?.();
      });
    return () => {
      cancelled = true;
    };
  }, [slug, onNotFound, onSchemaLoaded]);

  // Load tags for the tag filter (every object is taggable).
  useEffect(() => {
    let cancelled = false;
    getTags()
      .then((t) => {
        if (!cancelled) setTags(t);
      })
      .catch(() => {
        if (!cancelled) setTags([]);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Load options for each filterable relation field from its target object.
  //
  // This fetch does double duty: it fills the filter dropdown AND it is the map
  // `relationLabel` reads to render relation CELLS. A target outside the fetched
  // window has no label, and formatFieldValue falls back to the raw value — i.e.
  // a bare UUID in the Company column. It used to ask for a single page of 200,
  // which the server reset to 25; now it pages to RELATION_OPTION_CAP, and each
  // page is published as it lands so labels resolve progressively.
  useEffect(() => {
    let cancelled = false;
    relationFields.forEach((f) => {
      const target = f.target_slug as string;
      (async () => {
        const acc: RelationOption[] = [];
        let cursor: string | undefined;
        let fetched = 0;
        do {
          const page = await listObjectRecordsUnified(target, { limit: RELATION_PAGE_SIZE, cursor });
          fetched += 1;
          // Re-checked after EVERY await: a slug switch mid-page must not keep
          // paging, and must not write into the new object's option map.
          if (cancelled) return;
          acc.push(...page.records.map((r) => ({ id: r.id, label: r.display || r.id })));
          cursor = page.next_cursor;
          setRelationOptions((prev) => ({ ...prev, [f.key]: [...acc] }));
        } while (cursor && acc.length < RELATION_OPTION_CAP && fetched < RELATION_MAX_PAGES);
      })().catch(() => {
        /* a relation we can't enumerate is simply not filterable */
      });
    });
    return () => {
      cancelled = true;
    };
  }, [relationFields]);

  // R9.3: the org's boards. Only fetched for a board-able object, and the
  // selected one lives in the query string so a board link survives a reload
  // and can be pasted to a colleague — the same rule as every other list knob.
  useEffect(() => {
    if (!stageField) return;
    let cancelled = false;
    getPipelines()
      .then((p) => { if (!cancelled) setPipelines(p); })
      .catch(() => { /* the board falls back to the org default below */ });
    return () => { cancelled = true; };
  }, [stageField]);

  const pipelineParam = searchParams.get('pipeline') ?? '';
  const activePipelineId = useMemo(() => {
    if (pipelines.length === 0) return undefined;
    if (pipelineParam && pipelines.some((p) => p.id === pipelineParam)) return pipelineParam;
    return (pipelines.find((p) => p.is_default) ?? pipelines[0]).id;
  }, [pipelines, pipelineParam]);

  // A "stage" relation has no registry target (pipeline_stages isn't a registered
  // object), so its labels come from the pipeline stages. Loaded into the same
  // option map so relation cells resolve to the stage name, not its id.
  useEffect(() => {
    if (!stageField) return;
    let cancelled = false;
    // Deliberately UNSCOPED (R9.3): a table row can show a deal from any board,
    // so label resolution needs every stage. Only the BOARD view scopes.
    getStages()
      .then((s) => {
        if (!cancelled) {
          setRelationOptions((prev) => ({ ...prev, [stageField.key]: s.map((st) => ({ id: st.id, label: st.name })) }));
        }
      })
      .catch(() => {
        /* stage labels just fall back to the id */
      });
    return () => {
      cancelled = true;
    };
  }, [stageField]);

  // Resolve a relation field's id value to the target's display label, using the
  // already-loaded option maps. Falls back to the raw value when unknown.
  const relationLabel = useCallback(
    (field: ObjectFieldDescriptor, value: unknown): string | undefined => {
      if (field.type !== 'relation' || value == null || value === '') return undefined;
      const opt = (relationOptions[field.key] ?? []).find((o) => o.id === value);
      return opt?.label;
    },
    [relationOptions],
  );

  // ── Search box ────────────────────────────────────────────────────────────
  //
  // The URL is the source of truth for `q`, but the box is UNCONTROLLED and
  // debounced into the URL — deliberately NOT WorkflowList's shape, which mirrors
  // the URL back into React state with `setSearchInput(...)` inside an effect.
  // That mirror is what this avoids: it is a set-state-in-effect (a warning the
  // ratchet has no room for), and it leaves two effects racing — a back-navigation
  // first re-arms the debounce with the STALE typed value, and is saved only by the
  // mirror's re-render cancelling the timer a few microseconds later. Writing the
  // URL on every keystroke instead would remove the mirror too, but Safari throttles
  // history.replaceState to ~100 calls per 30s and then silently ignores them, which
  // for a URL-controlled input means the box FREEZES mid-word.
  //
  // So: type → DOM (free, no React), debounce → URL, and one effect that pushes the
  // URL back into the DOM node only when `q` changed for a reason that was not our
  // own commit (back/forward, a deep link, an object switch).
  //
  // `searchRef` / `searchTimer` / `committedQ` are declared above patchQuery,
  // which owns cancelling an in-flight commit and marking what was committed.

  const commitSearch = useCallback(
    (raw: string) => {
      // patchQuery records this as the committed term and leaves the (already
      // correct) box alone — one owner for `committedQ`, not two.
      // replace: a keystroke is not a history entry.
      patchQuery({ q: raw.trim() || null }, { replace: true });
    },
    [patchQuery],
  );

  // The debounce fires a callback captured at KEYSTROKE time, and `commitSearch`
  // transitively closes over react-router's render-time params: v7 memoises
  // `setSearchParams` on `location.search`, so even its functional form hands you
  // the snapshot of the render that produced the setter, not the live one.
  // Committing through a stale copy writes the OLD param set back and silently
  // undoes whatever ran in between — toggle AI Search mid-word and `ai=1`
  // disappears 300ms later. This ref always holds the newest, so a commit that
  // survives an unrelated patch (which is the deliberate behaviour — see
  // patchQuery) merges with it instead of clobbering it.
  const commitRef = useRef(commitSearch);
  useEffect(() => {
    commitRef.current = commitSearch;
  }, [commitSearch]);

  useEffect(() => {
    if (q === committedQ.current) return;
    // The URL moved under us. Cancel any pending commit first — otherwise a
    // debounce armed before a Back press lands after it and re-commits the term
    // the user just navigated away from.
    clearTimeout(searchTimer.current);
    committedQ.current = q;
    if (searchRef.current) searchRef.current.value = q;
  }, [q]);

  useEffect(() => () => clearTimeout(searchTimer.current), []);

  const listParams = useCallback(
    (cursor?: string) => ({
      limit: LIMIT,
      q,
      cursor,
      filters,
      tagIds,
      semantic: semantic && supportsSemantic,
      sortBy: sortBy || undefined,
      sortOrder: sortBy ? sortOrder : undefined,
    }),
    [q, filters, tagIds, semantic, supportsSemantic, sortBy, sortOrder],
  );

  // handleExport downloads the CURRENT filtered view as CSV (R8.2) — same
  // q/filters/tags/sort as listParams(), minus cursor/limit (the export
  // endpoint pages internally, capped server-side).
  const handleExport = useCallback(async () => {
    setExporting(true);
    try {
      const blob = await exportRecordsCsv(slug, listParams());
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `${slug}-export-${new Date().toISOString().slice(0, 10)}.csv`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } catch {
      // Best-effort: a failed export just leaves nothing downloaded — the user
      // can retry, and a toast primitive isn't in this file yet (R7.4).
    } finally {
      setExporting(false);
    }
  }, [slug, listParams]);

  const fetchFirstPage = useCallback(async () => {
    setLoading(true);
    try {
      const page = await listObjectRecordsUnified(slug, listParams());
      setRecords(page.records);
      setNextCursor(page.next_cursor);
      setSortInfo(page.sort ?? null);
      setLoadError(null);
      setMoreError(null);
    } catch (e) {
      setRecords([]);
      setNextCursor(undefined);
      setLoadError(e);
    } finally {
      setLoading(false);
    }
  }, [slug, listParams]);

  useEffect(() => {
    fetchFirstPage();
  }, [fetchFirstPage]);

  const loadMore = async () => {
    if (!nextCursor) return;
    setLoadingMore(true);
    try {
      const page = await listObjectRecordsUnified(slug, listParams(nextCursor));
      // Drop ids we already hold — the same guard ObjectKanban runs, for the
      // same reason. Rows are keyed by record id, so one repeat gives React two
      // rows with the same key: it keeps one, and a click on the survivor opens
      // whichever record won reconciliation. OFFSET paging over a non-total
      // ordering (equal created_at, custom objects) is enough to produce that.
      setRecords((prev) => {
        const seen = new Set(prev.map((r) => r.id));
        const fresh: UniformRecord[] = [];
        for (const r of page.records) {
          if (seen.has(r.id)) continue;
          seen.add(r.id);
          fresh.push(r);
        }
        // Nothing new: keep the identity so the table doesn't re-render for free.
        return fresh.length === 0 ? prev : [...prev, ...fresh];
      });
      setNextCursor(page.next_cursor);
      // Re-read rather than trust page 1's copy: across a deploy that changed the
      // sortable set, a header must not stay wired to a key the server dropped.
      if (page.sort) setSortInfo(page.sort);
      setMoreError(null);
    } catch (e) {
      // Keep the rows we already have, but say why the button did nothing —
      // a "Load more" that silently no-ops reads as a dead button.
      setMoreError(e);
    } finally {
      setLoadingMore(false);
    }
  };

  const closePanel = () => setPanel(null);

  const handleSaved = () => {
    closePanel();
    fetchFirstPage();
  };

  const setFilter = (key: string, value: string) => {
    patchQuery({ [FILTER_PREFIX + key]: value || null });
  };

  const toggleTag = (id: string) => {
    const next = tagIds.includes(id) ? tagIds.filter((t) => t !== id) : [...tagIds, id];
    patchQuery({ tags: next.join(',') || null });
  };

  const clearFilters = () => {
    // `q: null` is what tells patchQuery to kill a search that is still inside
    // its debounce window and blank the box — see the note there. Without that,
    // "clear" could APPLY a filter 300ms later.
    const cleared: Record<string, string | null> = { q: null, tags: null, ai: null };
    for (const key of Object.keys(filters)) cleared[FILTER_PREFIX + key] = null;
    // Sort survives: it is a view preference, not a filter, and wiping it would
    // silently re-order the list the user is looking at.
    patchQuery(cleared);
  };

  const hasActiveFilters =
    Object.keys(filters).length > 0 || tagIds.length > 0 || semantic || !!q;

  // ── Sorting (R7.3) ────────────────────────────────────────────────────────
  const sortableKeys = useMemo(() => new Set(sortInfo?.sortable ?? []), [sortInfo]);

  const toggleSort = (key: string) => {
    if (!sortableKeys.has(key)) return;
    // A new column starts ascending; the active column flips. The active column
    // is read from sortInfo (what the server ran), not from the URL, so a key the
    // server rejected can never leave the arrow stuck on a dead header.
    const nextOrder = sortInfo?.by === key && sortInfo.order === 'asc' ? 'desc' : 'asc';
    patchQuery({ sort_by: key, sort_order: nextOrder });
  };

  const ariaSortFor = (key: string): 'ascending' | 'descending' | 'none' | undefined => {
    if (!sortableKeys.has(key)) return undefined;
    if (sortInfo?.by !== key) return 'none';
    return sortInfo.order === 'asc' ? 'ascending' : 'descending';
  };

  const sortHeader = (key: string, label: string) => {
    if (!sortableKeys.has(key)) return label;
    const active = sortInfo?.by === key;
    const Icon = !active ? ArrowUpDown : sortInfo?.order === 'asc' ? ArrowUp : ArrowDown;
    return (
      <button
        type="button"
        onClick={() => toggleSort(key)}
        className={`-mx-1 inline-flex items-center gap-1 rounded px-1 py-0.5 text-xs font-medium uppercase tracking-wider transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${
          active ? 'text-foreground' : 'text-muted-foreground hover:text-foreground'
        }`}
      >
        {label}
        <Icon aria-hidden className={`h-3 w-3 ${active ? '' : 'opacity-50'}`} />
      </button>
    );
  };

  if (!schema) {
    return <SpinnerBlock label="Loading…" />;
  }

  const columns = schema.fields.slice(0, MAX_COLUMNS);
  const emptyMessage = hasActiveFilters
    ? `No ${schema.label_plural.toLowerCase()} match your filters.`
    : canCreate
      ? `No ${schema.label_plural.toLowerCase()} yet.`
      : `No ${schema.label_plural.toLowerCase()} to show.`;

  // ── Bulk actions, contacts only ───────────────────────────────────────────
  //
  // The two verbs are gated INDEPENDENTLY, matching the server: the bulk endpoint
  // multiplexes them and checks Delete for `delete` and Edit for `assign_tag`
  // inside the usecase (80c240e). "Edit but not delete" is a shipped default role,
  // so one combined gate would either hide tagging from a tagger or offer a delete
  // that only 403s.
  const supportsBulk = slug === 'contact';
  const canBulkDelete = supportsBulk && canAccess(slug, 'delete');
  const canBulkTag = supportsBulk && canAccess(slug, 'edit');
  const showSelection = view === 'table' && (canBulkDelete || canBulkTag);

  // The EFFECTIVE selection is the raw set INTERSECTED with what is on screen.
  // That intersection is the safety property, not a tidiness one: a bulk call can
  // only ever name rows the user can currently see, so a selection that outlived a
  // filter change cannot delete records that scrolled out of the result set. The
  // handlers below deliberately read this and never `selectedIds`.
  const selectedRecords = records.filter((r) => selectedIds.has(r.id));
  const allLoadedSelected = records.length > 0 && selectedRecords.length === records.length;

  const toggleRow = (id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
    setBulkNotice('');
  };

  // "Select all" means all LOADED rows — the ones the checkbox column is drawn
  // next to. There is no "select all matching": the count behind a filter is not
  // known to the client, and a control that silently means "and the 900 you have
  // not seen" is how bulk deletes go wrong.
  const toggleAllLoaded = () => {
    setSelectedIds(allLoadedSelected ? EMPTY_SELECTION : new Set(records.map((r) => r.id)));
    setBulkNotice('');
  };

  const runBulk = async (fn: () => Promise<string>) => {
    setBulkBusy(true);
    setBulkError(null);
    try {
      const notice = await fn();
      setBulkNotice(notice);
    } catch (e) {
      setBulkError(e);
    } finally {
      setBulkBusy(false);
    }
  };

  const handleBulkDelete = async () => {
    const ids = selectedRecords.map((r) => r.id);
    if (ids.length === 0) return;
    const noun = ids.length === 1 ? schema.label.toLowerCase() : schema.label_plural.toLowerCase();
    // The app's dialog primitive, never confirm(): confirm() is unstyled, blocks
    // the whole tab, and cannot say what is about to be destroyed.
    const ok = await confirm({
      title: `Delete ${ids.length} ${noun}?`,
      body: `This removes ${ids.length === 1 ? 'the selected contact' : `all ${ids.length} selected contacts`} from your lists. Their lead-capture history and marketing-consent provenance are erased permanently and are not restored if the contact is undeleted.`,
      confirmLabel: `Delete ${ids.length} ${noun}`,
      tone: 'danger',
    });
    if (!ok) return;
    await runBulk(async () => {
      await bulkContactAction('delete', ids);
      clearSelection();
      // No toast: the durable feedback is the rows leaving the table. A refetch
      // also re-derives the count and the cursor, which a local splice would not.
      await fetchFirstPage();
      return '';
    });
  };

  const handleBulkTag = async (tagId: string) => {
    const ids = selectedRecords.map((r) => r.id);
    if (ids.length === 0 || !tagId) return;
    const tagName = tags.find((t) => t.id === tagId)?.name ?? 'tag';
    await runBulk(async () => {
      const res = await bulkContactAction('assign_tag', ids, tagId);
      // Tags are not a column here, so nothing on screen would change — which is
      // exactly when a durable line beats a toast that vanishes before it is read.
      // The selection is kept so a second tag can be applied to the same rows.
      return `Tagged ${res.affected} ${res.affected === 1 ? 'contact' : 'contacts'} as ${tagName}.`;
    });
  };

  return (
    <div className="mx-auto w-full max-w-6xl">
      <PageHeader
        title={
          <span className="flex items-center gap-3">
            <span aria-hidden className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-muted text-lg">
              {schema.icon}
            </span>
            {schema.label_plural}
          </span>
        }
        description={`Manage your ${schema.label_plural.toLowerCase()}`}
        actions={
          <>
            {/* R9.3 board switcher — only meaningful once a second board exists,
                so a single-pipeline org sees no extra chrome. */}
            {stageField && view === 'board' && pipelines.length > 1 && (
              <select
                aria-label="Pipeline"
                value={activePipelineId ?? ''}
                onChange={(e) => updateParams({ pipeline: e.target.value })}
                className="h-9 rounded-lg border border-input bg-background px-2 text-xs font-medium shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                {pipelines.map((p) => (
                  <option key={p.id} value={p.id}>{p.name}</option>
                ))}
              </select>
            )}
            {stageField && (
              <div className="inline-flex items-center rounded-lg border border-input bg-background p-0.5 shadow-sm">
                {(['table', 'board'] as const).map((v) => (
                  <button
                    key={v}
                    onClick={() => updateParams({ view: v === 'board' ? 'board' : null })}
                    className={`inline-flex h-7 items-center gap-1.5 rounded-md px-2.5 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${
                      view === v ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:text-foreground'
                    }`}
                  >
                    {v === 'table' ? <List aria-hidden className="h-3.5 w-3.5" /> : <LayoutGrid aria-hidden className="h-3.5 w-3.5" />}
                    {v === 'table' ? 'Table' : 'Board'}
                  </button>
                ))}
              </div>
            )}
            {supportsImport && canCreate && (
              <Button variant="outline" onClick={() => setShowImport(true)}>
                <Upload aria-hidden /> Import
              </Button>
            )}
            {canExport && (
              <Button variant="outline" onClick={handleExport} disabled={exporting}>
                <Download aria-hidden /> {exporting ? 'Exporting…' : 'Export'}
              </Button>
            )}
            {canCreate && (
              <Button onClick={() => setPanel({ mode: 'create' })}>
                <Plus aria-hidden /> Add {schema.label}
              </Button>
            )}
          </>
        }
      />

      {showImport && useGenericImport && (
        <GenericImportModal
          schema={schema}
          onClose={() => setShowImport(false)}
          onSuccess={() => {
            setShowImport(false);
            fetchFirstPage();
          }}
        />
      )}
      {showImport && !useGenericImport && (
        <ImportModal
          onClose={() => setShowImport(false)}
          onSuccess={() => {
            setShowImport(false);
            fetchFirstPage();
          }}
        />
      )}

      {view === 'board' && stageField ? (
        <ObjectKanban
          schema={schema}
          stageKey={stageField.key}
          pipelineId={activePipelineId}
          onCardClick={openRecord}
        />
      ) : (
      <>
      {/* Search + filters */}
      <div className="mb-4 flex flex-wrap items-center gap-2.5">
        <div className="relative w-72 max-w-full">
          <Search aria-hidden className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            ref={searchRef}
            defaultValue={q}
            onChange={(e) => {
              const value = e.target.value;
              clearTimeout(searchTimer.current);
              // Through the ref, never the captured `commitSearch` — see above.
              searchTimer.current = setTimeout(() => commitRef.current(value), SEARCH_DEBOUNCE_MS);
            }}
            aria-label={`Search ${schema.label_plural.toLowerCase()}`}
            placeholder={semantic && supportsSemantic ? `Describe the ${schema.label.toLowerCase()} you want…` : `Search ${schema.label_plural.toLowerCase()}...`}
            className="pl-9"
          />
        </div>

        {supportsSemantic && (
          <Button
            variant="outline"
            onClick={() => patchQuery({ ai: semantic ? null : '1' })}
            title="Toggle AI semantic search"
            className={semantic ? 'border-primary/40 bg-primary/10 text-primary hover:bg-primary/15 hover:text-primary' : 'text-muted-foreground'}
          >
            <Sparkles aria-hidden /> AI Search{semantic ? ' ON' : ''}
          </Button>
        )}

        {/* One dropdown per filterable relation field (company, contact, …). */}
        {relationFields.map((f: ObjectFieldDescriptor) => (
          <Select
            key={f.key}
            value={filters[f.key] ?? ''}
            onChange={(e) => setFilter(f.key, e.target.value)}
            aria-label={`Filter by ${f.label}`}
            className="w-auto min-w-[10rem] max-w-[13rem]"
          >
            <option value="">All {f.label}</option>
            {(relationOptions[f.key] ?? []).map((o) => (
              <option key={o.id} value={o.id}>{o.label}</option>
            ))}
          </Select>
        ))}

        {hasActiveFilters && (
          <Button variant="ghost" onClick={clearFilters} className="text-muted-foreground">
            <X aria-hidden /> Clear filters
          </Button>
        )}
      </div>

      {/* Tag filter chips — border/tint take the tag's own color when active,
          so the palette here is user data, not hardcoded chrome. */}
      {tags.length > 0 && (
        <div className="mb-4 flex flex-wrap gap-1.5">
          {tags.map((t) => {
            const active = tagIds.includes(t.id);
            return (
              <button
                key={t.id}
                onClick={() => toggleTag(t.id)}
                aria-pressed={active}
                className={`inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${
                  active ? '' : 'border-border bg-background text-muted-foreground hover:bg-accent hover:text-foreground'
                }`}
                style={active ? { borderColor: t.color, color: t.color, background: `${t.color}1a` } : undefined}
              >
                {t.name}
                {active && <Check aria-hidden className="h-3 w-3" />}
              </button>
            );
          })}
        </div>
      )}

      {/* Bulk action bar — only while something is selected. */}
      {showSelection && selectedRecords.length > 0 && (
        <div className="mb-3 flex flex-wrap items-center gap-2.5 rounded-xl border border-border bg-muted/40 px-3 py-2">
          <span className="text-sm font-medium text-foreground">
            {selectedRecords.length} selected
          </span>
          {canBulkTag && tags.length > 0 && (
            <Select
              value=""
              disabled={bulkBusy}
              onChange={(e) => handleBulkTag(e.target.value)}
              aria-label="Assign a tag to the selected contacts"
              className="w-auto min-w-[11rem]"
            >
              <option value="">Assign tag…</option>
              {tags.map((t) => (
                <option key={t.id} value={t.id}>{t.name}</option>
              ))}
            </Select>
          )}
          {canBulkDelete && (
            <Button
              variant="ghost"
              size="sm"
              disabled={bulkBusy}
              onClick={handleBulkDelete}
              className="text-destructive hover:bg-destructive/10 hover:text-destructive"
            >
              <Trash2 aria-hidden /> Delete
            </Button>
          )}
          <Button variant="ghost" size="sm" disabled={bulkBusy} onClick={clearSelection} className="text-muted-foreground">
            Clear selection
          </Button>
          {bulkNotice && (
            <span role="status" className="inline-flex items-center gap-1.5 text-sm text-muted-foreground">
              <TagIcon aria-hidden className="h-3.5 w-3.5" />
              {bulkNotice}
            </span>
          )}
        </div>
      )}

      {bulkError != null && (
        <ErrorState
          compact
          title="Bulk action failed."
          error={bulkError}
          className="mb-3 max-w-xl"
        />
      )}

      {/* Records table. The error branch comes FIRST: a failed request must not
          fall through to "No deals yet", which is advice for a different
          situation entirely. */}
      {!loading && loadError ? (
        <ErrorState
          title={`Couldn't load ${schema.label_plural.toLowerCase()}.`}
          error={loadError}
          onRetry={fetchFirstPage}
        />
      ) : !loading && records.length === 0 ? (
        <EmptyState
          icon={Search}
          title={emptyMessage}
          description={
            hasActiveFilters
              ? 'Try adjusting or clearing your filters.'
              : canCreate
                ? `Click "Add ${schema.label}" to create one.`
                : undefined
          }
        />
      ) : (
      <TableShell>
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              {showSelection && (
                <TableHead className="w-10">
                  {/* Absent while the skeletons are up: a "select all 0" control
                      is not a thing anyone can mean. */}
                  {records.length > 0 && (
                    <input
                      type="checkbox"
                      className="h-4 w-4 cursor-pointer rounded border-input accent-primary"
                      checked={allLoadedSelected}
                      ref={(el) => {
                        if (el) el.indeterminate = selectedRecords.length > 0 && !allLoadedSelected;
                      }}
                      onChange={toggleAllLoaded}
                      aria-label={`Select all ${records.length} loaded ${schema.label_plural.toLowerCase()}`}
                    />
                  )}
                </TableHead>
              )}
              <TableHead className="w-16">#</TableHead>
              <TableHead>Name</TableHead>
              {columns.map((f) => (
                <TableHead key={f.key} aria-sort={ariaSortFor(f.key)}>
                  {sortHeader(f.key, f.label)}
                </TableHead>
              ))}
              <TableHead aria-sort={ariaSortFor('created_at')}>
                {sortHeader('created_at', 'Created')}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              Array.from({ length: 5 }).map((_, i) => (
                <TableRow key={i}>
                  {Array.from({ length: columns.length + (showSelection ? 4 : 3) }).map((_, j) => (
                    <TableCell key={j}>
                      <Skeleton className="h-4 w-full max-w-[10rem]" />
                    </TableCell>
                  ))}
                </TableRow>
              ))
            ) : (
              records.map((rec) => (
                <TableRow key={rec.id} data-clickable="true" onClick={() => openRecord(rec)}>
                  {showSelection && (
                    <TableCell onClick={(e) => e.stopPropagation()}>
                      <input
                        type="checkbox"
                        className="h-4 w-4 cursor-pointer rounded border-input accent-primary"
                        checked={selectedIds.has(rec.id)}
                        onChange={() => toggleRow(rec.id)}
                        aria-label={`Select ${rec.display || 'Untitled'}`}
                      />
                    </TableCell>
                  )}
                  <TableCell className="whitespace-nowrap text-xs font-medium text-muted-foreground">
                    {rec.number || '—'}
                  </TableCell>
                  <TableCell className="font-medium text-foreground">
                    {/* A real link so Cmd/Ctrl/middle-click opens the record in a
                        new tab (Salesforce-style); plain click is SPA navigation.
                        stopPropagation keeps the row's onClick from double-firing. */}
                    <Link
                      to={recordPath(slug, rec.id)}
                      onClick={(e) => e.stopPropagation()}
                      className="text-inherit no-underline"
                    >
                      {rec.display || 'Untitled'}
                    </Link>
                  </TableCell>
                  {columns.map((f) => (
                    <TableCell key={f.key} className="text-[13px]">
                      {formatFieldValue(f, rec.fields[f.key], relationLabel(f, rec.fields[f.key]))}
                    </TableCell>
                  ))}
                  <TableCell className="text-[13px] text-muted-foreground">
                    {new Date(rec.created_at).toLocaleDateString()}
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </TableShell>
      )}

      {/* Load more */}
      {nextCursor && (
        <div className="mt-4 flex flex-col items-center gap-2">
          {moreError != null && (
            <ErrorState
              compact
              title="Couldn't load more."
              error={moreError}
              className="max-w-xl"
            />
          )}
          <Button variant="outline" size="sm" onClick={loadMore} disabled={loadingMore}>
            {loadingMore ? 'Loading...' : moreError ? 'Try again' : 'Load more'}
          </Button>
        </div>
      )}

      {!loadError && (
        <p className="mt-3 text-center text-xs text-muted-foreground">
          Showing {records.length} {schema.label_plural.toLowerCase()}
        </p>
      )}
      </>
      )}

      {/* Create modal — the shared Radix Modal (focus trap, Escape, restore).
          ObjectForm owns its chrome (title bar + footer), so the modal header
          and body padding are turned off. */}
      <Modal
        open={panel?.mode === 'create'}
        onClose={closePanel}
        title={`New ${schema.label}`}
        hideHeader
        padded={false}
      >
        <ObjectForm schema={schema} onSaved={handleSaved} onCancel={closePanel} />
      </Modal>

      {confirmDialog}
    </div>
  );
}
