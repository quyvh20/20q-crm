import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { ArrowLeft, Users, Search, X, Plus, AlertCircle } from 'lucide-react';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import { useToast } from '@/lib/useToast';
import {
  Badge, Button, EmptyState, Input, PageHeader, SpinnerBlock,
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow, TableShell,
} from '@/components/ui';
import { getTags, getContacts, type Tag, type Contact } from '../../lib/api';
import SegmentBuilder from './SegmentBuilder';
import {
  useSegment, useUpdateSegment, useSegmentFields, useSegmentCount,
  useAddStaticMembers, useRemoveStaticMember, segmentKeys,
} from './segmentsQueries';
import {
  previewDefinition, previewSegment,
  type Segment, type SegmentFilter, type SegmentContactRow,
} from './segmentsApi';

const SegmentEditorPage: React.FC = () => {
  const { can, loaded } = usePermissions();
  const { id } = useParams<{ id: string }>();
  const { data: seg, isLoading, isError } = useSegment(id);

  if (!loaded) return <div className="mx-auto w-full max-w-4xl"><SpinnerBlock label="Loading…" /></div>;
  if (!can('marketing.manage')) {
    return <div className="mx-auto w-full max-w-4xl"><AccessDeniedPanel capability="marketing.manage" what="audiences" /></div>;
  }
  if (isLoading) return <div className="mx-auto w-full max-w-4xl"><SpinnerBlock label="Loading…" /></div>;
  if (isError || !seg) {
    return <div className="mx-auto w-full max-w-4xl"><EmptyState icon={AlertCircle} title="Audience not found" description="It may have been deleted." /></div>;
  }
  // Key by id so navigating between two segments remounts the editor — otherwise
  // the name/AST useState seeds (taken once at mount) would stay on the old segment.
  return seg.type === 'dynamic'
    ? <DynamicEditor key={seg.id} seg={seg} />
    : <StaticEditor key={seg.id} seg={seg} />;
};

// ── shared chrome ──────────────────────────────────────────────────────────────

function BackLink() {
  const navigate = useNavigate();
  return (
    <button onClick={() => navigate('/marketing/audiences')}
      className="mb-3 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
      <ArrowLeft aria-hidden className="h-4 w-4" /> Audiences
    </button>
  );
}

// ── dynamic segment editor ───────────────────────────────────────────────────

function DynamicEditor({ seg }: { seg: Segment }) {
  const toast = useToast();
  const update = useUpdateSegment();
  const { data: fields = [], isLoading: fieldsLoading } = useSegmentFields();
  const { data: tags = [] } = useQuery<Tag[]>({ queryKey: ['tags'], queryFn: getTags, staleTime: 5 * 60_000 });

  const [name, setName] = useState(seg.name);
  // ast + runnable move together so the preview's enabled gate always matches the
  // exact definition being counted (never dry-runs an incomplete leaf).
  const [current, setCurrent] = useState<{ ast: SegmentFilter; runnable: boolean }>({
    ast: seg.definition ?? {}, runnable: true,
  });

  // Debounce the whole {ast, runnable} pair so we don't POST on every keystroke.
  const [debounced, setDebounced] = useState(current);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(current), 400);
    return () => clearTimeout(t);
  }, [current]);

  const preview = useQuery({
    queryKey: ['segment-live-preview', JSON.stringify(debounced.ast)],
    queryFn: () => previewDefinition(debounced.ast, 25),
    enabled: debounced.runnable,
    retry: false,
    staleTime: 10_000,
  });

  const handleSave = () => {
    if (!name.trim()) { toast.error('Name is required'); return; }
    update.mutate(
      { id: seg.id, input: { name: name.trim(), type: 'dynamic', definition: current.ast } },
      {
        onSuccess: () => toast.show('Audience saved'),
        onError: (e) => toast.error((e as Error).message || 'Failed to save'),
      },
    );
  };

  return (
    <div className="mx-auto w-full max-w-4xl">
      <BackLink />
      <PageHeader
        title="Edit segment"
        description="A dynamic segment always reflects who matches right now. Preview and counts respect your own record access — you only ever see contacts you're allowed to."
        actions={<Button onClick={handleSave} disabled={update.isPending || !!seg.definition_restricted}>Save</Button>}
      />

      <div className="mb-4">
        <label className="mb-1 block text-xs font-medium text-muted-foreground" htmlFor="seg-name">Name</label>
        <Input id="seg-name" value={name} onChange={(e) => setName(e.target.value)} className="max-w-md" />
      </div>

      {seg.definition_restricted ? (
        <div className="rounded-xl border border-border bg-muted/40 p-4 text-sm text-muted-foreground">
          This segment filters on one or more fields you don't have access to view, so its
          definition is hidden and can't be edited here. Ask an admin who can see those fields.
        </div>
      ) : (
        <>
          <div className="rounded-xl border border-border bg-card p-4">
            <div className="mb-3 flex items-center justify-between">
              <h3 className="text-sm font-semibold text-foreground">Who's in this segment</h3>
              <LiveCount preview={preview} runnable={debounced.runnable} />
            </div>
            {fieldsLoading ? (
              <SpinnerBlock label="Loading fields…" />
            ) : (
              <SegmentBuilder
                fields={fields}
                tags={tags}
                initial={seg.definition}
                onChange={(nextAst, isRun) => setCurrent({ ast: nextAst, runnable: isRun })}
              />
            )}
          </div>

          <SamplePreview rows={preview.data?.contacts ?? []} loading={preview.isFetching} error={preview.isError} />
        </>
      )}
    </div>
  );
}

