import { useMemo, useState } from 'react';
import { ArrowRight, Plus, Trash2, Wand2 } from 'lucide-react';
import { Badge, Button, Input, Select } from '@/components/ui';
import { useMapping, useSaveMapping } from '../../features/integrations/queries';
import {
  TRANSFORM_LABELS,
  type FieldMap,
  type Transform,
} from '../../features/integrations/types';

// The mapping table: what a source calls a field vs what this CRM calls it.
//
// The design turns on one thing — the delivery log already records every payload
// verbatim, so we KNOW the real key names this source sends. An admin picks from
// what actually arrived instead of recalling what Facebook calls a question and
// typing it exactly. A mistyped source key never matches anything and the lead
// quarantines silently, so removing the typing removes the failure.

interface Props {
  sourceId: string;
}

export default function FieldMappingTable({ sourceId }: Props) {
  const { data, isLoading, error } = useMapping(sourceId);
  const saveMapping = useSaveMapping();

  // Draft state, seeded from the server once loaded. Kept local so an admin can
  // stage several rows and save once.
  const [draft, setDraft] = useState<FieldMap | null>(null);
  const [newKey, setNewKey] = useState('');
  const [saveError, setSaveError] = useState('');
  const [problems, setProblems] = useState<Record<string, string>>({});
  const [saved, setSaved] = useState(false);

  const map = draft ?? data?.field_map ?? {};

  // Keys the source has sent that nothing maps and nothing writes — the rows worth
  // an admin's attention, since each is a field currently being thrown away.
  const unmapped = useMemo(() => {
    if (!data) return [];
    const targetKeys = new Set(data.target_fields.map((f) => f.key));
    return data.observed.filter((k) => !(k in map) && !targetKeys.has(k));
  }, [data, map]);

  const update = (src: string, patch: Partial<{ target_key: string; transform: Transform }>) => {
    setSaved(false);
    setDraft({ ...map, [src]: { ...(map[src] ?? { target_key: '' }), ...patch } });
  };

  const remove = (src: string) => {
    setSaved(false);
    const next = { ...map };
    delete next[src];
    setDraft(next);
  };

  const addRow = (src: string) => {
    if (!src.trim()) return;
    setSaved(false);
    setDraft({ ...map, [src.trim()]: { target_key: '' } });
    setNewKey('');
  };

  const save = async () => {
    setSaveError('');
    setProblems({});
    try {
      await saveMapping.mutateAsync({ id: sourceId, field_map: map });
      setDraft(null);
      setSaved(true);
    } catch (err) {
      // The server validates the map against the target object and returns
      // per-source-key problems. Surface them ON the rows, not as one opaque
      // banner — the admin needs to know WHICH row is wrong.
      const detail = (err as { details?: Record<string, string> })?.details;
      if (detail && typeof detail === 'object') setProblems(detail);
      setSaveError(err instanceof Error ? err.message : 'Failed to save the mapping');
    }
  };

  if (isLoading) return <p className="text-sm text-muted-foreground">Loading fields…</p>;
  if (error) {
    return (
      <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
        {error instanceof Error ? error.message : 'Failed to load the field mapping'}
      </div>
    );
  }
  if (!data) return null;

  const rows = Object.entries(map);
  const dirty = draft !== null;

  return (
    <div className="space-y-4">
      <div>
        <h3 className="text-sm font-medium text-foreground">Field mapping</h3>
        <p className="text-xs text-muted-foreground mt-0.5">
          Only needed when this source calls a field something else. Anything you don't map
          that already matches a contact field is used as-is.
        </p>
      </div>

      {/* The high-value row: fields this source is sending that are being thrown away. */}
      {unmapped.length > 0 && (
        <div className="rounded-xl border border-amber-500/40 bg-amber-500/5 p-3 space-y-2">
          <p className="text-xs font-medium text-foreground">
            This source is sending fields nothing is capturing
          </p>
          <p className="text-xs text-muted-foreground">
            Seen in recent deliveries but not saved to the contact. Map one to start keeping it.
          </p>
          <div className="flex flex-wrap gap-1.5">
            {unmapped.map((k) => (
              <Button key={k} variant="outline" size="sm" onClick={() => addRow(k)}>
                <Plus />
                {k}
              </Button>
            ))}
          </div>
        </div>
      )}

      {rows.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          No mappings — every field is passed through under the name it arrives with.
        </p>
      ) : (
        <div className="space-y-2">
          {rows.map(([src, entry]) => (
            <div key={src} className="rounded-lg border border-border p-3 space-y-2">
              <div className="flex items-center gap-2 flex-wrap">
                <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs text-foreground">
                  {src}
                </code>
                <ArrowRight className="w-3.5 h-3.5 text-muted-foreground shrink-0" />
                <Select
                  className="min-w-[10rem]"
                  aria-label={`Target field for ${src}`}
                  value={entry.target_key}
                  onChange={(e) => update(src, { target_key: e.target.value })}
                >
                  <option value="">Choose a field…</option>
                  {data.target_fields.map((f) => (
                    <option key={f.key} value={f.key}>
                      {f.label || f.key}
                    </option>
                  ))}
                </Select>
                <Select
                  className="min-w-[9rem]"
                  aria-label={`Transform for ${src}`}
                  value={entry.transform ?? ''}
                  onChange={(e) => update(src, { transform: e.target.value as Transform })}
                >
                  {(Object.keys(TRANSFORM_LABELS) as Transform[]).map((t) => (
                    <option key={t} value={t}>
                      {TRANSFORM_LABELS[t]}
                    </option>
                  ))}
                </Select>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={`Remove the mapping for ${src}`}
                  onClick={() => remove(src)}
                >
                  <Trash2 />
                </Button>
              </div>
              {entry.transform === 'split_name' && (
                <p className="text-xs text-muted-foreground flex items-center gap-1.5">
                  <Wand2 className="w-3 h-3" />
                  Splits on the last space — "Ada Byron King" becomes Ada Byron / King.
                </p>
              )}
              {problems[src] && <p className="text-xs text-destructive">{problems[src]}</p>}
            </div>
          ))}
        </div>
      )}

      <div className="flex items-center gap-2">
        <Input
          value={newKey}
          onChange={(e) => setNewKey(e.target.value)}
          placeholder="Add a field name this source sends"
          className="max-w-xs"
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              addRow(newKey);
            }
          }}
        />
        <Button variant="outline" size="sm" onClick={() => addRow(newKey)} disabled={!newKey.trim()}>
          <Plus />
          Add
        </Button>
      </div>

      {saveError && (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {saveError}
        </div>
      )}

      <div className="flex items-center gap-3">
        <Button size="sm" onClick={save} disabled={!dirty || saveMapping.isPending}>
          {saveMapping.isPending ? 'Saving…' : 'Save mapping'}
        </Button>
        {saved && !dirty && <Badge variant="success">Saved</Badge>}
        {dirty && <span className="text-xs text-muted-foreground">Unsaved changes</span>}
      </div>
    </div>
  );
}
