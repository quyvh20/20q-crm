import { useMemo, useRef, useCallback } from 'react';
import {
  useReactTable,
  getCoreRowModel,
  flexRender,
  type ColumnDef,
} from '@tanstack/react-table';
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { getContacts, deleteContact, type Contact } from '../../lib/api';

interface ContactListProps {
  onEdit: (contact: Contact) => void;
  onImport: () => void;
  searchQuery: string;
}

export default function ContactList({ onEdit, onImport, searchQuery }: ContactListProps) {
  const queryClient = useQueryClient();
  const observerRef = useRef<IntersectionObserver | null>(null);

  const {
    data,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
    isLoading,
  } = useInfiniteQuery({
    queryKey: ['contacts', searchQuery],
    queryFn: async ({ pageParam }) => {
      return getContacts({ q: searchQuery || undefined, cursor: pageParam, limit: 25 });
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) =>
      lastPage.meta.has_more ? lastPage.meta.next_cursor : undefined,
  });

  const deleteMutation = useMutation({
    mutationFn: deleteContact,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['contacts'] }),
  });

  const contacts = useMemo(
    () => data?.pages.flatMap((p) => p.contacts) ?? [],
    [data]
  );

  const totalCount = data?.pages[0]?.meta.total ?? 0;

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
    [onEdit, deleteMutation]
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
                  className="border-b last:border-b-0 hover:bg-muted/20 cursor-pointer transition-colors group"
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
      </div>
    </div>
  );
}