function LiveCount({ preview, runnable }: { preview: ReturnType<typeof useQuery<any>>; runnable: boolean }) {
  if (!runnable) return <Badge variant="outline">finish the filter…</Badge>;
  if (preview.isFetching && preview.data === undefined) return <Badge variant="outline">counting…</Badge>;
  if (preview.isError) return <Badge variant="destructive">preview failed</Badge>;
  const n = (preview.data as { count?: number } | undefined)?.count ?? 0;
  return <Badge variant="secondary" className="gap-1"><Users aria-hidden className="h-3 w-3" />{n.toLocaleString()} {n === 1 ? 'contact' : 'contacts'}</Badge>;
}

function SamplePreview({ rows, loading, error }: { rows: SegmentContactRow[]; loading: boolean; error: boolean }) {
  if (error) return null;
  return (
    <div className="mt-4">
      <p className="mb-2 text-xs font-medium text-muted-foreground">Sample {loading ? '' : `(${rows.length})`}</p>
      {rows.length === 0 ? (
        <p className="text-sm text-muted-foreground">{loading ? 'Loading…' : 'No matching contacts.'}</p>
      ) : (
        <TableShell>
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent"><TableHead>Name</TableHead><TableHead>Email</TableHead></TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((r) => (
                <TableRow key={r.id}>
                  <TableCell className="font-medium text-foreground">{r.name || '—'}</TableCell>
                  <TableCell className="text-muted-foreground">{r.email || '—'}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableShell>
      )}
    </div>
  );
}

// ── static list editor ─────────────────────────────────────────────────────────

