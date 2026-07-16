import { useState, useEffect, useCallback, useMemo } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { Check, LayoutGrid, List, Plus, Search, Sparkles, Upload, X } from 'lucide-react';
import {
  getObjectSchema,
  listObjectRecordsUnified,
  getTags,
  getStages,
  type ObjectSchema,
  type ObjectFieldDescriptor,
  type UniformRecord,
  type Tag,
} from '../../lib/api';
import { formatFieldValue } from './fieldHelpers';
import ObjectForm from './ObjectForm';
import ObjectKanban from './ObjectKanban';
import ImportModal from '../../components/contacts/ImportModal';
import Modal from '../../components/common/Modal';
import {
  Button,
  EmptyState,
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
  const canCreate = canAccess(slug, 'create');
  const openRecord = useCallback(
    (rec: UniformRecord) => navigate(recordPath(slug, rec.id)),
    [navigate, slug],
  );
  const [schema, setSchema] = useState<ObjectSchema | null>(null);
  const [records, setRecords] = useState<UniformRecord[]>([]);
  const [nextCursor, setNextCursor] = useState<string | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [search, setSearch] = useState('');
  const [debouncedSearch, setDebouncedSearch] = useState('');
  const [panel, setPanel] = useState<Panel>(null);
  const [showImport, setShowImport] = useState(false);

  // CSV import is a contact-specific affordance (the bulk importer is contact-aware).
  const supportsImport = slug === 'contact';

  // Filters (parity with the legacy pages): relation field key → selected id,
  // tag ids (any-match), and a semantic toggle.
  const [filters, setFilters] = useState<Record<string, string>>({});
  const [tagIds, setTagIds] = useState<string[]>([]);
  const [semantic, setSemantic] = useState(false);
  const [relationOptions, setRelationOptions] = useState<Record<string, RelationOption[]>>({});
  const [tags, setTags] = useState<Tag[]>([]);

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
  const [view, setView] = useState<'table' | 'board'>('table');

  // Reset transient state when switching objects.
  useEffect(() => {
    setSchema(null);
    setSearch('');
    setDebouncedSearch('');
    setPanel(null);
    setFilters({});
    setTagIds([]);
    setSemantic(false);
    setRelationOptions({});
    setView('table');
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
  useEffect(() => {
    let cancelled = false;
    relationFields.forEach((f) => {
      listObjectRecordsUnified(f.target_slug as string, { limit: 200 })
        .then((page) => {
          if (cancelled) return;
          setRelationOptions((prev) => ({
            ...prev,
            [f.key]: page.records.map((r) => ({ id: r.id, label: r.display || r.id })),
          }));
        })
        .catch(() => {
          /* a relation we can't enumerate is simply not filterable */
        });
    });
    return () => {
      cancelled = true;
    };
  }, [relationFields]);

  // A "stage" relation has no registry target (pipeline_stages isn't a registered
  // object), so its labels come from the pipeline stages. Loaded into the same
  // option map so relation cells resolve to the stage name, not its id.
  useEffect(() => {
    if (!stageField) return;
    let cancelled = false;
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

  // Debounce the search box.
  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(t);
  }, [search]);

  const listParams = useCallback(
    (cursor?: string) => ({
      limit: LIMIT,
      q: debouncedSearch,
      cursor,
      filters,
      tagIds,
      semantic: semantic && supportsSemantic,
    }),
    [debouncedSearch, filters, tagIds, semantic, supportsSemantic],
  );

  const fetchFirstPage = useCallback(async () => {
    setLoading(true);
    try {
      const page = await listObjectRecordsUnified(slug, listParams());
      setRecords(page.records);
      setNextCursor(page.next_cursor);
    } catch {
      setRecords([]);
      setNextCursor(undefined);
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
      setRecords((prev) => [...prev, ...page.records]);
      setNextCursor(page.next_cursor);
    } catch {
      /* keep what we have */
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
    setFilters((prev) => {
      const next = { ...prev };
      if (value) next[key] = value;
      else delete next[key];
      return next;
    });
  };

  const toggleTag = (id: string) => {
    setTagIds((prev) => (prev.includes(id) ? prev.filter((t) => t !== id) : [...prev, id]));
  };

  const clearFilters = () => {
    setFilters({});
    setTagIds([]);
    setSemantic(false);
    setSearch('');
  };

  const hasActiveFilters =
    Object.keys(filters).length > 0 || tagIds.length > 0 || semantic || !!search;

  if (!schema) {
    return <SpinnerBlock label="Loading…" />;
  }

  const columns = schema.fields.slice(0, MAX_COLUMNS);
  const emptyMessage = hasActiveFilters
    ? `No ${schema.label_plural.toLowerCase()} match your filters.`
    : canCreate
      ? `No ${schema.label_plural.toLowerCase()} yet.`
      : `No ${schema.label_plural.toLowerCase()} to show.`;

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
            {stageField && (
              <div className="inline-flex items-center rounded-lg border border-input bg-background p-0.5 shadow-sm">
                {(['table', 'board'] as const).map((v) => (
                  <button
                    key={v}
                    onClick={() => setView(v)}
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
            {canCreate && (
              <Button onClick={() => setPanel({ mode: 'create' })}>
                <Plus aria-hidden /> Add {schema.label}
              </Button>
            )}
          </>
        }
      />

      {showImport && (
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
          onCardClick={openRecord}
        />
      ) : (
      <>
      {/* Search + filters */}
      <div className="mb-4 flex flex-wrap items-center gap-2.5">
        <div className="relative w-72 max-w-full">
          <Search aria-hidden className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={semantic && supportsSemantic ? `Describe the ${schema.label.toLowerCase()} you want…` : `Search ${schema.label_plural.toLowerCase()}...`}
            className="pl-9"
          />
        </div>

        {supportsSemantic && (
          <Button
            variant="outline"
            onClick={() => setSemantic((v) => !v)}
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

      {/* Records table */}
      {!loading && records.length === 0 ? (
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
              <TableHead className="w-16">#</TableHead>
              <TableHead>Name</TableHead>
              {columns.map((f) => (
                <TableHead key={f.key}>{f.label}</TableHead>
              ))}
              <TableHead>Created</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              Array.from({ length: 5 }).map((_, i) => (
                <TableRow key={i}>
                  {Array.from({ length: columns.length + 3 }).map((_, j) => (
                    <TableCell key={j}>
                      <Skeleton className="h-4 w-full max-w-[10rem]" />
                    </TableCell>
                  ))}
                </TableRow>
              ))
            ) : (
              records.map((rec) => (
                <TableRow key={rec.id} data-clickable="true" onClick={() => openRecord(rec)}>
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
        <div className="mt-4 flex justify-center">
          <Button variant="outline" size="sm" onClick={loadMore} disabled={loadingMore}>
            {loadingMore ? 'Loading...' : 'Load more'}
          </Button>
        </div>
      )}

      <p className="mt-3 text-center text-xs text-muted-foreground">
        Showing {records.length} {schema.label_plural.toLowerCase()}
      </p>
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
    </div>
  );
}
