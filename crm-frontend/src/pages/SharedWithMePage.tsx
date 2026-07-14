import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { listSharedWithMe, type SharedRecordView } from '../lib/api';
import { recordPath } from '../features/objects/recordRoutes';

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
    <div className="mx-auto max-w-5xl space-y-8">
      <div>
        <h1 className="text-2xl font-bold">Shared with me</h1>
        <p className="text-sm text-muted-foreground">
          Records other people own that were shared with you — directly, through your role, or through one of your teams.
        </p>
      </div>

      {isLoading ? (
        <div className="h-40 animate-pulse rounded-xl bg-muted/50" />
      ) : error ? (
        <div className="rounded-xl border border-red-500/30 bg-red-500/10 px-4 py-3 text-sm text-red-600 dark:text-red-400">
          {error instanceof Error ? error.message : 'Failed to load shared records.'}
        </div>
      ) : records.length === 0 ? (
        <div className="rounded-xl border border-dashed p-8 text-center text-sm text-muted-foreground">
          Nothing has been shared with you yet.
        </div>
      ) : (
        <div className="space-y-6">
          {groups.map((g) => (
            <section key={g.slug}>
              <h2 className="mb-3 text-sm font-semibold text-muted-foreground">{g.label}</h2>
              <div className="overflow-hidden rounded-xl border">
                {g.rows.map((r) => (
                  <Link
                    key={`${r.object_slug}:${r.record_id}`}
                    to={recordPath(r.object_slug, r.record_id)}
                    className="flex items-center gap-3 border-b px-4 py-3 last:border-0 hover:bg-accent"
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
                    <span
                      className={`rounded-full px-2 py-0.5 text-xs ${
                        r.level === 'edit'
                          ? 'bg-primary/10 text-primary'
                          : 'bg-muted text-muted-foreground'
                      }`}
                    >
                      {r.level === 'edit' ? 'Can edit' : 'Can view'}
                    </span>
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
