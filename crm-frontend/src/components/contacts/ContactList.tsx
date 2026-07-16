import { useState, useMemo, useRef, useCallback } from 'react';
import {
  useReactTable,
  getCoreRowModel,
  flexRender,
  type ColumnDef,
} from '@tanstack/react-table';
import { useInfiniteQuery, useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Pencil, Trash2, Tag, ChevronDown, X, Check, Users } from 'lucide-react';
import { getContacts, deleteContact, bulkAction, getTags, type Contact, type ContactFilter } from '../../lib/api';
import { useConfirm } from '../common/ConfirmDialog';
import {
  Badge,
  Button,
  Spinner,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  TableShell,
} from '@/components/ui';

interface ContactListProps {
  filters: ContactFilter;
  onEdit: (contact: Contact) => void;
  onImport: () => void;
}

export default function ContactList({ filters, onEdit, onImport }: ContactListProps) {
  const queryClient = useQueryClient();
  const observerRef = useRef<IntersectionObserver | null>(null);

  // Bulk selection state
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [showTagDropdown, setShowTagDropdown] = useState(false);
  const [bulkFeedback, setBulkFeedback] = useState<string | null>(null);

  // U7: this list used to ship its own confirm modal — no Escape, no focus trap,
  // no aria, and a hardcoded red button. It now uses the app's shared dialog.
  const { confirm, dialog: confirmEl } = useConfirm();

  const {
    data,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
    isLoading,
  } = useInfiniteQuery({
    queryKey: ['contacts', filters],
    queryFn: async ({ pageParam }) => {
      return getContacts({ ...filters, cursor: pageParam, limit: 25 });
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) =>
      lastPage.meta.has_more ? lastPage.meta.next_cursor : undefined,
  });

  const { data: allTags = [] } = useQuery({ queryKey: ['tags'], queryFn: getTags });

  const deleteMutation = useMutation({
    mutationFn: deleteContact,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['contacts'] });
    },
  });

  const bulkMutation = useMutation({
    mutationFn: ({ action, tagId }: { action: 'delete' | 'assign_tag'; tagId?: string }) =>
      bulkAction(action, Array.from(selectedIds), tagId),
    onSuccess: (result) => {
      setBulkFeedback(result.message);
      setSelectedIds(new Set());
      setShowTagDropdown(false);
      queryClient.invalidateQueries({ queryKey: ['contacts'] });
      setTimeout(() => setBulkFeedback(null), 3000);
    },
    onError: (err: Error) => {
      alert(`Bulk action failed: ${err.message}`);
    },
  });

  const contacts = useMemo(
    () => data?.pages.flatMap((p) => p.contacts) ?? [],
    [data]
  );

  const totalCount = data?.pages[0]?.meta.total ?? 0;
  const allVisibleSelected = contacts.length > 0 && contacts.every(c => selectedIds.has(c.id));
  const someSelected = selectedIds.size > 0 && !allVisibleSelected;

  const toggleAll = () => {
    if (allVisibleSelected) {
      setSelectedIds(new Set());
    } else {
      setSelectedIds(new Set(contacts.map(c => c.id)));
    }
  };

  const toggleOne = (id: string) => {
    setSelectedIds(prev => {
      const s = new Set(prev);
      s.has(id) ? s.delete(id) : s.add(id);
      return s;
    });
  };

  // Infinite scroll observer
  const lastElementRef = useCallback(
    (node: HTMLDivElement | null) => {
      if (isFetchingNextPage) return;
      if (observerRef.current) observerRef.current.disconnect();
      observerRef.current = new IntersectionObserver((entries) => {
        if (entries[0].isIntersecting && hasNextPage) {
          fetchNextPage();
        }
      });
      if (node) observerRef.current.observe(node);
    },
    [isFetchingNextPage, fetchNextPage, hasNextPage]
  );

  const columns: ColumnDef<Contact>[] = useMemo(
    () => [
      {
        id: 'select',
        header: () => (
          <input
            id="select-all-contacts"
            type="checkbox"
            checked={allVisibleSelected}
            ref={(el) => { if (el) el.indeterminate = someSelected; }}
            onChange={toggleAll}
            onClick={(e) => e.stopPropagation()}
            className="h-4 w-4 rounded border-muted-foreground/30 accent-primary cursor-pointer"
          />
        ),
        cell: ({ row }) => (
          <input
            id={`select-contact-${row.original.id}`}
            type="checkbox"
            checked={selectedIds.has(row.original.id)}
            onChange={() => toggleOne(row.original.id)}
            onClick={(e) => e.stopPropagation()}
            className="h-4 w-4 rounded border-muted-foreground/30 accent-primary cursor-pointer"
          />
        ),
      },
      {
        header: 'Name',
        accessorFn: (row) => `${row.first_name} ${row.last_name}`,
        cell: ({ row }) => (
          <div className="flex items-center gap-3">
            <div className="h-8 w-8 rounded-full bg-primary flex items-center justify-center text-primary-foreground text-xs font-semibold shrink-0">
              {row.original.first_name[0]}{row.original.last_name?.[0] || ''}
            </div>
            <div className="min-w-0">
              <p className="font-medium truncate">{row.original.first_name} {row.original.last_name}</p>
            </div>
          </div>
        ),
      },
      {
        header: 'Email',
        accessorKey: 'email',
        cell: ({ getValue }) => (
          <span className="text-muted-foreground text-sm">{getValue() as string || '—'}</span>
        ),
      },
      {
        header: 'Phone',
        accessorKey: 'phone',
        cell: ({ getValue }) => (
          <span className="text-muted-foreground text-sm">{getValue() as string || '—'}</span>
        ),
      },
      {
        header: 'Company',
        accessorFn: (row) => row.company?.name,
        cell: ({ getValue }) => (
          <span className="text-sm">{getValue() as string || '—'}</span>
        ),
      },
      {
        header: 'Tags',
        cell: ({ row }) => (
          <div className="flex flex-wrap gap-1">
            {row.original.tags?.map((tag) => (
              <span
                key={tag.id}
                className="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium"
                style={{ backgroundColor: tag.color + '20', color: tag.color }}
              >
                {tag.name}
              </span>
            ))}
          </div>
        ),
      },
      {
        header: 'Created',
        accessorKey: 'created_at',
        cell: ({ getValue }) => (
          <span className="text-muted-foreground text-sm">
            {new Date(getValue() as string).toLocaleDateString()}
          </span>
        ),
      },
      {
        header: '',
        id: 'actions',
        // focus-within keeps these reachable for keyboard users: the hover-only
        // reveal left Edit/Delete invisible while focused (U7 a11y).
        cell: ({ row }) => (
          <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 focus-within:opacity-100 transition-opacity">
            <button
              onClick={(e) => { e.stopPropagation(); onEdit(row.original); }}
              className="p-1.5 rounded-md hover:bg-accent text-muted-foreground hover:text-foreground transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              title="Edit"
              aria-label={`Edit ${row.original.first_name} ${row.original.last_name}`}
            >
              <Pencil aria-hidden className="h-3.5 w-3.5" />
            </button>
            <button
              onClick={async (e) => {
                e.stopPropagation();
                const ok = await confirm({
                  title: 'Delete Contact',
                  body: `Are you sure you want to delete ${row.original.first_name} ${row.original.last_name}?`,
                  confirmLabel: 'Delete',
                });
                if (ok) deleteMutation.mutate(row.original.id);
              }}
              className="p-1.5 rounded-md hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              title="Delete"
              aria-label={`Delete ${row.original.first_name} ${row.original.last_name}`}
            >
              <Trash2 aria-hidden className="h-3.5 w-3.5" />
            </button>
          </div>
        ),
      },
    ],
    [onEdit, deleteMutation, confirm, selectedIds, allVisibleSelected, someSelected]
  );

  const table = useReactTable({
    data: contacts,
    columns,
    getCoreRowModel: getCoreRowModel(),
  });

  if (isLoading) {
    return (
      <div className="space-y-3">
        {[...Array(8)].map((_, i) => (
          <div key={i} className="h-14 rounded-lg bg-muted/50 animate-pulse" />
        ))}
      </div>
    );
  }

  return (
    <div>
      {/* Bulk Action Toolbar */}
      {selectedIds.size > 0 && (
        <div
          id="bulk-action-toolbar"
          className="mb-3 flex items-center gap-3 px-4 py-2.5 rounded-xl bg-primary text-primary-foreground shadow-lg shadow-primary/20 animate-in slide-in-from-top-2 duration-200"
        >
          <span className="text-sm font-semibold">
            {selectedIds.size} selected
          </span>
          <div className="h-4 w-px bg-primary-foreground/30" />

          {/* Assign Tag dropdown trigger */}
          <div className="relative">
            <button
              id="bulk-assign-tag-btn"
              onClick={() => setShowTagDropdown(v => !v)}
              disabled={bulkMutation.isPending}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-primary-foreground/15 hover:bg-primary-foreground/25 transition-colors text-sm font-medium disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-foreground/50"
            >
              <Tag aria-hidden className="h-3.5 w-3.5" />
              Assign Tag
              <ChevronDown aria-hidden className="h-3 w-3" />
            </button>
            {showTagDropdown && (
              <div className="absolute top-full left-0 mt-1 w-48 rounded-xl bg-popover border shadow-xl z-50 overflow-hidden py-1 text-foreground">
                {allTags.length === 0 && (
                  <p className="px-3 py-2 text-xs text-muted-foreground">No tags available</p>
                )}
                {allTags.map(tag => (
                  <button
                    key={tag.id}
                    id={`bulk-assign-tag-${tag.id}`}
                    onClick={() => bulkMutation.mutate({ action: 'assign_tag', tagId: tag.id })}
                    className="w-full flex items-center gap-2 px-3 py-2 text-sm hover:bg-muted transition-colors text-left"
                  >
                    <span
                      className="inline-block w-2 h-2 rounded-full"
                      style={{ backgroundColor: tag.color }}
                    />
                    {tag.name}
                  </button>
                ))}
              </div>
            )}
          </div>

          {/* Bulk Delete */}
          <button
            id="bulk-delete-btn"
            onClick={async () => {
              const ok = await confirm({
                title: 'Delete Contacts',
                body: `Are you sure you want to delete ${selectedIds.size} contact(s)? This action cannot be undone.`,
                confirmLabel: 'Delete',
              });
              if (ok) bulkMutation.mutate({ action: 'delete' });
            }}
            disabled={bulkMutation.isPending}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-destructive text-destructive-foreground hover:bg-destructive/90 transition-colors text-sm font-medium disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-foreground/50"
          >
            <Trash2 aria-hidden className="h-3.5 w-3.5" />
            Delete {selectedIds.size}
          </button>

          {/* Clear selection */}
          <button
            onClick={() => setSelectedIds(new Set())}
            className="ml-auto flex items-center gap-1 px-2 py-1 rounded-lg hover:bg-primary-foreground/15 transition-colors text-xs opacity-75 hover:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-foreground/50"
          >
            <X aria-hidden className="h-3 w-3" /> Clear
          </button>
        </div>
      )}

      {/* Success feedback */}
      {bulkFeedback && (
        <div className="mb-3">
          <Badge variant="success" className="px-3 py-1">
            <Check aria-hidden className="h-3.5 w-3.5" /> {bulkFeedback}
          </Badge>
        </div>
      )}

      <TableShell>
        <Table>
          <TableHeader>
            {table.getHeaderGroups().map((hg) => (
              <TableRow key={hg.id} className="hover:bg-transparent">
                {hg.headers.map((header) => (
                  <TableHead key={header.id} className="px-4 py-3">
                    {flexRender(header.column.columnDef.header, header.getContext())}
                  </TableHead>
                ))}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {table.getRowModel().rows.length === 0 ? (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={columns.length} className="px-4 py-12 text-center text-muted-foreground">
                  <div className="flex flex-col items-center gap-3">
                    <Users aria-hidden className="h-12 w-12 text-muted-foreground/40" strokeWidth={1} />
                    <p className="text-sm">No contacts found</p>
                    <Button variant="link" onClick={onImport} className="h-auto p-0">
                      Import contacts from CSV
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              table.getRowModel().rows.map((row, i) => (
                <TableRow
                  key={row.id}
                  data-clickable="true"
                  onClick={() => onEdit(row.original)}
                  className={`group ${
                    selectedIds.has(row.original.id) ? 'bg-primary/5' : ''
                  }`}
                  ref={i === table.getRowModel().rows.length - 1 ? lastElementRef : null}
                >
                  {row.getVisibleCells().map((cell) => (
                    <TableCell key={cell.id} className="px-4 py-3">
                      {flexRender(cell.column.columnDef.cell, cell.getContext())}
                    </TableCell>
                  ))}
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </TableShell>

      {isFetchingNextPage && (
        <div className="flex justify-center py-4">
          <Spinner />
        </div>
      )}

      <div className="mt-3 text-xs text-muted-foreground text-center">
        Showing {contacts.length} of {totalCount} contacts
        {selectedIds.size > 0 && (
          <span className="ml-2 text-primary">&bull; {selectedIds.size} selected</span>
        )}
      </div>

      {confirmEl}
    </div>
  );
}
