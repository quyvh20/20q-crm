import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Users2, GitMerge } from 'lucide-react';
import { listContactDuplicates, mergeContacts, type DuplicateGroup, type Contact } from '../../lib/api';
import { Button, EmptyState, Skeleton } from '@/components/ui';

// R8.3: duplicate detection + merge, contacts first. Candidates are grouped
// by shared phone digits (the only always-available signal today — exact
// email collisions can't exist among active contacts, and name-similarity
// via pg_trgm is a follow-up once R6.2 lands the extension). Merge picks one
// survivor per group; everything else in the group folds into it one pair at
// a time, so a 3-contact group takes two merges.
export default function DuplicatesSection() {
  const queryClient = useQueryClient();
  const { data: groups = [], isLoading, error } = useQuery({
    queryKey: ['contact-duplicates'],
    queryFn: listContactDuplicates,
  });

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold text-foreground">Duplicates</h2>
        <p className="text-sm text-muted-foreground mt-0.5">
          Contacts that share a phone number. Pick which one to keep — the others' tasks,
          activities, links and tags move onto it, and the rest are deleted.
        </p>
      </div>

      {isLoading ? (
        <Skeleton className="h-40 rounded-xl" />
      ) : error ? (
        <div className="rounded-xl border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">
          {error instanceof Error ? error.message : 'Failed to load duplicates.'}
        </div>
      ) : groups.length === 0 ? (
        <EmptyState icon={Users2} title="No duplicates found." description="Contacts sharing a phone number will show up here." />
      ) : (
        <div className="space-y-4">
          {groups.map((g) => (
            <DuplicateGroupCard
              key={g.key}
              group={g}
              onMerged={() => queryClient.invalidateQueries({ queryKey: ['contact-duplicates'] })}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function contactName(c: Contact): string {
  const name = `${c.first_name} ${c.last_name}`.trim();
  return name || c.email || 'Unnamed contact';
}

function DuplicateGroupCard({ group, onMerged }: { group: DuplicateGroup; onMerged: () => void }) {
  const [survivorId, setSurvivorId] = useState(group.contacts[0]?.id ?? '');
  const [error, setError] = useState<string | null>(null);

  const mergeMutation = useMutation({
    mutationFn: (loserId: string) => mergeContacts(survivorId, loserId),
    onSuccess: () => {
      setError(null);
      onMerged();
    },
    onError: (e) => setError(e instanceof Error ? e.message : 'Merge failed'),
  });

  return (
    <div className="rounded-xl border border-border bg-card p-4">
      <div className="mb-3 text-xs font-medium text-muted-foreground">Shared phone ending in {group.key.slice(-4)}</div>
      <div className="space-y-1.5">
        {group.contacts.map((c) => (
          <div key={c.id} className="flex items-center gap-3 rounded-lg border border-border px-3 py-2 text-sm">
            <span className="min-w-0 flex-1 truncate font-medium">{contactName(c)}</span>
            <span className="truncate text-xs text-muted-foreground">{c.email || '—'}</span>
            <label className="flex items-center gap-1.5 text-xs">
              <input
                type="radio"
                name={`survivor-${group.key}`}
                checked={survivorId === c.id}
                onChange={() => setSurvivorId(c.id)}
              />
              Keep
            </label>
          </div>
        ))}
      </div>

      {error && <p className="mt-2 text-xs text-destructive">{error}</p>}

      <div className="mt-3 flex items-center justify-between gap-3">
        <p className="text-xs text-muted-foreground">
          {group.contacts.length > 2
            ? 'Merges one pair at a time — merge again to fold in the rest.'
            : null}
        </p>
        <Button
          size="sm"
          onClick={() => {
            const nextLoser = group.contacts.find((c) => c.id !== survivorId)?.id;
            if (nextLoser) mergeMutation.mutate(nextLoser);
          }}
          disabled={mergeMutation.isPending || group.contacts.length < 2}
        >
          <GitMerge aria-hidden size={14} /> {mergeMutation.isPending ? 'Merging…' : 'Merge into kept contact'}
        </Button>
      </div>
    </div>
  );
}
