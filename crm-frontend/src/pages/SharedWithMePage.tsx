import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Share2 } from 'lucide-react';
import { listSharedWithMe, type SharedRecordView } from '../lib/api';
import { recordPath } from '../features/objects/recordRoutes';
import { Badge } from '../components/ui/badge';
import { EmptyState } from '../components/ui/empty-state';
import { PageHeader } from '../components/ui/page-header';
import { Skeleton } from '../components/ui/skeleton';

// SharedWithMePage lists every record someone ELSE owns that reached the caller
// through a share (U6) — directly, through their role, or through a group.
// Grouped by object, so "3 deals and a contact" reads as such. Rows link to the
// record's own detail page, where the normal permission rules apply.
export default function SharedWithMePage() {
  const { data, isLoading, error } = useQuery({
    queryKey: ['shared-with-me'],
    queryFn: () => listSharedWithMe(),
  });

  const records = data?.records ?? [];

  // Preserve server order within each object group, and show the groups in the
  // order their first record arrived.
  const groups: { label: string; slug: string; rows: SharedRecordView[] }[] = [];
  for (const r of records) {
    let g = groups.find((x) => x.slug === r.object_slug);
    if (!g) {
      g = { label: r.object_label || r.object_slug, slug: r.object_slug, rows: [] };
      groups.push(g);
    }
    g.rows.push(r);
  }

  return (
    <div className="mx-auto w-full max-w-6xl space-y-8">
      <PageHeader
        className="mb-0"
        title="Shared with me"
        description="Records other people own that were shared with you — directly, through your role, or through one of your teams."
      />

      {isLoading ? (
        <Skeleton className="h-40 rounded-xl" />
      ) : error ? (
        <div className="rounded-xl border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">
          {error instanceof Error ? error.message : 'Failed to load shared records.'}
        </div>
      ) : records.length === 0 ? (
        <EmptyState icon={Share2} title="Nothing has been shared with you yet." />
      ) : (
        <div className="space-y-6">
          {groups.map((g) => (
            <section key={g.slug}>
              <h2 className="mb-3 text-sm font-semibold text-muted-foreground">{g.label}</h2>
              <div className="overflow-hidden rounded-xl border border-border">
                {g.rows.map((r) => (
                  <Link
                    key={`${r.object_slug}:${r.record_id}`}
                    to={recordPath(r.object_slug, r.record_id)}
                    className="flex items-center gap-3 border-b border-border px-4 py-3 last:border-0 hover:bg-accent"
                  >
                    <div className="min-w-0 flex-1">
                      <div className="truncate font-medium">{r.display || 'Untitled'}</div>
                      <div className="truncate text-xs text-muted-foreground">
                        Owned by {r.owner_name || 'someone else'}
                      </div>
                    </div>
                    {r.updated_at && (
                      <span className="hidden text-xs text-muted-foreground sm:block">
                        Updated {new Date(r.updated_at).toLocaleDateString()}
                      </span>
                    )}
                    {r.level === 'edit'
                      ? <Badge>Can edit</Badge>
                      : <Badge variant="secondary" className="text-muted-foreground">Can view</Badge>}
                  </Link>
                ))}
              </div>
            </section>
          ))}
        </div>
      )}
    </div>
  );
}
