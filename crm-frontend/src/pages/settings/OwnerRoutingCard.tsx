import { useEffect, useMemo, useState } from 'react';
import { ArrowDown, ArrowUp, UserPlus, X } from 'lucide-react';
import { Badge, Button, Select } from '@/components/ui';
import OwnerPicker from '../../components/records/OwnerPicker';
import { getWorkspaceMembers, type WorkspaceMember } from '../../lib/api';
import { useUpdateSource } from '../../features/integrations/queries';
import type { LeadSource } from '../../features/integrations/types';

// Who a captured lead lands on. This is the difference between a lead and a lost
// lead: an unowned contact is invisible to every own-scoped rep, so the point of
// this card is that an admin can never leave a source without an answer.

const memberName = (m: WorkspaceMember) =>
  m.full_name || `${m.first_name} ${m.last_name}`.trim() || m.email;

interface Props {
  source: LeadSource;
}

export default function OwnerRoutingCard({ source }: Props) {
  const updateSource = useUpdateSource();
  const [members, setMembers] = useState<WorkspaceMember[]>([]);
  const [error, setError] = useState('');

  // Editor state seeds from the SOURCE, never from a member-list join.
  // getWorkspaceMembers() resolves to [] when it fails, and an intersection-as-state
  // would then badge a healthy rotation as empty and PATCH owner_pool: [] on the
  // next save — destroying the routing config because a list request blipped.
  const [pool, setPool] = useState<string[]>(source.owner_pool ?? []);
  const [rotating, setRotating] = useState((source.owner_pool ?? []).length > 0);

  useEffect(() => {
    setPool(source.owner_pool ?? []);
    setRotating((source.owner_pool ?? []).length > 0);
  }, [source.id, source.owner_pool]);

  useEffect(() => {
    let cancelled = false;
    getWorkspaceMembers()
      .then((m) => { if (!cancelled) setMembers(m.filter((x) => x.status === 'active')); })
      .catch(() => { if (!cancelled) setMembers([]); });
    return () => { cancelled = true; };
  }, []);

  const nameOf = useMemo(() => {
    const byID = new Map(members.map((m) => [m.user_id, memberName(m)]));
    // A member we could not load is "unknown", NOT inactive — the server is the only
    // authority on who is inactive (owner_pool_inactive).
    return (id: string) => byID.get(id) ?? 'Unknown member';
  }, [members]);

  const inactive = useMemo(
    () => new Set(source.owner_pool_inactive ?? []),
    [source.owner_pool_inactive],
  );

  const available = members.filter((m) => !pool.includes(m.user_id));
  const dirty = JSON.stringify(pool) !== JSON.stringify(source.owner_pool ?? []);

  const save = async (nextPool: string[]) => {
    setError('');
    try {
      await updateSource.mutateAsync({ id: source.id, input: { owner_pool: nextPool } });
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save the rotation');
    }
  };

  const move = (i: number, delta: number) => {
    const next = [...pool];
    const j = i + delta;
    if (j < 0 || j >= next.length) return;
    [next[i], next[j]] = [next[j], next[i]];
    setPool(next);
  };

  const everyoneInactive = pool.length > 0 && pool.every((id) => inactive.has(id));

  return (
    <div className="rounded-xl border border-border p-4 space-y-4">
      <div>
        <h3 className="text-sm font-medium text-foreground">Who gets these leads</h3>
        <p className="text-xs text-muted-foreground mt-0.5">
          Assigned when the lead creates a new contact. Reps who only see their own records
          cannot see an unassigned lead at all.
        </p>
      </div>

      {/* Mode is derived from the pool, not stored — one less thing that can disagree
          with reality. */}
      <div className="flex gap-2">
        <Button
          variant={rotating ? 'outline' : 'default'}
          size="sm"
          onClick={() => { setRotating(false); setPool([]); void save([]); }}
          disabled={updateSource.isPending}
        >
          One owner
        </Button>
        <Button
          variant={rotating ? 'default' : 'outline'}
          size="sm"
          onClick={() => setRotating(true)}
          disabled={updateSource.isPending}
        >
          Rotate through a team
        </Button>
      </div>

      {rotating && (
        <div className="space-y-2">
          <p className="text-xs text-muted-foreground">
            Each new lead goes to the next person in this list, in order. Anyone suspended or
            no longer in the workspace is skipped.
          </p>

          {everyoneInactive && (
            <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-xs text-destructive">
              Nobody in this rotation can receive leads right now. New leads will go to the
              fallback owner below — or be left unassigned if there isn't one.
            </div>
          )}

          {pool.length === 0 ? (
            <p className="text-xs text-muted-foreground italic">
              No one in the rotation yet — add someone below.
            </p>
          ) : (
            <ol className="space-y-1">
              {pool.map((id, i) => (
                <li
                  key={id}
                  className="flex items-center gap-2 rounded-lg border border-border bg-background px-3 py-1.5"
                >
                  <span className="text-xs text-muted-foreground w-5">{i + 1}.</span>
                  <span className="text-sm text-foreground flex-1">{nameOf(id)}</span>
                  {inactive.has(id) && (
                    <Badge variant="warning">Can't receive leads</Badge>
                  )}
                  <Button variant="ghost" size="sm" onClick={() => move(i, -1)} disabled={i === 0} aria-label="Move up">
                    <ArrowUp className="w-3.5 h-3.5" />
                  </Button>
                  <Button variant="ghost" size="sm" onClick={() => move(i, 1)} disabled={i === pool.length - 1} aria-label="Move down">
                    <ArrowDown className="w-3.5 h-3.5" />
                  </Button>
                  <Button variant="ghost" size="sm" onClick={() => setPool(pool.filter((p) => p !== id))} aria-label="Remove from rotation">
                    <X className="w-3.5 h-3.5" />
                  </Button>
                </li>
              ))}
            </ol>
          )}

          {available.length > 0 && (
            <div className="flex items-center gap-2">
              <UserPlus className="w-4 h-4 text-muted-foreground" />
              <Select
                aria-label="Add someone to the rotation"
                value=""
                onChange={(e) => { if (e.target.value) setPool([...pool, e.target.value]); }}
                className="flex-1"
              >
                <option value="">Add someone…</option>
                {available.map((m) => (
                  <option key={m.user_id} value={m.user_id}>{memberName(m)}</option>
                ))}
              </Select>
            </div>
          )}

          {dirty && (
            <div className="flex items-center gap-2">
              <Button size="sm" onClick={() => void save(pool)} disabled={updateSource.isPending}>
                {updateSource.isPending ? 'Saving…' : 'Save rotation'}
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setPool(source.owner_pool ?? [])}>
                Cancel
              </Button>
            </div>
          )}
        </div>
      )}

      {/* Rendered in BOTH modes on purpose. A toggle that hid (and nulled) this would
          destroy the safety net at the exact moment an admin adopts rotations. */}
      <div>
        <label className="text-xs text-muted-foreground" htmlFor="fallback-owner">
          {rotating ? 'Fallback owner — used when nobody in the rotation is available' : 'Owner'}
        </label>
        <div className="mt-1">
          <OwnerPicker
            id="fallback-owner"
            value={source.default_owner_id ?? null}
            onChange={(userID) => {
              setError('');
              updateSource
                .mutateAsync({ id: source.id, input: { default_owner_id: userID } })
                .catch((err) => setError(err instanceof Error ? err.message : 'Failed to save the owner'));
            }}
            disabled={updateSource.isPending}
          />
        </div>
        {!source.default_owner_id && !rotating && (
          <p className="text-xs text-amber-700 dark:text-amber-400 mt-1">
            Leads from this source are left unassigned, so reps who only see their own
            records will not see them.
          </p>
        )}
      </div>

      {error && (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {error}
        </div>
      )}
    </div>
  );
}
