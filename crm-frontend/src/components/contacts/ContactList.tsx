import { useState, useMemo, useRef, useCallback } from 'react';
import {
  useReactTable,
  getCoreRowModel,
  flexRender,
  type ColumnDef,
} from '@tanstack/react-table';
import { useInfiniteQuery, useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { getContacts, deleteContact, bulkAction, getTags, type Contact, type ContactFilter } from '../../lib/api';

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
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['contacts'] }),
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
            className="h-4 w-4 rounded border-muted-foreground/30 accent-blue-500 cursor-pointer"
          />
        ),
        cell: ({ row }) => (
          <input
            id={`select-contact-${row.original.id}`}
            type="checkbox"
            checked={selectedIds.has(row.original.id)}
            onChange={() => toggleOne(row.original.id)}
            onClick={(e) => e.stopPropagation()}
            className="h-4 w-4 rounded border-muted-foreground/30 accent-blue-500 cursor-pointer"
          />
        ),
      },
      {
        header: 'Name',
        accessorFn: (row) => `${row.first_name} ${row.last_name}`,
        cell: ({ row }) => (
          <div className="flex items-center gap-3">
            <div className="h-8 w-8 rounded-full bg-gradient-to-br from-blue-500 to-purple-600 flex items-center justify-center text-white text-xs font-semibold shrink-0">
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
        cell: ({ row }) => (
          <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
            <button
              onClick={(e) => { e.stopPropagation(); onEdit(row.original); }}
              className="p-1.5 rounded-md hover:bg-accent text-muted-foreground hover:text-foreground transition-colors"
              title="Edit"
            >
              <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z"/></svg>
            </button>
            <button
              onClick={(e) => {
                e.stopPropagation();
                if (confirm('Delete this contact?')) deleteMutation.mutate(row.original.id);
              }}
              className="p-1.5 rounded-md hover:bg-red-500/10 text-muted-foreground hover:text-red-500 transition-colors"
              title="Delete"
            >
              <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M3 6h18"/><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"/><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/></svg>
            </button>
          </div>
        ),
      },
    ],
    [onEdit, deleteMutation, selectedIds, allVisibleSelected, someSelected]
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
          className="mb-3 flex items-center gap-3 px-4 py-2.5 rounded-xl bg-blue-600 text-white shadow-lg shadow-blue-600/20 animate-in slide-in-from-top-2 duration-200"
        >
          <span className="text-sm font-semibold">
            {selectedIds.size} selected
          </span>
          <div className="h-4 w-px bg-white/30" />

          {/* Assign Tag dropdown trigger */}
          <div className="relative">
            <button
              id="bulk-assign-tag-btn"
              onClick={() => setShowTagDropdown(v => !v)}
              disabled={bulkMutation.isPending}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-white/15 hover:bg-white/25 transition-colors text-sm font-medium disabled:opacity-50"
            >
              <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M12 2H2v10l9.29 9.29c.94.94 2.48.94 3.42 0l6.58-6.58c.94-.94.94-2.48 0-3.42L12 2Z"/><path d="M7 7h.01"/></svg>
              Assign Tag
              <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="m6 9 6 6 6-6"/></svg>
            </button>
            {showTagDropdown && (
              <div className="absolute top-full left-0 mt-1 w-48 rounded-xl bg-popover border shadow-xl z-50 overflow-hidden py-1">
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
            onClick={() => {
              if (confirm(`Delete ${selectedIds.size} contact(s)? This cannot be undone.`)) {
                bulkMutation.mutate({ action: 'delete' });
              }
            }}
            disabled={bulkMutation.isPending}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-red-500/80 hover:bg-red-500 transition-colors text-sm font-medium disabled:opacity-50"
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M3 6h18"/><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"/><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/></svg>
            Delete {selectedIds.size}
          </button>

          {/* Clear selection */}
          <button
            onClick={() => setSelectedIds(new Set())}
            className="ml-auto px-2 py-1 rounded-lg hover:bg-white/15 transition-colors text-xs opacity-75 hover:opacity-100"
          >
            ✕ Clear
          </button>
        </div>
      )}

      {/* Success feedback */}
      {bulkFeedback && (
        <div className="mb-3 px-4 py-2 rounded-lg bg-emerald-500/10 border border-emerald-500/20 text-sm text-emerald-600 font-medium">
          ✓ {bulkFeedback}
        </div>
      )}

      <div className="rounded-xl border bg-card overflow-hidden">
        <table className="w-full">
          <thead>
            {table.getHeaderGroups().map((hg) => (
              <tr key={hg.id} className="border-b bg-muted/30">
                {hg.headers.map((header) => (
                  <th key={header.id} className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                    {flexRender(header.column.columnDef.header, header.getContext())}
                  </th>
                ))}
              </tr>
            ))}
          </thead>
          <tbody>
            {table.getRowModel().rows.length === 0 ? (
              <tr>
                <td colSpan={columns.length} className="px-4 py-12 text-center text-muted-foreground">
                  <div className="flex flex-col items-center gap-3">
                    <svg xmlns="http://www.w3.org/2000/svg" width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1" strokeLinecap="round" strokeLinejoin="round" className="text-muted-foreground/40"><path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg>
                    <p className="text-sm">No contacts found</p>
                    <button
                      onClick={onImport}
                      className="text-sm text-blue-500 hover:text-blue-400 underline underline-offset-4"
                    >
                      Import contacts from CSV
                    </button>
                  </div>
                </td>
              </tr>
            ) : (
              table.getRowModel().rows.map((row, i) => (
                <tr
                  key={row.id}
                  onClick={() => onEdit(row.original)}
                  className={`border-b last:border-b-0 hover:bg-muted/20 cursor-pointer transition-colors group ${
                    selectedIds.has(row.original.id) ? 'bg-blue-500/5' : ''
                  }`}
                  ref={i === table.getRowModel().rows.length - 1 ? lastElementRef : null}
                >
                  {row.getVisibleCells().map((cell) => (
                    <td key={cell.id} className="px-4 py-3">
                      {flexRender(cell.column.columnDef.cell, cell.getContext())}
                    </td>
                  ))}
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {isFetchingNextPage && (
        <div className="flex justify-center py-4">
          <div className="animate-spin h-5 w-5 border-2 border-blue-500 border-t-transparent rounded-full" />
        </div>
      )}

      <div className="mt-3 text-xs text-muted-foreground text-center">
        Showing {contacts.length} of {totalCount} contacts
        {selectedIds.size > 0 && (
          <span className="ml-2 text-blue-500">&bull; {selectedIds.size} selected</span>
        )}
      </div>
    </div>
  );
}