function StaticEditor({ seg }: { seg: Segment }) {
  const toast = useToast();
  const update = useUpdateSegment();
  const add = useAddStaticMembers(seg.id);
  const remove = useRemoveStaticMember(seg.id);
  const { data: count } = useSegmentCount(seg.id);

  const [name, setName] = useState(seg.name);
  // Ids added this session, so the "already added" label is correct even for members
  // beyond the 200-row preview cap (the preview only reflects the first page).
  const [addedIds, setAddedIds] = useState<Set<string>>(new Set());
  const members = useQuery({
    queryKey: segmentKeys.members(seg.id),
    queryFn: () => previewSegment(seg.id, 200),
    staleTime: 10_000,
  });

  const handleSaveName = () => {
    if (!name.trim()) { toast.error('Name is required'); return; }
    update.mutate(
      { id: seg.id, input: { name: name.trim(), type: 'static' } },
      { onSuccess: () => toast.show('Saved'), onError: (e) => toast.error((e as Error).message || 'Failed to save') },
    );
  };

  const existingIds = useMemo(() => {
    const s = new Set((members.data?.contacts ?? []).map((c) => c.id));
    for (const id of addedIds) s.add(id);
    return s;
  }, [members.data, addedIds]);

  return (
    <div className="mx-auto w-full max-w-4xl">
      <BackLink />
      <PageHeader
        title="Edit static list"
        description="A static list is an explicit set of contacts. Adding someone never changes their consent or suppression status — a suppressed contact still won't receive marketing email."
        actions={<Button onClick={handleSaveName} disabled={update.isPending}>Save name</Button>}
      />

      <div className="mb-4">
        <label className="mb-1 block text-xs font-medium text-muted-foreground" htmlFor="seg-name">Name</label>
        <Input id="seg-name" value={name} onChange={(e) => setName(e.target.value)} className="max-w-md" />
      </div>

      <AddMemberSearch
        existingIds={existingIds}
        onAdd={(contactId, label) => add.mutate([contactId], {
          onSuccess: (n) => {
            setAddedIds((prev) => new Set(prev).add(contactId));
            toast.show(n > 0 ? `Added ${label}` : `${label} is already on the list`);
          },
          onError: (e) => toast.error((e as Error).message || 'Failed to add'),
        })}
      />

      <div className="mt-4">
        <div className="mb-2 flex items-center justify-between">
          <h3 className="text-sm font-semibold text-foreground">Members</h3>
          <Badge variant="secondary" className="gap-1"><Users aria-hidden className="h-3 w-3" />{(count ?? 0).toLocaleString()}</Badge>
        </div>
        {members.isLoading ? (
          <SpinnerBlock label="Loading members…" />
        ) : (members.data?.contacts ?? []).length === 0 ? (
          <EmptyState icon={Users} title="No members yet" description="Search above to add contacts to this list." />
        ) : (
          <TableShell>
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead>Name</TableHead><TableHead>Email</TableHead><TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(members.data?.contacts ?? []).map((c) => (
                  <TableRow key={c.id}>
                    <TableCell className="font-medium text-foreground">{c.name || '—'}</TableCell>
                    <TableCell className="text-muted-foreground">{c.email || '—'}</TableCell>
                    <TableCell className="text-right">
                      <Button variant="ghost" size="sm" title="Remove from list"
                        className="text-destructive hover:text-destructive"
                        disabled={remove.isPending}
                        onClick={() => remove.mutate(c.id, {
                          onSuccess: () => toast.show('Removed'),
                          onError: (e) => toast.error((e as Error).message || 'Failed to remove'),
                        })}>
                        <X aria-hidden className="h-4 w-4" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableShell>
        )}
        {(members.data?.count ?? 0) > (members.data?.contacts.length ?? 0) && (
          <p className="mt-2 text-xs text-muted-foreground">
            Showing the first {members.data?.contacts.length} of {members.data?.count.toLocaleString()} members.
          </p>
        )}
      </div>
    </div>
  );
}

function AddMemberSearch({ existingIds, onAdd }: {
  existingIds: Set<string>;
  onAdd: (contactId: string, label: string) => void;
}) {
  const [raw, setRaw] = useState('');
  const [q, setQ] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const t = setTimeout(() => setQ(raw.trim()), 250);
    return () => clearTimeout(t);
  }, [raw]);

  const results = useQuery({
    queryKey: ['segment-member-search', q],
    queryFn: () => getContacts({ q, limit: 8 }),
    enabled: q.length >= 2,
    staleTime: 10_000,
    retry: false,
  });

  const contacts: Contact[] = results.data?.contacts ?? [];

  const label = (c: Contact) => [c.first_name, c.last_name].filter(Boolean).join(' ') || c.email || 'contact';

  return (
    <div className="rounded-xl border border-border bg-card p-4">
      <label className="mb-1 block text-xs font-medium text-muted-foreground" htmlFor="member-search">Add contacts</label>
      <div className="relative">
        <Search aria-hidden className="pointer-events-none absolute left-2 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          id="member-search" ref={inputRef} value={raw}
          onChange={(e) => setRaw(e.target.value)}
          placeholder="Search contacts by name or email…"
          className="pl-8"
        />
      </div>
      {q.length >= 2 && (
        <div className="mt-2 divide-y divide-border rounded-lg border border-border">
          {results.isLoading ? (
            <p className="px-3 py-2 text-sm text-muted-foreground">Searching…</p>
          ) : contacts.length === 0 ? (
            <p className="px-3 py-2 text-sm text-muted-foreground">No contacts match “{q}”.</p>
          ) : (
            contacts.map((c) => {
              const already = existingIds.has(c.id);
              return (
                <div key={c.id} className="flex items-center justify-between px-3 py-2 text-sm">
                  <span>
                    <span className="font-medium text-foreground">{label(c)}</span>
                    {c.email && <span className="ml-2 text-muted-foreground">{c.email}</span>}
                  </span>
                  <Button size="sm" variant={already ? 'ghost' : 'outline'} disabled={already}
                    onClick={() => onAdd(c.id, label(c))}>
                    {already ? 'Added' : <><Plus aria-hidden className="h-3 w-3" /> Add</>}
                  </Button>
                </div>
              );
            })
          )}
        </div>
      )}
    </div>
  );
}

export default SegmentEditorPage;
